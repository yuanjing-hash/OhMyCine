// SPDX-License-Identifier: MIT

#include <dml_provider_factory.h>
#include <onnxruntime_cxx_api.h>

#include <wrl/client.h>
#include <windows.h>
#include <d3d11on12.h>
#include <d3dcompiler.h>
#include <dxgi1_6.h>
#include <windows.graphics.capture.interop.h>
#include <windows.graphics.directx.direct3d11.interop.h>
#include <winrt/Windows.Foundation.h>
#include <winrt/Windows.Graphics.Capture.h>
#include <winrt/Windows.Graphics.DirectX.Direct3D11.h>

#include <algorithm>
#include <array>
#include <cmath>
#include <cstddef>
#include <cstdio>
#include <cstring>
#include <exception>
#include <atomic>
#include <chrono>
#include <mutex>
#include <stdexcept>
#include <string>
#include <thread>
#include <vector>

#pragma comment(lib, "d3d12.lib")
#pragma comment(lib, "d3d11.lib")
#pragma comment(lib, "d3dcompiler.lib")
#pragma comment(lib, "dxgi.lib")
#pragma comment(lib, "DirectML.lib")
#pragma comment(lib, "runtimeobject.lib")

namespace {

using Microsoft::WRL::ComPtr;

void throw_if_failed(HRESULT result, const char* operation) {
    if (FAILED(result)) {
        char message[160]{};
        std::snprintf(
            message,
            sizeof(message),
            "%s failed with HRESULT 0x%08lx",
            operation,
            static_cast<unsigned long>(result));
        throw std::runtime_error(message);
    }
}

void write_reason(char* destination, size_t capacity, const char* reason) {
    if (destination == nullptr || capacity == 0)
        return;
    std::snprintf(destination, capacity, "%s", reason == nullptr ? "unknown" : reason);
}

bool expected_names(Ort::Session& session) {
    Ort::AllocatorWithDefaultOptions allocator;
    if (session.GetInputCount() != 3 || session.GetOutputCount() != 2)
        return false;
    const char* inputs[] = {"earlier_proxy", "later_proxy", "timestep"};
    const char* outputs[] = {"flow_pixels", "blend_mask"};
    for (size_t index = 0; index < 3; ++index) {
        auto name = session.GetInputNameAllocated(index, allocator);
        if (std::strcmp(name.get(), inputs[index]) != 0)
            return false;
    }
    for (size_t index = 0; index < 2; ++index) {
        auto name = session.GetOutputNameAllocated(index, allocator);
        if (std::strcmp(name.get(), outputs[index]) != 0)
            return false;
    }
    return true;
}

struct GpuTensor {
    ComPtr<ID3D12Resource> resource;
    void* allocation = nullptr;
};

ComPtr<ID3D12Resource> create_buffer(
    ID3D12Device* device,
    size_t byte_count,
    D3D12_HEAP_TYPE heap_type,
    D3D12_RESOURCE_STATES initial_state,
    D3D12_RESOURCE_FLAGS flags = D3D12_RESOURCE_FLAG_NONE) {
    const D3D12_HEAP_PROPERTIES heap{
        heap_type,
        D3D12_CPU_PAGE_PROPERTY_UNKNOWN,
        D3D12_MEMORY_POOL_UNKNOWN,
        1,
        1,
    };
    const D3D12_RESOURCE_DESC desc{
        D3D12_RESOURCE_DIMENSION_BUFFER,
        0,
        static_cast<UINT64>(byte_count),
        1,
        1,
        1,
        DXGI_FORMAT_UNKNOWN,
        {1, 0},
        D3D12_TEXTURE_LAYOUT_ROW_MAJOR,
        flags,
    };
    ComPtr<ID3D12Resource> resource;
    throw_if_failed(device->CreateCommittedResource(
        &heap,
        D3D12_HEAP_FLAG_NONE,
        &desc,
        initial_state,
        nullptr,
        IID_PPV_ARGS(&resource)), "ID3D12Device::CreateCommittedResource");
    return resource;
}

void wait_for_queue_idle(ID3D12Device* device, ID3D12CommandQueue* queue) {
    ComPtr<ID3D12Fence> fence;
    throw_if_failed(device->CreateFence(0, D3D12_FENCE_FLAG_NONE, IID_PPV_ARGS(&fence)), "ID3D12Device::CreateFence");
    const HANDLE event = CreateEventW(nullptr, FALSE, FALSE, nullptr);
    if (event == nullptr)
        throw_if_failed(HRESULT_FROM_WIN32(GetLastError()), "CreateEventW");
    throw_if_failed(queue->Signal(fence.Get(), 1), "ID3D12CommandQueue::Signal");
    throw_if_failed(fence->SetEventOnCompletion(1, event), "ID3D12Fence::SetEventOnCompletion");
    WaitForSingleObject(event, INFINITE);
    CloseHandle(event);
}

void execute_and_wait(
    ID3D12Device* device,
    ID3D12CommandQueue* queue,
    ID3D12GraphicsCommandList* list) {
    throw_if_failed(list->Close(), "ID3D12GraphicsCommandList::Close");
    ID3D12CommandList* lists[] = {list};
    queue->ExecuteCommandLists(1, lists);
    wait_for_queue_idle(device, queue);
}

void upload_inputs(
    ID3D12Device* device,
    ID3D12CommandQueue* queue,
    const std::vector<std::pair<ComPtr<ID3D12Resource>, const std::vector<float>*>>& uploads) {
    ComPtr<ID3D12CommandAllocator> allocator;
    ComPtr<ID3D12GraphicsCommandList> list;
    throw_if_failed(device->CreateCommandAllocator(
        D3D12_COMMAND_LIST_TYPE_DIRECT, IID_PPV_ARGS(&allocator)), "ID3D12Device::CreateCommandAllocator");
    throw_if_failed(device->CreateCommandList(
        0, D3D12_COMMAND_LIST_TYPE_DIRECT, allocator.Get(), nullptr, IID_PPV_ARGS(&list)), "ID3D12Device::CreateCommandList");
    std::vector<ComPtr<ID3D12Resource>> staging;
    staging.reserve(uploads.size());
    for (const auto& [destination, source] : uploads) {
        const size_t bytes = source->size() * sizeof(float);
        auto upload = create_buffer(
            device, bytes, D3D12_HEAP_TYPE_UPLOAD, D3D12_RESOURCE_STATE_GENERIC_READ);
        void* mapped = nullptr;
        const D3D12_RANGE no_read{0, 0};
        throw_if_failed(upload->Map(0, &no_read, &mapped), "ID3D12Resource::Map");
        std::memcpy(mapped, source->data(), bytes);
        upload->Unmap(0, nullptr);
        list->CopyBufferRegion(destination.Get(), 0, upload.Get(), 0, bytes);
        const D3D12_RESOURCE_BARRIER barrier{
            D3D12_RESOURCE_BARRIER_TYPE_TRANSITION,
            D3D12_RESOURCE_BARRIER_FLAG_NONE,
            {.Transition = {
                destination.Get(),
                D3D12_RESOURCE_BARRIER_ALL_SUBRESOURCES,
                D3D12_RESOURCE_STATE_COPY_DEST,
                D3D12_RESOURCE_STATE_UNORDERED_ACCESS,
            }},
        };
        list->ResourceBarrier(1, &barrier);
        staging.push_back(std::move(upload));
    }
    execute_and_wait(device, queue, list.Get());
}

const OrtDmlApi* dml_api() {
    const void* api = nullptr;
    Ort::ThrowOnError(Ort::GetApi().GetExecutionProviderApi("DML", ORT_API_VERSION, &api));
    return static_cast<const OrtDmlApi*>(api);
}

GpuTensor bind_gpu_tensor(
    const OrtDmlApi* api,
    ID3D12Device* device,
    size_t byte_count,
    D3D12_RESOURCE_STATES initial_state) {
    GpuTensor tensor;
    tensor.resource = create_buffer(
        device,
        byte_count,
        D3D12_HEAP_TYPE_DEFAULT,
        initial_state,
        D3D12_RESOURCE_FLAG_ALLOW_UNORDERED_ACCESS);
    Ort::ThrowOnError(api->CreateGPUAllocationFromD3DResource(
        tensor.resource.Get(), &tensor.allocation));
    return tensor;
}

void free_gpu_tensor(const OrtDmlApi* api, GpuTensor& tensor) noexcept {
    if (tensor.allocation != nullptr) {
        const OrtStatus* status = api->FreeGPUAllocation(tensor.allocation);
        if (status != nullptr)
            Ort::GetApi().ReleaseStatus(const_cast<OrtStatus*>(status));
        tensor.allocation = nullptr;
    }
}

constexpr wchar_t kOutputWindowClass[] = L"OhMyCineFrameGenerationOutput";
constexpr UINT kProxySize = 64;

struct ProxyExtent {
    UINT width;
    UINT height;
};

// RIFE's encoder/decoder concatenations require both spatial axes to stay aligned to 32.
// A square 48x48 proxy reaches mismatched 3x3/4x4 feature maps and DirectML rejects the
// Concat node with E_INVALIDARG. Keep the three product tiers distinct by using a half-area,
// orientation-aware 64x32 proxy for the balanced selector.
ProxyExtent proxy_extent(UINT selector, const winrt::Windows::Graphics::SizeInt32& source_size) {
    if (selector == 48) {
        return source_size.Width >= source_size.Height
            ? ProxyExtent{64, 32}
            : ProxyExtent{32, 64};
    }
    return ProxyExtent{selector, selector};
}

constexpr char kProxyShader[] = R"(
Texture2D<float4> Source : register(t0);
RWStructuredBuffer<float> Proxy : register(u0);
cbuffer Params : register(b0) { uint2 sourceSize; uint2 proxySize; float referenceWhite; float sourcePeak; };
[numthreads(8, 8, 1)]
void main(uint3 id : SV_DispatchThreadID) {
    if (any(id.xy >= proxySize)) return;
    uint2 pixel = min(uint2((float2(id.xy) + 0.5) * float2(sourceSize) / float2(proxySize)), sourceSize - 1);
    float3 linearHdr = max(Source.Load(int3(pixel, 0)).rgb, 0.0);
    float peakScale = max(sourcePeak / max(referenceWhite, 1.0), 1.0);
    float3 compressed = saturate(log2(1.0 + min(linearHdr, peakScale)) / log2(1.0 + peakScale));
    uint index = id.y * proxySize.x + id.x;
    uint plane = proxySize.x * proxySize.y;
    Proxy[index] = compressed.r; Proxy[plane + index] = compressed.g; Proxy[2 * plane + index] = compressed.b;
})";

constexpr char kTimestepShader[] = R"(
RWStructuredBuffer<float> Timestep : register(u0);
cbuffer Params : register(b0) { float value; uint elementCount; float2 padding; };
[numthreads(64, 1, 1)]
void main(uint3 id : SV_DispatchThreadID) {
    if (id.x < elementCount) Timestep[id.x] = value;
})";

constexpr char kCompositeShader[] = R"(
Texture2D<float4> Source0 : register(t0); Texture2D<float4> Source1 : register(t1);
StructuredBuffer<float> Flow : register(t2); StructuredBuffer<float> Mask : register(t3);
SamplerState LinearClamp : register(s0); RWTexture2D<float4> Output : register(u0);
cbuffer Params : register(b0) { uint2 outputSize; uint2 proxySize; float timestep; float confidenceThreshold; uint sceneCut; float flowTimestep; };
float conf(float3 a, float3 b) { a /= 1.0 + max(a, 0.0); b /= 1.0 + max(b, 0.0); return saturate(1.0 - dot(abs(a-b), float3(.2126,.7152,.0722))*3.0); }
[numthreads(8, 8, 1)]
void main(uint3 id : SV_DispatchThreadID) {
    if (any(id.xy >= outputSize)) return;
    float2 uv = (float2(id.xy) + 0.5) / float2(outputSize);
    uint2 p = min(uint2(uv * float2(proxySize)), proxySize - 1); uint i = p.y * proxySize.x + p.x; uint plane = proxySize.x * proxySize.y;
    float4 flow = float4(Flow[i], Flow[plane+i], Flow[2*plane+i], Flow[3*plane+i]);
    float3 a0 = Source0.SampleLevel(LinearClamp, uv, 0).rgb; float3 b0 = Source1.SampleLevel(LinearClamp, uv, 0).rgb;
    float flowTime = clamp(flowTimestep, 0.001, 0.999);
    float2 earlierFlow = flow.xy * (timestep / flowTime);
    float2 laterFlow = flow.zw * ((1.0 - timestep) / (1.0 - flowTime));
    float3 a = Source0.SampleLevel(LinearClamp, uv + earlierFlow / float2(proxySize), 0).rgb;
    float3 b = Source1.SampleLevel(LinearClamp, uv + laterFlow / float2(proxySize), 0).rgb;
    float occlusion = saturate(Mask[i]);
    float blend = saturate((1.0 - timestep) + (occlusion - 0.5) * 4.0 * timestep * (1.0 - timestep));
    float3 generated = a * blend + b * (1.0 - blend);
    float valid = conf(a,b) >= confidenceThreshold && sceneCut == 0;
    Output[id.xy] = float4(clamp(valid ? generated : (timestep < 0.5 ? a0 : b0), -65504.0, 65504.0), 1.0);
})";

ComPtr<ID3DBlob> compile_compute_shader(const char* source, const char* name) {
    ComPtr<ID3DBlob> bytecode;
    ComPtr<ID3DBlob> errors;
    const HRESULT result = D3DCompile(
        source, std::strlen(source), name, nullptr, nullptr, "main", "cs_5_0",
        D3DCOMPILE_ENABLE_STRICTNESS | D3DCOMPILE_WARNINGS_ARE_ERRORS, 0, &bytecode, &errors);
    if (FAILED(result)) {
        const char* detail = errors ? static_cast<const char*>(errors->GetBufferPointer()) : name;
        throw std::runtime_error(detail);
    }
    return bytecode;
}

ComPtr<ID3D11Buffer> create_constant_buffer(ID3D11Device* device, UINT bytes) {
    D3D11_BUFFER_DESC desc{};
    desc.ByteWidth = (bytes + 15u) & ~15u;
    desc.Usage = D3D11_USAGE_DEFAULT;
    desc.BindFlags = D3D11_BIND_CONSTANT_BUFFER;
    ComPtr<ID3D11Buffer> buffer;
    throw_if_failed(device->CreateBuffer(&desc, nullptr, &buffer), "CreateBuffer(constants)");
    return buffer;
}

struct WrappedTensorViews {
    ComPtr<ID3D11Resource> resource;
    ComPtr<ID3D11UnorderedAccessView> uav;
    ComPtr<ID3D11ShaderResourceView> srv;
};

WrappedTensorViews wrap_tensor(
    ID3D11On12Device* on12,
    ID3D11Device* device,
    ID3D12Resource* resource,
    UINT elements,
    bool create_uav,
    bool create_srv) {
    WrappedTensorViews views;
    const D3D11_RESOURCE_FLAGS flags{
        (create_uav ? D3D11_BIND_UNORDERED_ACCESS : 0u)
            | (create_srv ? D3D11_BIND_SHADER_RESOURCE : 0u),
        0,
        0,
        0,
    };
    throw_if_failed(on12->CreateWrappedResource(
        resource,
        &flags,
        D3D12_RESOURCE_STATE_UNORDERED_ACCESS,
        D3D12_RESOURCE_STATE_UNORDERED_ACCESS,
        IID_PPV_ARGS(&views.resource)), "CreateWrappedResource(tensor)");
    if (create_uav) {
        D3D11_UNORDERED_ACCESS_VIEW_DESC desc{};
        desc.Format = DXGI_FORMAT_R32_FLOAT;
        desc.ViewDimension = D3D11_UAV_DIMENSION_BUFFER;
        desc.Buffer.NumElements = elements;
        throw_if_failed(device->CreateUnorderedAccessView(
            views.resource.Get(), &desc, &views.uav), "CreateUnorderedAccessView(tensor)");
    }
    if (create_srv) {
        D3D11_SHADER_RESOURCE_VIEW_DESC desc{};
        desc.Format = DXGI_FORMAT_R32_FLOAT;
        desc.ViewDimension = D3D11_SRV_DIMENSION_BUFFER;
        desc.Buffer.NumElements = elements;
        throw_if_failed(device->CreateShaderResourceView(
            views.resource.Get(), &desc, &views.srv), "CreateShaderResourceView(tensor)");
    }
    return views;
}

LRESULT CALLBACK output_window_proc(HWND hwnd, UINT message, WPARAM wparam, LPARAM lparam) {
    return DefWindowProcW(hwnd, message, wparam, lparam);
}

void register_output_window_class() {
    static std::once_flag once;
    static DWORD error = ERROR_SUCCESS;
    std::call_once(once, [] {
        WNDCLASSW window_class{};
        window_class.lpfnWndProc = output_window_proc;
        window_class.hInstance = GetModuleHandleW(nullptr);
        window_class.lpszClassName = kOutputWindowClass;
        if (RegisterClassW(&window_class) == 0) {
            error = GetLastError();
            if (error == ERROR_CLASS_ALREADY_EXISTS)
                error = ERROR_SUCCESS;
        }
    });
    if (error != ERROR_SUCCESS)
        throw_if_failed(HRESULT_FROM_WIN32(error), "RegisterClassW(frame-generation output)");
}

struct WindowsFrameGenerationSession {
    WindowsFrameGenerationSession(HWND source, std::wstring model, UINT target, bool hdr, UINT proxy)
        : source_hwnd(source), model_path(std::move(model)), target_fps(target), hdr_input(hdr), proxy_size(proxy) {}

    HWND source_hwnd = nullptr;
    std::wstring model_path;
    UINT target_fps = 60;
    bool hdr_input = false;
    UINT proxy_size = kProxySize;
    std::atomic<HWND> output_hwnd{nullptr};
    std::atomic<bool> stop_requested{false};
    std::atomic<bool> captured_pair{false};
    std::atomic<bool> hidden_first_present{false};
    std::atomic<bool> generated_first_present{false};
    std::atomic<bool> cadence_stalled{false};
    std::atomic<bool> device_lost{false};
    std::atomic<bool> finished{false};
    std::atomic<uint64_t> successful_present_count{0};
    std::atomic<uint64_t> generated_present_count{0};
    std::atomic<uint64_t> dropped_output_ticks{0};
    std::atomic<uint64_t> inference_sample_count{0};
    std::atomic<uint64_t> latest_inference_micros{0};
    std::atomic<int64_t> first_successful_present_qpc{0};
    std::atomic<int64_t> last_successful_present_qpc{0};
    std::atomic<int64_t> last_generated_present_qpc{0};
    std::atomic<bool> inference_in_flight{false};
    std::atomic<int64_t> inference_started_qpc{0};
    std::atomic<bool> timing_reliable{false};
    std::atomic<bool> paused{false};
    std::atomic<double> media_pts_seconds{0.0};
    std::atomic<double> source_fps{0.0};
    std::atomic<int64_t> timing_qpc{0};
    std::mutex reason_mutex;
    std::string reason = "Waiting for live WGC FP16 frames.";
    Ort::RunOptions run_options;
    std::thread worker;
    std::thread watchdog;

    void set_reason(std::string value) {
        std::lock_guard lock(reason_mutex);
        reason = std::move(value);
    }

    std::string get_reason() {
        std::lock_guard lock(reason_mutex);
        return reason;
    }
};

void hide_generated_output(
    WindowsFrameGenerationSession* state,
    const char* reason,
    bool stalled) noexcept;

void run_inference_watchdog(WindowsFrameGenerationSession* state) noexcept {
    LARGE_INTEGER frequency{};
    if (!QueryPerformanceFrequency(&frequency))
        return;
    while (!state->finished.load(std::memory_order_acquire)
           && !state->stop_requested.load(std::memory_order_acquire)) {
        if (state->inference_in_flight.load(std::memory_order_acquire)) {
            const auto started = state->inference_started_qpc.load(std::memory_order_acquire);
            LARGE_INTEGER now{};
            QueryPerformanceCounter(&now);
            const double elapsed_ms = static_cast<double>(now.QuadPart - started) * 1000.0
                / static_cast<double>(frequency.QuadPart);
            if (started > 0 && elapsed_ms > 250.0) {
                hide_generated_output(
                    state,
                    "DirectML inference exceeded the 250 ms safety deadline; cancellation was requested and mpv source playback was restored.",
                    true);
                try {
                    state->run_options.SetTerminate();
                }
                catch (...) {
                    state->set_reason("DirectML inference exceeded the safety deadline and cancellation failed; the generated overlay remains bypassed.");
                }
                return;
            }
        }
        std::this_thread::sleep_for(std::chrono::milliseconds(10));
    }
}

void hide_generated_output(
    WindowsFrameGenerationSession* state,
    const char* reason,
    bool stalled) noexcept {
    const HWND output = state->output_hwnd.load(std::memory_order_acquire);
    if (output != nullptr)
        ShowWindowAsync(output, SW_HIDE);
    state->generated_first_present.store(false, std::memory_order_release);
    state->hidden_first_present.store(false, std::memory_order_release);
    state->cadence_stalled.store(stalled, std::memory_order_release);
    state->set_reason(reason);
}

int64_t adapter_luid(ID3D11Device* device) {
    ComPtr<IDXGIDevice> dxgi_device;
    throw_if_failed(device->QueryInterface(IID_PPV_ARGS(&dxgi_device)), "ID3D11Device::QueryInterface(IDXGIDevice)");
    ComPtr<IDXGIAdapter> adapter;
    throw_if_failed(dxgi_device->GetAdapter(&adapter), "IDXGIDevice::GetAdapter");
    DXGI_ADAPTER_DESC desc{};
    throw_if_failed(adapter->GetDesc(&desc), "IDXGIAdapter::GetDesc");
    return (static_cast<int64_t>(desc.AdapterLuid.HighPart) << 32)
        | static_cast<uint32_t>(desc.AdapterLuid.LowPart);
}

ComPtr<IDXGIAdapter1> display_adapter_for_window(HWND hwnd) {
    const HMONITOR monitor = MonitorFromWindow(hwnd, MONITOR_DEFAULTTONEAREST);
    ComPtr<IDXGIFactory6> factory;
    throw_if_failed(CreateDXGIFactory2(0, IID_PPV_ARGS(&factory)), "CreateDXGIFactory2(adapter selection)");
    for (UINT adapter_index = 0;; ++adapter_index) {
        ComPtr<IDXGIAdapter1> adapter;
        const HRESULT adapter_result = factory->EnumAdapters1(adapter_index, &adapter);
        if (adapter_result == DXGI_ERROR_NOT_FOUND)
            break;
        throw_if_failed(adapter_result, "EnumAdapters1");
        for (UINT output_index = 0;; ++output_index) {
            ComPtr<IDXGIOutput> output;
            const HRESULT output_result = adapter->EnumOutputs(output_index, &output);
            if (output_result == DXGI_ERROR_NOT_FOUND)
                break;
            throw_if_failed(output_result, "EnumOutputs");
            DXGI_OUTPUT_DESC output_desc{};
            throw_if_failed(output->GetDesc(&output_desc), "IDXGIOutput::GetDesc");
            if (output_desc.Monitor == monitor)
                return adapter;
        }
    }
    throw std::runtime_error("no DXGI adapter owns the mpv window monitor");
}

void run_product_capture_session(WindowsFrameGenerationSession* state) noexcept {
    using namespace winrt;
    using namespace winrt::Windows::Graphics;
    using namespace winrt::Windows::Graphics::Capture;
    using namespace winrt::Windows::Graphics::DirectX;
    using namespace winrt::Windows::Graphics::DirectX::Direct3D11;
    try {
        init_apartment(apartment_type::multi_threaded);
        if (!IsWindow(state->source_hwnd))
            throw std::runtime_error("mpv source HWND is no longer valid");

        const auto adapter = display_adapter_for_window(state->source_hwnd);
        ComPtr<ID3D11Device> bootstrap_device;
        ComPtr<ID3D11DeviceContext> bootstrap_context;
        D3D_FEATURE_LEVEL selected_level{};
        throw_if_failed(D3D11CreateDevice(
            adapter.Get(),
            D3D_DRIVER_TYPE_UNKNOWN,
            nullptr,
            D3D11_CREATE_DEVICE_BGRA_SUPPORT,
            nullptr,
            0,
            D3D11_SDK_VERSION,
            &bootstrap_device,
            &selected_level,
            &bootstrap_context), "D3D11CreateDevice(frame-generation bootstrap)");
        ComPtr<ID3D12Device> d3d12_device;
        throw_if_failed(D3D12CreateDevice(
            adapter.Get(), D3D_FEATURE_LEVEL_11_0, IID_PPV_ARGS(&d3d12_device)), "D3D12CreateDevice(product)");
        const D3D12_COMMAND_QUEUE_DESC queue_desc{
            D3D12_COMMAND_LIST_TYPE_DIRECT,
            0,
            D3D12_COMMAND_QUEUE_FLAG_NONE,
            0,
        };
        ComPtr<ID3D12CommandQueue> queue;
        throw_if_failed(d3d12_device->CreateCommandQueue(&queue_desc, IID_PPV_ARGS(&queue)), "CreateCommandQueue(product)");
        IUnknown* queues[] = {queue.Get()};
        ComPtr<ID3D11Device> bridge_device;
        ComPtr<ID3D11DeviceContext> bridge_context;
        throw_if_failed(D3D11On12CreateDevice(
            d3d12_device.Get(),
            D3D11_CREATE_DEVICE_BGRA_SUPPORT,
            &selected_level,
            1,
            queues,
            1,
            0,
            &bridge_device,
            &bridge_context,
            nullptr), "D3D11On12CreateDevice(product)");
        if (adapter_luid(bootstrap_device.Get()) != adapter_luid(bridge_device.Get()))
            throw std::runtime_error("D3D11On12 bridge adapter differs from mpv/WGC adapter");

        auto interop = get_activation_factory<GraphicsCaptureItem, IGraphicsCaptureItemInterop>();
        GraphicsCaptureItem item{nullptr};
        check_hresult(interop->CreateForWindow(
            state->source_hwnd,
            guid_of<GraphicsCaptureItem>(),
            put_abi(item)));
        const auto item_size = item.Size();
        if (item_size.Width <= 0 || item_size.Height <= 0)
            throw std::runtime_error("mpv WGC item has empty dimensions");

        ComPtr<IDXGIDevice> bridge_dxgi;
        throw_if_failed(bridge_device.As(&bridge_dxgi), "D3D11On12 IDXGIDevice");
        com_ptr<IInspectable> inspectable;
        check_hresult(CreateDirect3D11DeviceFromDXGIDevice(
            bridge_dxgi.Get(), inspectable.put()));
        const auto winrt_device = inspectable.as<IDirect3DDevice>();
        auto pool = Direct3D11CaptureFramePool::CreateFreeThreaded(
            winrt_device,
            DirectXPixelFormat::R16G16B16A16Float,
            4,
            item_size);
        auto capture = pool.CreateCaptureSession(item);
        capture.IsCursorCaptureEnabled(false);

        register_output_window_class();
        const HWND output_hwnd = CreateWindowExW(
            WS_EX_NOACTIVATE | WS_EX_TOOLWINDOW,
            kOutputWindowClass,
            L"OhMyCine hidden frame-generation output",
            WS_POPUP,
            0,
            0,
            item_size.Width,
            item_size.Height,
            state->source_hwnd,
            nullptr,
            GetModuleHandleW(nullptr),
            nullptr);
        if (output_hwnd == nullptr)
            throw_if_failed(HRESULT_FROM_WIN32(GetLastError()), "CreateWindowExW(frame-generation output)");
        state->output_hwnd.store(output_hwnd, std::memory_order_release);
        ShowWindow(output_hwnd, SW_HIDE);

        ComPtr<IDXGIFactory2> factory;
        throw_if_failed(CreateDXGIFactory2(0, IID_PPV_ARGS(&factory)), "CreateDXGIFactory2(product)");
        DXGI_SWAP_CHAIN_DESC1 swap_desc{};
        swap_desc.Width = static_cast<UINT>(item_size.Width);
        swap_desc.Height = static_cast<UINT>(item_size.Height);
        swap_desc.Format = DXGI_FORMAT_R16G16B16A16_FLOAT;
        swap_desc.SampleDesc = {1, 0};
        // Flip-model swap-chain buffers are not guaranteed to support UAV
        // binding.  Requesting DXGI_USAGE_UNORDERED_ACCESS makes
        // CreateSwapChainForHwnd fail with DXGI_ERROR_INVALID_CALL on a
        // number of otherwise capable drivers.  Composite into a dedicated
        // FP16 UAV below and copy it into the presentable buffer instead.
        swap_desc.BufferUsage = DXGI_USAGE_RENDER_TARGET_OUTPUT;
        swap_desc.BufferCount = 3;
        swap_desc.Scaling = DXGI_SCALING_STRETCH;
        swap_desc.SwapEffect = DXGI_SWAP_EFFECT_FLIP_DISCARD;
        swap_desc.AlphaMode = DXGI_ALPHA_MODE_IGNORE;
        ComPtr<IDXGISwapChain1> swapchain1;
        throw_if_failed(factory->CreateSwapChainForHwnd(
            queue.Get(), output_hwnd, &swap_desc, nullptr, nullptr, &swapchain1), "CreateSwapChainForHwnd(product)");
        ComPtr<IDXGISwapChain3> swapchain;
        throw_if_failed(swapchain1.As(&swapchain), "IDXGISwapChain3(product)");
        UINT color_support = 0;
        throw_if_failed(swapchain->CheckColorSpaceSupport(
            DXGI_COLOR_SPACE_RGB_FULL_G10_NONE_P709, &color_support), "CheckColorSpaceSupport(scRGB)");
        if ((color_support & DXGI_SWAP_CHAIN_COLOR_SPACE_SUPPORT_FLAG_PRESENT) == 0)
            throw std::runtime_error("FP16 scRGB swapchain cannot present on this output");
        throw_if_failed(swapchain->SetColorSpace1(
            DXGI_COLOR_SPACE_RGB_FULL_G10_NONE_P709), "SetColorSpace1(scRGB)");

        ComPtr<ID3D11On12Device> on12;
        throw_if_failed(bridge_device.As(&on12), "ID3D11On12Device(product)");
        std::array<ComPtr<ID3D11Resource>, 3> wrapped_buffers;
        for (UINT index = 0; index < wrapped_buffers.size(); ++index) {
            ComPtr<ID3D12Resource> back_buffer;
            throw_if_failed(swapchain->GetBuffer(index, IID_PPV_ARGS(&back_buffer)), "GetBuffer(product)");
            const D3D11_RESOURCE_FLAGS flags{
                D3D11_BIND_RENDER_TARGET, 0, 0, 0};
            throw_if_failed(on12->CreateWrappedResource(
                back_buffer.Get(),
                &flags,
                D3D12_RESOURCE_STATE_COPY_DEST,
                D3D12_RESOURCE_STATE_PRESENT,
                IID_PPV_ARGS(&wrapped_buffers[index])), "CreateWrappedResource(product)");
        }
        D3D11_TEXTURE2D_DESC composite_texture_desc{};
        composite_texture_desc.Width = static_cast<UINT>(item_size.Width);
        composite_texture_desc.Height = static_cast<UINT>(item_size.Height);
        composite_texture_desc.MipLevels = 1;
        composite_texture_desc.ArraySize = 1;
        composite_texture_desc.Format = DXGI_FORMAT_R16G16B16A16_FLOAT;
        composite_texture_desc.SampleDesc = {1, 0};
        composite_texture_desc.Usage = D3D11_USAGE_DEFAULT;
        composite_texture_desc.BindFlags = D3D11_BIND_UNORDERED_ACCESS;
        ComPtr<ID3D11Texture2D> composite_texture;
        ComPtr<ID3D11UnorderedAccessView> composite_uav;
        throw_if_failed(bridge_device->CreateTexture2D(
            &composite_texture_desc, nullptr, &composite_texture),
            "CreateTexture2D(FP16 composite output)");
        throw_if_failed(bridge_device->CreateUnorderedAccessView(
            composite_texture.Get(), nullptr, &composite_uav),
            "CreateUnorderedAccessView(FP16 composite output)");

        ComPtr<IDMLDevice> dml_device;
        throw_if_failed(DMLCreateDevice(
            d3d12_device.Get(), DML_CREATE_DEVICE_FLAG_NONE, IID_PPV_ARGS(&dml_device)), "DMLCreateDevice(product)");
        Ort::Env ort_environment(ORT_LOGGING_LEVEL_WARNING, "OhMyCineFrameInterpolationLive");
        Ort::SessionOptions ort_options;
        ort_options.DisableMemPattern();
        ort_options.SetExecutionMode(ExecutionMode::ORT_SEQUENTIAL);
        Ort::ThrowOnError(OrtSessionOptionsAppendExecutionProviderEx_DML(
            ort_options, dml_device.Get(), queue.Get()));
        Ort::Session ort_session(ort_environment, state->model_path.c_str(), ort_options);
        if (!expected_names(ort_session))
            throw std::runtime_error("live flow/mask ONNX contract mismatch");
        const OrtDmlApi* api = dml_api();
        const ProxyExtent proxy = proxy_extent(state->proxy_size, item_size);
        const size_t proxy_pixels = static_cast<size_t>(proxy.width) * proxy.height;
        const size_t image_elements = 3u * proxy_pixels;
        const size_t timestep_elements = proxy_pixels;
        const size_t flow_elements = 4u * proxy_pixels;
        const size_t mask_elements = proxy_pixels;
        GpuTensor earlier_gpu = bind_gpu_tensor(api, d3d12_device.Get(), image_elements * sizeof(float), D3D12_RESOURCE_STATE_UNORDERED_ACCESS);
        GpuTensor later_gpu = bind_gpu_tensor(api, d3d12_device.Get(), image_elements * sizeof(float), D3D12_RESOURCE_STATE_UNORDERED_ACCESS);
        GpuTensor timestep_gpu = bind_gpu_tensor(api, d3d12_device.Get(), timestep_elements * sizeof(float), D3D12_RESOURCE_STATE_UNORDERED_ACCESS);
        GpuTensor flow_gpu = bind_gpu_tensor(api, d3d12_device.Get(), flow_elements * sizeof(float), D3D12_RESOURCE_STATE_UNORDERED_ACCESS);
        GpuTensor mask_gpu = bind_gpu_tensor(api, d3d12_device.Get(), mask_elements * sizeof(float), D3D12_RESOURCE_STATE_UNORDERED_ACCESS);
        const Ort::MemoryInfo dml_memory("DML", OrtDeviceAllocator, 0, OrtMemTypeDefault);
        const std::array<int64_t, 4> image_shape{1, 3, proxy.height, proxy.width};
        const std::array<int64_t, 4> timestep_shape{1, 1, proxy.height, proxy.width};
        const std::array<int64_t, 4> flow_shape{1, 4, proxy.height, proxy.width};
        const std::array<int64_t, 4> mask_shape{1, 1, proxy.height, proxy.width};
        std::array<Ort::Value, 3> model_inputs{
            Ort::Value::CreateTensor(dml_memory, earlier_gpu.allocation, image_elements * sizeof(float), image_shape.data(), image_shape.size(), ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT),
            Ort::Value::CreateTensor(dml_memory, later_gpu.allocation, image_elements * sizeof(float), image_shape.data(), image_shape.size(), ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT),
            Ort::Value::CreateTensor(dml_memory, timestep_gpu.allocation, timestep_elements * sizeof(float), timestep_shape.data(), timestep_shape.size(), ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT),
        };
        std::array<Ort::Value, 2> model_outputs{
            Ort::Value::CreateTensor(dml_memory, flow_gpu.allocation, flow_elements * sizeof(float), flow_shape.data(), flow_shape.size(), ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT),
            Ort::Value::CreateTensor(dml_memory, mask_gpu.allocation, mask_elements * sizeof(float), mask_shape.data(), mask_shape.size(), ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT),
        };
        auto earlier_views = wrap_tensor(on12.Get(), bridge_device.Get(), earlier_gpu.resource.Get(), static_cast<UINT>(image_elements), true, false);
        auto later_views = wrap_tensor(on12.Get(), bridge_device.Get(), later_gpu.resource.Get(), static_cast<UINT>(image_elements), true, false);
        auto timestep_views = wrap_tensor(on12.Get(), bridge_device.Get(), timestep_gpu.resource.Get(), static_cast<UINT>(timestep_elements), true, false);
        auto flow_views = wrap_tensor(on12.Get(), bridge_device.Get(), flow_gpu.resource.Get(), static_cast<UINT>(flow_elements), false, true);
        auto mask_views = wrap_tensor(on12.Get(), bridge_device.Get(), mask_gpu.resource.Get(), static_cast<UINT>(mask_elements), false, true);

        const auto proxy_bytecode = compile_compute_shader(kProxyShader, "OhMyCineProxy");
        const auto timestep_bytecode = compile_compute_shader(kTimestepShader, "OhMyCineTimestep");
        const auto composite_bytecode = compile_compute_shader(kCompositeShader, "OhMyCineComposite");
        ComPtr<ID3D11ComputeShader> proxy_shader;
        ComPtr<ID3D11ComputeShader> timestep_shader;
        ComPtr<ID3D11ComputeShader> composite_shader;
        throw_if_failed(bridge_device->CreateComputeShader(proxy_bytecode->GetBufferPointer(), proxy_bytecode->GetBufferSize(), nullptr, &proxy_shader), "CreateComputeShader(proxy)");
        throw_if_failed(bridge_device->CreateComputeShader(timestep_bytecode->GetBufferPointer(), timestep_bytecode->GetBufferSize(), nullptr, &timestep_shader), "CreateComputeShader(timestep)");
        throw_if_failed(bridge_device->CreateComputeShader(composite_bytecode->GetBufferPointer(), composite_bytecode->GetBufferSize(), nullptr, &composite_shader), "CreateComputeShader(composite)");
        struct ProxyConstants { UINT source_size[2]; UINT proxy_size[2]; float reference_white; float source_peak; float padding[2]; };
        struct TimestepConstants { float value; UINT element_count; float padding[2]; };
        struct CompositeConstants { UINT output_size[2]; UINT proxy_size[2]; float timestep; float confidence; UINT scene_cut; float flow_timestep; };
        auto proxy_constants = create_constant_buffer(bridge_device.Get(), sizeof(ProxyConstants));
        auto timestep_constants = create_constant_buffer(bridge_device.Get(), sizeof(TimestepConstants));
        auto composite_constants = create_constant_buffer(bridge_device.Get(), sizeof(CompositeConstants));
        D3D11_SAMPLER_DESC sampler_desc{};
        sampler_desc.Filter = D3D11_FILTER_MIN_MAG_MIP_LINEAR;
        sampler_desc.AddressU = sampler_desc.AddressV = sampler_desc.AddressW = D3D11_TEXTURE_ADDRESS_CLAMP;
        sampler_desc.MaxLOD = D3D11_FLOAT32_MAX;
        ComPtr<ID3D11SamplerState> sampler;
        throw_if_failed(bridge_device->CreateSamplerState(&sampler_desc, &sampler), "CreateSamplerState(composite)");

        capture.StartCapture();
        ComPtr<ID3D11Texture2D> previous;
        int64_t previous_timestamp = 0;
        int64_t previous_source_index = -1;
        int64_t next_output_tick = -1;
        UINT consecutive_generated_pairs = 0;
        UINT consecutive_missed_pairs = 0;
        LARGE_INTEGER qpc_frequency{};
        QueryPerformanceFrequency(&qpc_frequency);
        while (!state->stop_requested.load(std::memory_order_acquire)) {
            MSG message{};
            while (PeekMessageW(&message, nullptr, 0, 0, PM_REMOVE)) {
                TranslateMessage(&message);
                DispatchMessageW(&message);
            }
            auto frame = pool.TryGetNextFrame();
            if (!frame) {
                const auto last_present =
                    state->last_successful_present_qpc.load(std::memory_order_acquire);
                if (state->generated_first_present.load(std::memory_order_acquire)
                    && last_present > 0) {
                    LARGE_INTEGER now{};
                    QueryPerformanceCounter(&now);
                    const double stalled_ms =
                        static_cast<double>(now.QuadPart - last_present) * 1000.0
                        / static_cast<double>(qpc_frequency.QuadPart);
                    if (stalled_ms > 350.0) {
                        consecutive_generated_pairs = 0;
                        hide_generated_output(
                            state,
                            "Generated presentation stalled for over 350 ms; the overlay was hidden and mpv source playback was restored.",
                            true);
                    }
                }
                std::this_thread::sleep_for(std::chrono::milliseconds(2));
                continue;
            }
            const auto size = frame.ContentSize();
            if (size.Width != item_size.Width || size.Height != item_size.Height) {
                state->set_reason("mpv window size changed; source playback remains visible while the frame-generation session rebuilds.");
                break;
            }
            auto surface = frame.Surface();
            auto access = surface.as<::Windows::Graphics::DirectX::Direct3D11::IDirect3DDxgiInterfaceAccess>();
            ComPtr<ID3D11Texture2D> texture;
            check_hresult(access->GetInterface(IID_PPV_ARGS(&texture)));
            D3D11_TEXTURE2D_DESC texture_desc{};
            texture->GetDesc(&texture_desc);
            ComPtr<ID3D11Device> capture_device;
            texture->GetDevice(&capture_device);
            if (texture_desc.Format != DXGI_FORMAT_R16G16B16A16_FLOAT
                || adapter_luid(capture_device.Get()) != adapter_luid(bridge_device.Get()))
                throw std::runtime_error("live WGC frame is not same-adapter R16G16B16A16_FLOAT");
            const int64_t ticks = frame.SystemRelativeTime().count();
            RECT source_rect{};
            if (GetWindowRect(state->source_hwnd, &source_rect)) {
                HWND insertion_point = GetWindow(state->source_hwnd, GW_HWNDPREV);
                if (insertion_point == output_hwnd)
                    insertion_point = GetWindow(output_hwnd, GW_HWNDPREV);
                const BOOL positioned = SetWindowPos(
                    output_hwnd,
                    insertion_point,
                    source_rect.left,
                    source_rect.top,
                    source_rect.right - source_rect.left,
                    source_rect.bottom - source_rect.top,
                    SWP_NOACTIVATE | SWP_NOOWNERZORDER);
                const bool output_visible =
                    state->generated_first_present.load(std::memory_order_acquire);
                const bool safely_above_source =
                    GetWindow(state->source_hwnd, GW_HWNDPREV) == output_hwnd;
                if (output_visible
                    && (!positioned || !safely_above_source || !IsWindowVisible(output_hwnd))) {
                    consecutive_generated_pairs = 0;
                    hide_generated_output(
                        state,
                        "Generated output lost its safe z-order; the overlay was hidden and mpv source playback was restored.",
                        true);
                }
            }
            texture_desc.BindFlags = D3D11_BIND_SHADER_RESOURCE;
            texture_desc.MiscFlags = 0;
            ComPtr<ID3D11Texture2D> current;
            throw_if_failed(bridge_device->CreateTexture2D(&texture_desc, nullptr, &current), "CreateTexture2D(frame-ring)");
            bridge_context->CopyResource(current.Get(), texture.Get());
            bridge_context->Flush();
            if (!state->timing_reliable.load(std::memory_order_acquire)
                || state->paused.load(std::memory_order_acquire)) {
                state->set_reason("Waiting for reliable CFR mpv media timing; VFR/unknown timing remains bypassed.");
                continue;
            }
            const auto anchor_qpc = state->timing_qpc.load(std::memory_order_acquire);
            const auto fps = state->source_fps.load(std::memory_order_acquire);
            const auto anchor_pts = state->media_pts_seconds.load(std::memory_order_acquire);
            // GraphicsCaptureFrame::SystemRelativeTime is a 100-nanosecond timestamp on the
            // system-relative/QPC timeline. Map the captured texture's time to mpv's latest
            // media anchor instead of using the later queue-consumption time; otherwise a slow
            // inference pass relabels queued old textures as current video frames.
            constexpr double kTimeSpanTicksPerSecond = 10'000'000.0;
            const auto capture_seconds = static_cast<double>(ticks) / kTimeSpanTicksPerSecond;
            const auto anchor_seconds = static_cast<double>(anchor_qpc)
                / static_cast<double>(qpc_frequency.QuadPart);
            const auto elapsed = capture_seconds - anchor_seconds;
            const auto media_pts = anchor_pts + elapsed;
            const auto source_index = static_cast<int64_t>(std::floor(media_pts * fps + 0.5));
            if (source_index == previous_source_index)
                continue;
            if (previous && ticks > previous_timestamp
                && source_index == previous_source_index + 1) {
                state->captured_pair.store(true, std::memory_order_release);
                ComPtr<ID3D11ShaderResourceView> earlier_srv;
                ComPtr<ID3D11ShaderResourceView> later_srv;
                throw_if_failed(bridge_device->CreateShaderResourceView(previous.Get(), nullptr, &earlier_srv), "CreateShaderResourceView(earlier)");
                throw_if_failed(bridge_device->CreateShaderResourceView(current.Get(), nullptr, &later_srv), "CreateShaderResourceView(later)");
                const ProxyConstants proxy_params{
                    {texture_desc.Width, texture_desc.Height},
                    {proxy.width, proxy.height},
                    203.0f,
                    state->hdr_input ? 1000.0f : 203.0f,
                    {0.0f, 0.0f}};
                bridge_context->UpdateSubresource(proxy_constants.Get(), 0, nullptr, &proxy_params, 0, 0);
                bridge_context->CSSetShader(proxy_shader.Get(), nullptr, 0);
                bridge_context->CSSetConstantBuffers(0, 1, proxy_constants.GetAddressOf());
                const std::array<std::pair<ID3D11ShaderResourceView*, WrappedTensorViews*>, 2> proxy_passes{{
                    {earlier_srv.Get(), &earlier_views}, {later_srv.Get(), &later_views}}};
                for (const auto& [source_srv, destination] : proxy_passes) {
                    ID3D11Resource* acquired[] = {destination->resource.Get()};
                    on12->AcquireWrappedResources(acquired, 1);
                    bridge_context->CSSetShaderResources(0, 1, &source_srv);
                    auto* destination_uav = destination->uav.Get();
                    bridge_context->CSSetUnorderedAccessViews(0, 1, &destination_uav, nullptr);
                    bridge_context->Dispatch(
                        (proxy.width + 7) / 8, (proxy.height + 7) / 8, 1);
                    ID3D11ShaderResourceView* null_srv = nullptr;
                    ID3D11UnorderedAccessView* null_uav = nullptr;
                    bridge_context->CSSetShaderResources(0, 1, &null_srv);
                    bridge_context->CSSetUnorderedAccessViews(0, 1, &null_uav, nullptr);
                    on12->ReleaseWrappedResources(acquired, 1);
                }
                bridge_context->Flush();

                const double earlier_pts = static_cast<double>(previous_source_index) / fps;
                const double later_pts = static_cast<double>(source_index) / fps;
                if (next_output_tick < 0 ||
                    static_cast<double>(next_output_tick) / state->target_fps < earlier_pts - 1e-6) {
                    next_output_tick = static_cast<int64_t>(
                        std::ceil(earlier_pts * state->target_fps - 1e-7));
                }
                const char* input_names[] = {"earlier_proxy", "later_proxy", "timestep"};
                const char* output_names[] = {"flow_pixels", "blend_mask"};
                LARGE_INTEGER pair_started{};
                QueryPerformanceCounter(&pair_started);
                bool abort_session = false;
                bool pair_had_drop = false;
                UINT generated_this_pair = 0;
                bool flow_ready = false;
                const int64_t target_tick_qpc = std::max<int64_t>(
                    1, qpc_frequency.QuadPart / static_cast<int64_t>(state->target_fps));
                while (static_cast<double>(next_output_tick) / state->target_fps
                       < later_pts - 1e-7) {
                    if (state->stop_requested.load(std::memory_order_acquire)) {
                        abort_session = true;
                        break;
                    }
                    const double output_pts =
                        static_cast<double>(next_output_tick) / state->target_fps;
                    const float timestep = static_cast<float>(std::clamp(
                        (output_pts - earlier_pts) / (later_pts - earlier_pts), 0.0, 1.0));
                    const bool interpolated = timestep > 0.0001f && timestep < 0.9999f;
                    const double offset_seconds = output_pts - earlier_pts;
                    const int64_t deadline = pair_started.QuadPart + static_cast<int64_t>(
                        offset_seconds * static_cast<double>(qpc_frequency.QuadPart));
                    LARGE_INTEGER before_inference{};
                    QueryPerformanceCounter(&before_inference);
                    if (interpolated
                        && before_inference.QuadPart > deadline + target_tick_qpc) {
                        state->dropped_output_ticks.fetch_add(1, std::memory_order_relaxed);
                        pair_had_drop = true;
                        ++next_output_tick;
                        continue;
                    }
                    if (interpolated && !flow_ready) {
                        const TimestepConstants timestep_params{
                            0.5f, static_cast<UINT>(timestep_elements), {0.0f, 0.0f}};
                        bridge_context->UpdateSubresource(
                            timestep_constants.Get(), 0, nullptr, &timestep_params, 0, 0);
                        ID3D11Resource* timestep_acquired[] = {timestep_views.resource.Get()};
                        on12->AcquireWrappedResources(timestep_acquired, 1);
                        bridge_context->CSSetShader(timestep_shader.Get(), nullptr, 0);
                        bridge_context->CSSetConstantBuffers(
                            0, 1, timestep_constants.GetAddressOf());
                        auto* timestep_uav = timestep_views.uav.Get();
                        bridge_context->CSSetUnorderedAccessViews(
                            0, 1, &timestep_uav, nullptr);
                        bridge_context->Dispatch(
                            (static_cast<UINT>(timestep_elements) + 63u) / 64u, 1, 1);
                        ID3D11UnorderedAccessView* null_timestep_uav = nullptr;
                        bridge_context->CSSetUnorderedAccessViews(
                            0, 1, &null_timestep_uav, nullptr);
                        on12->ReleaseWrappedResources(timestep_acquired, 1);
                        bridge_context->Flush();

                        LARGE_INTEGER inference_started{};
                        QueryPerformanceCounter(&inference_started);
                        state->inference_started_qpc.store(
                            inference_started.QuadPart, std::memory_order_release);
                        state->inference_in_flight.store(true, std::memory_order_release);
                        try {
                            ort_session.Run(
                                state->run_options, input_names, model_inputs.data(),
                                model_inputs.size(), output_names, model_outputs.data(),
                                model_outputs.size());
                        }
                        catch (...) {
                            state->inference_in_flight.store(false, std::memory_order_release);
                            throw;
                        }
                        state->inference_in_flight.store(false, std::memory_order_release);
                        LARGE_INTEGER inference_finished{};
                        QueryPerformanceCounter(&inference_finished);
                        const auto inference_micros = static_cast<uint64_t>(std::max<int64_t>(
                            0,
                            (inference_finished.QuadPart - inference_started.QuadPart) * 1000000
                                / qpc_frequency.QuadPart));
                        state->latest_inference_micros.store(
                            inference_micros, std::memory_order_release);
                        state->inference_sample_count.fetch_add(1, std::memory_order_relaxed);
                        flow_ready = true;
                        if (inference_finished.QuadPart > deadline + target_tick_qpc) {
                            state->dropped_output_ticks.fetch_add(1, std::memory_order_relaxed);
                            pair_had_drop = true;
                            ++next_output_tick;
                            continue;
                        }
                    }

                    const UINT index = swapchain->GetCurrentBackBufferIndex();
                    ID3D11Resource* acquired[] = {
                        flow_views.resource.Get(), mask_views.resource.Get(),
                        wrapped_buffers[index].Get()};
                    on12->AcquireWrappedResources(acquired, 3);
                    const CompositeConstants composite_params{
                        {texture_desc.Width, texture_desc.Height},
                        {proxy.width, proxy.height},
                        timestep,
                        0.20f,
                        timestep <= 0.0001f ? 1u : 0u,
                        0.5f};
                    bridge_context->UpdateSubresource(
                        composite_constants.Get(), 0, nullptr, &composite_params, 0, 0);
                    ID3D11ShaderResourceView* composite_srvs[] = {
                        earlier_srv.Get(), later_srv.Get(), flow_views.srv.Get(),
                        mask_views.srv.Get()};
                    bridge_context->CSSetShader(composite_shader.Get(), nullptr, 0);
                    bridge_context->CSSetShaderResources(0, 4, composite_srvs);
                    bridge_context->CSSetSamplers(0, 1, sampler.GetAddressOf());
                    bridge_context->CSSetConstantBuffers(
                        0, 1, composite_constants.GetAddressOf());
                    auto* output_uav = composite_uav.Get();
                    bridge_context->CSSetUnorderedAccessViews(
                        0, 1, &output_uav, nullptr);
                    bridge_context->Dispatch(
                        (texture_desc.Width + 7) / 8, (texture_desc.Height + 7) / 8, 1);
                    ID3D11ShaderResourceView* null_srvs[4]{};
                    ID3D11UnorderedAccessView* null_uav = nullptr;
                    bridge_context->CSSetShaderResources(0, 4, null_srvs);
                    bridge_context->CSSetUnorderedAccessViews(0, 1, &null_uav, nullptr);
                    bridge_context->CopyResource(
                        wrapped_buffers[index].Get(), composite_texture.Get());
                    on12->ReleaseWrappedResources(acquired, 3);
                    bridge_context->Flush();

                    for (;;) {
                        LARGE_INTEGER now{};
                        QueryPerformanceCounter(&now);
                        if (now.QuadPart >= deadline ||
                            state->stop_requested.load(std::memory_order_acquire)) {
                            break;
                        }
                        const double remaining_ms =
                            static_cast<double>(deadline - now.QuadPart) * 1000.0 /
                            static_cast<double>(qpc_frequency.QuadPart);
                        if (remaining_ms > 2.0)
                            std::this_thread::sleep_for(std::chrono::milliseconds(1));
                        else
                            std::this_thread::yield();
                    }
                    const HRESULT present = swapchain->Present(0, DXGI_PRESENT_DO_NOT_WAIT);
                    if (present == DXGI_ERROR_DEVICE_REMOVED ||
                        present == DXGI_ERROR_DEVICE_RESET) {
                        state->device_lost.store(true, std::memory_order_release);
                        state->set_reason("GPU device lost; source mpv playback remains visible and the generated output is being destroyed.");
                        abort_session = true;
                        break;
                    }
                    if (present != DXGI_ERROR_WAS_STILL_DRAWING)
                        throw_if_failed(present, "scheduled FP16 Present");
                    if (present == DXGI_ERROR_WAS_STILL_DRAWING) {
                        state->dropped_output_ticks.fetch_add(1, std::memory_order_relaxed);
                        pair_had_drop = true;
                    } else {
                        LARGE_INTEGER presented_at{};
                        QueryPerformanceCounter(&presented_at);
                        state->successful_present_count.fetch_add(1, std::memory_order_relaxed);
                        int64_t no_first_present = 0;
                        state->first_successful_present_qpc.compare_exchange_strong(
                            no_first_present,
                            presented_at.QuadPart,
                            std::memory_order_release,
                            std::memory_order_relaxed);
                        state->last_successful_present_qpc.store(
                            presented_at.QuadPart, std::memory_order_release);
                        if (interpolated) {
                            ++generated_this_pair;
                            state->generated_present_count.fetch_add(1, std::memory_order_relaxed);
                            state->last_generated_present_qpc.store(
                                presented_at.QuadPart, std::memory_order_release);
                        }
                    }
                    ++next_output_tick;
                }
                if (abort_session)
                    break;
                if (generated_this_pair > 0 && !pair_had_drop) {
                    ++consecutive_generated_pairs;
                    consecutive_missed_pairs = 0;
                    if (consecutive_generated_pairs >= 2) {
                        state->hidden_first_present.store(true, std::memory_order_release);
                        state->cadence_stalled.store(false, std::memory_order_release);
                        state->set_reason("Two consecutive source pairs completed generated FP16 presents without expired ticks; waiting for the audio/reveal gate.");
                    }
                } else {
                    consecutive_generated_pairs = 0;
                    ++consecutive_missed_pairs;
                    state->hidden_first_present.store(false, std::memory_order_release);
                    if (consecutive_missed_pairs >= 2) {
                        hide_generated_output(
                            state,
                            "The requested cadence could not be sustained for two source pairs; the overlay was hidden and mpv source playback was restored.",
                            true);
                    }
                }
            } else if (previous && source_index != previous_source_index + 1) {
                next_output_tick = -1;
                consecutive_generated_pairs = 0;
                ++consecutive_missed_pairs;
                state->dropped_output_ticks.fetch_add(1, std::memory_order_relaxed);
                if (consecutive_missed_pairs >= 2
                    || state->generated_first_present.load(std::memory_order_acquire)) {
                    hide_generated_output(
                        state,
                        "A source-frame discontinuity was detected; the overlay was hidden immediately and mpv source playback was restored.",
                        true);
                } else {
                    state->hidden_first_present.store(false, std::memory_order_release);
                    state->set_reason("A source-frame discontinuity was detected; the pair was discarded while mpv source playback remained visible.");
                }
            }
            previous = current;
            previous_timestamp = ticks;
            previous_source_index = source_index;
        }
        capture.Close();
        pool.Close();
        for (auto& output : model_outputs)
            output = Ort::Value{nullptr};
        for (auto& input : model_inputs)
            input = Ort::Value{nullptr};
        free_gpu_tensor(api, mask_gpu);
        free_gpu_tensor(api, flow_gpu);
        free_gpu_tensor(api, timestep_gpu);
        free_gpu_tensor(api, later_gpu);
        free_gpu_tensor(api, earlier_gpu);
    }
    catch (const hresult_error& error) {
        state->set_reason(to_string(error.message()));
    }
    catch (const std::exception& error) {
        state->set_reason(error.what());
    }
    catch (...) {
        state->set_reason("unknown Windows frame-generation session failure");
    }
    const HWND output = state->output_hwnd.exchange(nullptr, std::memory_order_acq_rel);
    if (output != nullptr) {
        ShowWindow(output, SW_HIDE);
        DestroyWindow(output);
    }
    state->finished.store(true, std::memory_order_release);
}

}  // namespace

extern "C" int ohmycine_probe_directml_flow_mask(
    const wchar_t* model_path,
    char* reason,
    size_t reason_capacity) noexcept {
    if (model_path == nullptr) {
        write_reason(reason, reason_capacity, "model path is null");
        return 0;
    }
    try {
        ComPtr<ID3D12Device> device;
        throw_if_failed(D3D12CreateDevice(
            nullptr, D3D_FEATURE_LEVEL_11_0, IID_PPV_ARGS(&device)), "D3D12CreateDevice");
        const D3D12_COMMAND_QUEUE_DESC queue_desc{
            D3D12_COMMAND_LIST_TYPE_DIRECT,
            0,
            D3D12_COMMAND_QUEUE_FLAG_NONE,
            0,
        };
        ComPtr<ID3D12CommandQueue> queue;
        throw_if_failed(device->CreateCommandQueue(&queue_desc, IID_PPV_ARGS(&queue)), "ID3D12Device::CreateCommandQueue");
        ComPtr<IDMLDevice> directml_device;
        throw_if_failed(DMLCreateDevice(
            device.Get(), DML_CREATE_DEVICE_FLAG_NONE, IID_PPV_ARGS(&directml_device)), "DMLCreateDevice");

        Ort::Env environment(ORT_LOGGING_LEVEL_WARNING, "OhMyCineFrameInterpolationProbe");
        Ort::SessionOptions options;
        options.DisableMemPattern();
        options.SetExecutionMode(ExecutionMode::ORT_SEQUENTIAL);
        Ort::ThrowOnError(OrtSessionOptionsAppendExecutionProviderEx_DML(
            options, directml_device.Get(), queue.Get()));
        Ort::Session session(environment, model_path, options);
        if (!expected_names(session)) {
            write_reason(reason, reason_capacity, "flow/mask ONNX input-output contract mismatch");
            return 0;
        }

        // The performance preset uses the 32x32 dynamic shape. The original
        // 64x64 shape is exercised by the product path and prior releases.
        constexpr int64_t kSize = 32;
        const std::array<int64_t, 4> image_shape{1, 3, kSize, kSize};
        const std::array<int64_t, 4> timestep_shape{1, 1, kSize, kSize};
        std::vector<float> earlier(3 * kSize * kSize, 0.2F);
        std::vector<float> later(3 * kSize * kSize, 0.8F);
        std::vector<float> timestep(kSize * kSize, 0.5F);

        const OrtDmlApi* api = dml_api();
        const size_t image_bytes = earlier.size() * sizeof(float);
        const size_t timestep_bytes = timestep.size() * sizeof(float);
        const size_t flow_bytes = 4 * kSize * kSize * sizeof(float);
        const size_t mask_bytes = kSize * kSize * sizeof(float);
        GpuTensor earlier_gpu = bind_gpu_tensor(
            api, device.Get(), image_bytes, D3D12_RESOURCE_STATE_COPY_DEST);
        GpuTensor later_gpu = bind_gpu_tensor(
            api, device.Get(), image_bytes, D3D12_RESOURCE_STATE_COPY_DEST);
        GpuTensor timestep_gpu = bind_gpu_tensor(
            api, device.Get(), timestep_bytes, D3D12_RESOURCE_STATE_COPY_DEST);
        GpuTensor flow_gpu = bind_gpu_tensor(
            api, device.Get(), flow_bytes, D3D12_RESOURCE_STATE_UNORDERED_ACCESS);
        GpuTensor mask_gpu = bind_gpu_tensor(
            api, device.Get(), mask_bytes, D3D12_RESOURCE_STATE_UNORDERED_ACCESS);
        upload_inputs(device.Get(), queue.Get(), {
            {earlier_gpu.resource, &earlier},
            {later_gpu.resource, &later},
            {timestep_gpu.resource, &timestep},
        });

        const Ort::MemoryInfo memory(
            "DML", OrtDeviceAllocator, 0, OrtMemTypeDefault);
        std::array<Ort::Value, 3> inputs{
            Ort::Value::CreateTensor(
                memory, earlier_gpu.allocation, image_bytes, image_shape.data(), image_shape.size(), ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT),
            Ort::Value::CreateTensor(
                memory, later_gpu.allocation, image_bytes, image_shape.data(), image_shape.size(), ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT),
            Ort::Value::CreateTensor(
                memory, timestep_gpu.allocation, timestep_bytes, timestep_shape.data(), timestep_shape.size(), ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT),
        };
        const std::array<int64_t, 4> flow_shape{1, 4, kSize, kSize};
        const std::array<int64_t, 4> mask_shape{1, 1, kSize, kSize};
        std::array<Ort::Value, 2> outputs{
            Ort::Value::CreateTensor(
                memory, flow_gpu.allocation, flow_bytes, flow_shape.data(), flow_shape.size(), ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT),
            Ort::Value::CreateTensor(
                memory, mask_gpu.allocation, mask_bytes, mask_shape.data(), mask_shape.size(), ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT),
        };
        const char* input_names[] = {"earlier_proxy", "later_proxy", "timestep"};
        const char* output_names[] = {"flow_pixels", "blend_mask"};
        session.Run(
            Ort::RunOptions{nullptr},
            input_names,
            inputs.data(),
            inputs.size(),
            output_names,
            outputs.data(),
            outputs.size());
        if (!outputs[0].IsTensor() || !outputs[1].IsTensor()) {
            write_reason(reason, reason_capacity, "DirectML inference did not return two tensors");
            return 0;
        }
        const auto actual_flow_shape = outputs[0].GetTensorTypeAndShapeInfo().GetShape();
        const auto actual_mask_shape = outputs[1].GetTensorTypeAndShapeInfo().GetShape();
        if (actual_flow_shape != std::vector<int64_t>({1, 4, kSize, kSize}) ||
            actual_mask_shape != std::vector<int64_t>({1, 1, kSize, kSize})) {
            write_reason(reason, reason_capacity, "DirectML flow/mask output shape mismatch");
            return 0;
        }

        // OrtValue objects must release their DML allocations before the backing D3D12
        // resources are freed. The probe deliberately never asks ORT for a CPU output: passing
        // these preallocated DML tensors proves that model input and flow/mask output stay GPU
        // resident. Pixel validation belongs to the subsequent FP16 composite/readback probe.
        for (auto& output : outputs)
            output = Ort::Value{nullptr};
        for (auto& input : inputs)
            input = Ort::Value{nullptr};
        free_gpu_tensor(api, mask_gpu);
        free_gpu_tensor(api, flow_gpu);
        free_gpu_tensor(api, timestep_gpu);
        free_gpu_tensor(api, later_gpu);
        free_gpu_tensor(api, earlier_gpu);
        write_reason(reason, reason_capacity, "ok");
        return 1;
    }
    catch (const Ort::Exception& error) {
        write_reason(reason, reason_capacity, error.what());
        return 0;
    }
    catch (const std::exception& error) {
        write_reason(reason, reason_capacity, error.what());
        return 0;
    }
    catch (...) {
        write_reason(reason, reason_capacity, "unknown DirectML probe error");
        return 0;
    }
}

extern "C" void* ohmycine_windows_framegen_start(
    intptr_t source_hwnd,
    const wchar_t* model_path,
    unsigned int target_fps,
    int hdr_input,
    unsigned int proxy_size,
    char* reason,
    size_t reason_capacity) noexcept {
    try {
        const HWND source = reinterpret_cast<HWND>(source_hwnd);
        if (!IsWindow(source) || model_path == nullptr) {
            write_reason(reason, reason_capacity, "invalid mpv source HWND");
            return nullptr;
        }
        if (target_fps < 24 || target_fps > 240) {
            write_reason(reason, reason_capacity, "invalid frame-generation target fps");
            return nullptr;
        }
        if (proxy_size != 32 && proxy_size != 48 && proxy_size != 64) {
            write_reason(reason, reason_capacity, "invalid frame-generation proxy size");
            return nullptr;
        }
        auto* session = new WindowsFrameGenerationSession(
            source, model_path, target_fps, hdr_input == 1, proxy_size);
        session->worker = std::thread(run_product_capture_session, session);
        session->watchdog = std::thread(run_inference_watchdog, session);
        write_reason(reason, reason_capacity, "started-hidden");
        return session;
    }
    catch (const std::exception& error) {
        write_reason(reason, reason_capacity, error.what());
        return nullptr;
    }
    catch (...) {
        write_reason(reason, reason_capacity, "unknown Windows session start error");
        return nullptr;
    }
}

extern "C" int ohmycine_windows_framegen_poll(
    void* opaque,
    int* captured_pair,
    int* hidden_first_present,
    int* generated_first_present,
    int* cadence_stalled,
    int* device_lost,
    int* finished,
    uint64_t* successful_present_count,
    uint64_t* generated_present_count,
    uint64_t* dropped_output_ticks,
    uint64_t* inference_sample_count,
    uint64_t* latest_inference_micros,
    double* measured_output_fps,
    char* reason,
    size_t reason_capacity) noexcept {
    if (opaque == nullptr) {
        write_reason(reason, reason_capacity, "Windows frame-generation session is null");
        return 0;
    }
    auto* session = static_cast<WindowsFrameGenerationSession*>(opaque);
    if (captured_pair != nullptr)
        *captured_pair = session->captured_pair.load(std::memory_order_acquire) ? 1 : 0;
    if (hidden_first_present != nullptr)
        *hidden_first_present = session->hidden_first_present.load(std::memory_order_acquire) ? 1 : 0;
    if (generated_first_present != nullptr)
        *generated_first_present = session->generated_first_present.load(std::memory_order_acquire) ? 1 : 0;
    if (cadence_stalled != nullptr)
        *cadence_stalled = session->cadence_stalled.load(std::memory_order_acquire) ? 1 : 0;
    if (device_lost != nullptr)
        *device_lost = session->device_lost.load(std::memory_order_acquire) ? 1 : 0;
    if (finished != nullptr)
        *finished = session->finished.load(std::memory_order_acquire) ? 1 : 0;
    if (successful_present_count != nullptr)
        *successful_present_count = session->successful_present_count.load(std::memory_order_acquire);
    if (generated_present_count != nullptr)
        *generated_present_count = session->generated_present_count.load(std::memory_order_acquire);
    if (dropped_output_ticks != nullptr)
        *dropped_output_ticks = session->dropped_output_ticks.load(std::memory_order_acquire);
    if (inference_sample_count != nullptr)
        *inference_sample_count = session->inference_sample_count.load(std::memory_order_acquire);
    if (latest_inference_micros != nullptr)
        *latest_inference_micros = session->latest_inference_micros.load(std::memory_order_acquire);
    if (measured_output_fps != nullptr) {
        *measured_output_fps = 0.0;
        const auto count = session->successful_present_count.load(std::memory_order_acquire);
        const auto first = session->first_successful_present_qpc.load(std::memory_order_acquire);
        const auto last = session->last_successful_present_qpc.load(std::memory_order_acquire);
        LARGE_INTEGER frequency{};
        if (count > 1 && first > 0 && last > first && QueryPerformanceFrequency(&frequency)) {
            *measured_output_fps = static_cast<double>(count - 1)
                * static_cast<double>(frequency.QuadPart)
                / static_cast<double>(last - first);
        }
    }
    const auto message = session->get_reason();
    write_reason(reason, reason_capacity, message.c_str());
    return 1;
}

extern "C" void ohmycine_windows_framegen_update_timing(
    void* opaque,
    double media_pts_seconds,
    double source_fps,
    int timing_reliable,
    int paused) noexcept {
    if (opaque == nullptr)
        return;
    auto* session = static_cast<WindowsFrameGenerationSession*>(opaque);
    LARGE_INTEGER counter{};
    QueryPerformanceCounter(&counter);
    session->media_pts_seconds.store(media_pts_seconds, std::memory_order_release);
    session->source_fps.store(source_fps, std::memory_order_release);
    session->paused.store(paused == 1, std::memory_order_release);
    session->timing_qpc.store(counter.QuadPart, std::memory_order_release);
    session->timing_reliable.store(
        timing_reliable == 1 && std::isfinite(media_pts_seconds)
            && std::isfinite(source_fps) && source_fps > 0.0,
        std::memory_order_release);
}

// Final reveal hook for the Rust media-timing/audio/subtitle gate. A genuinely interpolated frame
// must already have completed a hidden Present; only after Win32 confirms the output HWND is
// visible is generated_first_present published.
extern "C" int ohmycine_windows_framegen_reveal_after_safe_gates(
    void* opaque,
    char* reason,
    size_t reason_capacity) noexcept {
    if (opaque == nullptr) {
        write_reason(reason, reason_capacity, "Windows frame-generation session is null");
        return 0;
    }
    auto* session = static_cast<WindowsFrameGenerationSession*>(opaque);
    const HWND output = session->output_hwnd.load(std::memory_order_acquire);
    if (!session->hidden_first_present.load(std::memory_order_acquire)
        || session->device_lost.load(std::memory_order_acquire)
        || output == nullptr) {
        write_reason(reason, reason_capacity, "no safely presented generated frame is ready to reveal");
        return 0;
    }
    RECT rect{};
    if (!GetWindowRect(session->source_hwnd, &rect)) {
        write_reason(reason, reason_capacity, "source HWND geometry is unavailable");
        return 0;
    }
    HWND above_source = GetWindow(session->source_hwnd, GW_HWNDPREV);
    if (above_source == output)
        above_source = GetWindow(output, GW_HWNDPREV);
    if (!SetWindowPos(
            output,
            above_source,
            rect.left,
            rect.top,
            rect.right - rect.left,
            rect.bottom - rect.top,
            SWP_NOACTIVATE | SWP_NOOWNERZORDER | SWP_SHOWWINDOW)
        || !IsWindowVisible(output)
        || GetWindow(session->source_hwnd, GW_HWNDPREV) != output) {
        ShowWindow(output, SW_HIDE);
        write_reason(reason, reason_capacity, "generated output reveal failed; source playback remains visible");
        return 0;
    }
    session->generated_first_present.store(true, std::memory_order_release);
    session->cadence_stalled.store(false, std::memory_order_release);
    session->set_reason("A generated FP16 frame is visibly presented above the source HWND.");
    write_reason(reason, reason_capacity, "visible-generated-first-present");
    return 1;
}

extern "C" void ohmycine_windows_framegen_stop(void* opaque) noexcept {
    if (opaque == nullptr)
        return;
    auto* session = static_cast<WindowsFrameGenerationSession*>(opaque);
    // Hide first, then wait for GPU/session teardown. This ordering makes source mpv visibility
    // the atomic fallback even if DirectML is still finishing an in-flight dispatch.
    const HWND output = session->output_hwnd.load(std::memory_order_acquire);
    if (output != nullptr)
        ShowWindowAsync(output, SW_HIDE);
    session->stop_requested.store(true, std::memory_order_release);
    try {
        session->run_options.SetTerminate();
    }
    catch (...) {
    }
    if (session->worker.joinable())
        session->worker.join();
    if (session->watchdog.joinable())
        session->watchdog.join();
    delete session;
}

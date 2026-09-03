#[cfg(not(target_os = "windows"))]
fn main() {
    println!("{\"supported\":false,\"reason\":\"Windows-only probe\"}");
}

#[cfg(target_os = "windows")]
fn main() -> windows::core::Result<()> {
    use std::{
        collections::VecDeque,
        thread,
        time::{Duration, Instant},
    };
    use windows::{
        core::{factory, Interface, PCWSTR},
        Graphics::{
            Capture::{Direct3D11CaptureFramePool, GraphicsCaptureItem, GraphicsCaptureSession},
            DirectX::{Direct3D11::IDirect3DDevice, DirectXPixelFormat},
        },
        Win32::{
            Foundation::{FreeLibrary, HWND},
            Graphics::{
                Direct3D::{D3D_DRIVER_TYPE_HARDWARE, D3D_FEATURE_LEVEL_11_0},
                Direct3D11::{
                    D3D11CreateDevice, ID3D11Device, ID3D11Texture2D, D3D11_BIND_RENDER_TARGET,
                    D3D11_BIND_SHADER_RESOURCE, D3D11_CREATE_DEVICE_BGRA_SUPPORT,
                    D3D11_SDK_VERSION, D3D11_TEXTURE2D_DESC, D3D11_USAGE_DEFAULT,
                },
                Direct3D11on12::D3D11On12CreateDevice,
                Direct3D12::{
                    D3D12CreateDevice, ID3D12CommandQueue, ID3D12Device,
                    D3D12_COMMAND_LIST_TYPE_DIRECT, D3D12_COMMAND_QUEUE_DESC,
                    D3D12_COMMAND_QUEUE_FLAG_NONE,
                },
                Dxgi::{
                    Common::{
                        DXGI_ALPHA_MODE_IGNORE, DXGI_COLOR_SPACE_RGB_FULL_G10_NONE_P709,
                        DXGI_FORMAT_R16G16B16A16_FLOAT, DXGI_SAMPLE_DESC,
                    },
                    CreateDXGIFactory2, IDXGIDevice, IDXGIFactory2, IDXGISwapChain3,
                    DXGI_CREATE_FACTORY_FLAGS, DXGI_SCALING_STRETCH,
                    DXGI_SWAP_CHAIN_COLOR_SPACE_SUPPORT_FLAG_PRESENT, DXGI_SWAP_CHAIN_DESC1,
                    DXGI_SWAP_EFFECT_FLIP_DISCARD, DXGI_USAGE_RENDER_TARGET_OUTPUT,
                },
            },
            System::{
                LibraryLoader::LoadLibraryW,
                WinRT::{
                    Direct3D11::{
                        CreateDirect3D11DeviceFromDXGIDevice, IDirect3DDxgiInterfaceAccess,
                    },
                    Graphics::Capture::IGraphicsCaptureItemInterop,
                    RoInitialize, RoUninitialize, RO_INIT_MULTITHREADED,
                },
            },
            UI::WindowsAndMessaging::{FindWindowW, IsWindow},
        },
    };

    fn parse_hwnd() -> Option<HWND> {
        let explicit = std::env::args()
            .skip(1)
            .find_map(|arg| arg.strip_prefix("--hwnd=").map(str::to_owned))
            .or_else(|| std::env::var("OHMYCINE_MPV_HWND").ok());
        if let Some(value) = explicit {
            let value = value.trim().trim_start_matches("0x");
            let radix = if value.chars().any(|c| c.is_ascii_alphabetic()) {
                16
            } else {
                10
            };
            let parsed = isize::from_str_radix(value, radix).ok()?;
            let hwnd = HWND(parsed as *mut _);
            return unsafe { IsWindow(hwnd).as_bool() }.then_some(hwnd);
        }

        let class = "OhMyCineMpvSurface\0".encode_utf16().collect::<Vec<_>>();
        let hwnd = unsafe { FindWindowW(PCWSTR(class.as_ptr()), None) }.ok()?;
        unsafe { IsWindow(hwnd).as_bool() }.then_some(hwnd)
    }

    fn adapter_luid(device: &ID3D11Device) -> windows::core::Result<i64> {
        let dxgi: IDXGIDevice = device.cast()?;
        let adapter = unsafe { dxgi.GetAdapter()? };
        let desc = unsafe { adapter.GetDesc()? };
        Ok(((desc.AdapterLuid.HighPart as i64) << 32) | desc.AdapterLuid.LowPart as i64)
    }

    fn capture_frame_ring(
        hwnd: HWND,
        device: &ID3D11Device,
    ) -> windows::core::Result<serde_json::Value> {
        let interop: IGraphicsCaptureItemInterop =
            factory::<GraphicsCaptureItem, IGraphicsCaptureItemInterop>()?;
        let item: GraphicsCaptureItem = unsafe { interop.CreateForWindow(hwnd)? };
        let size = item.Size()?;
        let dxgi: IDXGIDevice = device.cast()?;
        let inspectable = unsafe { CreateDirect3D11DeviceFromDXGIDevice(&dxgi)? };
        let winrt_device: IDirect3DDevice = inspectable.cast()?;
        let pool = Direct3D11CaptureFramePool::CreateFreeThreaded(
            &winrt_device,
            DirectXPixelFormat::R16G16B16A16Float,
            3,
            size,
        )?;
        let session = pool.CreateCaptureSession(&item)?;
        session.SetIsCursorCaptureEnabled(false)?;
        session.StartCapture()?;

        let deadline = Instant::now() + Duration::from_secs(3);
        let mut frames = VecDeque::<(ID3D11Texture2D, i64, i32, i32)>::with_capacity(4);
        let mut source_luid = None;
        let mut capture_luid = None;
        let mut format_is_fp16 = true;
        let mut resize_count = 0_u32;
        let mut current_size = size;
        while Instant::now() < deadline {
            if let Ok(frame) = pool.TryGetNextFrame() {
                let frame_size = frame.ContentSize()?;
                if frame_size.Width <= 0 || frame_size.Height <= 0 {
                    continue;
                }
                if frame_size != current_size {
                    pool.Recreate(
                        &winrt_device,
                        DirectXPixelFormat::R16G16B16A16Float,
                        3,
                        frame_size,
                    )?;
                    current_size = frame_size;
                    frames.clear();
                    resize_count += 1;
                    continue;
                }
                let surface = frame.Surface()?;
                let access: IDirect3DDxgiInterfaceAccess = surface.cast()?;
                let texture: ID3D11Texture2D = unsafe { access.GetInterface()? };
                let capture_device = unsafe { texture.GetDevice()? };
                let mut desc = D3D11_TEXTURE2D_DESC::default();
                unsafe { texture.GetDesc(&mut desc) };
                source_luid = Some(adapter_luid(device)?);
                capture_luid = Some(adapter_luid(&capture_device)?);
                format_is_fp16 &= desc.Format == DXGI_FORMAT_R16G16B16A16_FLOAT;
                let timestamp = frame.SystemRelativeTime()?.Duration;
                if frames.len() == 4 {
                    frames.pop_front();
                }
                frames.push_back((texture, timestamp, frame_size.Width, frame_size.Height));
                if frames.len() >= 3 {
                    break;
                }
            }
            thread::sleep(Duration::from_millis(4));
        }
        session.Close()?;
        pool.Close()?;
        let monotonic = frames
            .iter()
            .map(|(_, timestamp, _, _)| *timestamp)
            .collect::<Vec<_>>()
            .windows(2)
            .all(|pair| pair[0] < pair[1]);
        let source_luid = source_luid.unwrap_or_default();
        let capture_luid = capture_luid.unwrap_or_default();
        let captured =
            frames.len() >= 2 && monotonic && format_is_fp16 && source_luid == capture_luid;
        let latest = frames.back();
        Ok(serde_json::json!({
            "captured": captured,
            "reason": (!captured).then_some("WGC did not produce two monotonic same-adapter FP16 frames within 3 seconds"),
            "ringDepth": frames.len(),
            "ringCapacity": 4,
            "timestampsMonotonic": monotonic,
            "resizeCount": resize_count,
            "width": latest.map(|frame| frame.2).unwrap_or(size.Width),
            "height": latest.map(|frame| frame.3).unwrap_or(size.Height),
            "dxgiFormat": format!("0x{:x}", DXGI_FORMAT_R16G16B16A16_FLOAT.0),
            "fp16": format_is_fp16,
            "sourceAdapterLuid": format!("0x{source_luid:016x}"),
            "captureAdapterLuid": format!("0x{capture_luid:016x}"),
            "adapterLuidMatches": source_luid == capture_luid,
        }))
    }

    fn create_same_adapter_bridge(
        d3d11_device: &ID3D11Device,
    ) -> windows::core::Result<(ID3D12Device, ID3D12CommandQueue, ID3D11Device)> {
        let dxgi: IDXGIDevice = d3d11_device.cast()?;
        let adapter = unsafe { dxgi.GetAdapter()? };
        let mut d3d12_device = None;
        unsafe { D3D12CreateDevice(&adapter, D3D_FEATURE_LEVEL_11_0, &mut d3d12_device)? };
        let d3d12_device: ID3D12Device =
            d3d12_device.expect("D3D12CreateDevice returned no device");
        let queue_desc = D3D12_COMMAND_QUEUE_DESC {
            Type: D3D12_COMMAND_LIST_TYPE_DIRECT,
            Priority: 0,
            Flags: D3D12_COMMAND_QUEUE_FLAG_NONE,
            NodeMask: 0,
        };
        let queue: ID3D12CommandQueue = unsafe { d3d12_device.CreateCommandQueue(&queue_desc)? };
        let queue_unknown = queue.cast()?;
        let mut bridge_device = None;
        unsafe {
            D3D11On12CreateDevice(
                &d3d12_device,
                D3D11_CREATE_DEVICE_BGRA_SUPPORT.0 as u32,
                Some(&[D3D_FEATURE_LEVEL_11_0]),
                Some(&[Some(queue_unknown)]),
                0,
                Some(&mut bridge_device),
                None,
                None,
            )?;
        }
        Ok((
            d3d12_device,
            queue,
            bridge_device.expect("D3D11On12CreateDevice returned no device"),
        ))
    }

    fn probe_scrgb_swapchain(
        queue: &ID3D12CommandQueue,
    ) -> windows::core::Result<serde_json::Value> {
        let factory: IDXGIFactory2 = unsafe { CreateDXGIFactory2(DXGI_CREATE_FACTORY_FLAGS(0))? };
        let desc = DXGI_SWAP_CHAIN_DESC1 {
            Width: 64,
            Height: 64,
            Format: DXGI_FORMAT_R16G16B16A16_FLOAT,
            Stereo: false.into(),
            SampleDesc: DXGI_SAMPLE_DESC {
                Count: 1,
                Quality: 0,
            },
            BufferUsage: DXGI_USAGE_RENDER_TARGET_OUTPUT,
            BufferCount: 3,
            Scaling: DXGI_SCALING_STRETCH,
            SwapEffect: DXGI_SWAP_EFFECT_FLIP_DISCARD,
            AlphaMode: DXGI_ALPHA_MODE_IGNORE,
            Flags: 0,
        };
        let swapchain = unsafe { factory.CreateSwapChainForComposition(queue, &desc, None)? };
        let swapchain: IDXGISwapChain3 = swapchain.cast()?;
        let support =
            unsafe { swapchain.CheckColorSpaceSupport(DXGI_COLOR_SPACE_RGB_FULL_G10_NONE_P709)? };
        let present_supported =
            support & DXGI_SWAP_CHAIN_COLOR_SPACE_SUPPORT_FLAG_PRESENT.0 as u32 != 0;
        if present_supported {
            unsafe { swapchain.SetColorSpace1(DXGI_COLOR_SPACE_RGB_FULL_G10_NONE_P709)? };
        }
        Ok(serde_json::json!({
            "created": true,
            "format": "R16G16B16A16_FLOAT",
            "colorSpace": "RGB_FULL_G10_NONE_P709",
            "colorSpacePresentSupported": present_supported,
            "bufferCount": 3,
            "note": "composition swapchain resource gate only; no product HWND was revealed",
        }))
    }

    let winrt_initialized = unsafe { RoInitialize(RO_INIT_MULTITHREADED) }.is_ok();
    let wgc_supported = winrt_initialized && GraphicsCaptureSession::IsSupported().unwrap_or(false);
    let directml_name = "DirectML.dll\0".encode_utf16().collect::<Vec<_>>();
    let directml = unsafe { LoadLibraryW(PCWSTR(directml_name.as_ptr())) }.ok();
    let directml_available = directml.as_ref().is_some_and(|module| !module.is_invalid());
    if let Some(module) = directml {
        unsafe { FreeLibrary(module)? };
    }

    let mut device: Option<ID3D11Device> = None;
    let mut selected_feature_level = Default::default();
    unsafe {
        D3D11CreateDevice(
            None,
            D3D_DRIVER_TYPE_HARDWARE,
            None,
            D3D11_CREATE_DEVICE_BGRA_SUPPORT,
            Some(&[D3D_FEATURE_LEVEL_11_0]),
            D3D11_SDK_VERSION,
            Some(&mut device),
            Some(&mut selected_feature_level),
            None,
        )?;
    }
    let bootstrap_device = device.expect("D3D11CreateDevice succeeded without returning a device");
    let (_d3d12_device, command_queue, device) = create_same_adapter_bridge(&bootstrap_device)?;
    let d3d11on12_luid_matches = adapter_luid(&bootstrap_device)? == adapter_luid(&device)?;
    let scrgb_swapchain = probe_scrgb_swapchain(&command_queue).unwrap_or_else(
        |error| serde_json::json!({ "created": false, "reason": error.to_string() }),
    );
    let desc = D3D11_TEXTURE2D_DESC {
        Width: 64,
        Height: 64,
        MipLevels: 1,
        ArraySize: 1,
        Format: DXGI_FORMAT_R16G16B16A16_FLOAT,
        SampleDesc: DXGI_SAMPLE_DESC {
            Count: 1,
            Quality: 0,
        },
        Usage: D3D11_USAGE_DEFAULT,
        BindFlags: (D3D11_BIND_SHADER_RESOURCE | D3D11_BIND_RENDER_TARGET).0 as u32,
        CPUAccessFlags: 0,
        MiscFlags: 0,
    };
    let mut fp16_texture_resource = None;
    let fp16_texture =
        unsafe { device.CreateTexture2D(&desc, None, Some(&mut fp16_texture_resource)) }.is_ok()
            && fp16_texture_resource.is_some();
    let hwnd = parse_hwnd();
    let hwnd_capture = if wgc_supported {
        hwnd.map(|value| {
            capture_frame_ring(value, &device).unwrap_or_else(
                |error| serde_json::json!({ "captured": false, "reason": error.to_string() }),
            )
        })
    } else {
        None
    };
    let strict_hwnd = std::env::args().any(|arg| arg == "--require-hwnd");
    let hwnd_ok = hwnd_capture.as_ref().is_some_and(|capture| {
        capture.get("captured").and_then(|value| value.as_bool()) == Some(true)
            && capture.get("fp16").and_then(|value| value.as_bool()) == Some(true)
            && capture
                .get("adapterLuidMatches")
                .and_then(|value| value.as_bool())
                == Some(true)
    });
    let bridge_ready =
        d3d11on12_luid_matches && adapter_luid(&device)? == adapter_luid(&bootstrap_device)?;
    let scrgb_ready = scrgb_swapchain
        .get("created")
        .and_then(|value| value.as_bool())
        == Some(true)
        && scrgb_swapchain
            .get("colorSpacePresentSupported")
            .and_then(|value| value.as_bool())
            == Some(true);
    let supported = wgc_supported
        && directml_available
        && fp16_texture
        && bridge_ready
        && scrgb_ready
        && (!strict_hwnd || hwnd_ok);

    println!(
        "{}",
        serde_json::json!({
            "supported": supported,
            "windowsGraphicsCapture": wgc_supported,
            "directMlRuntime": directml_available,
            "d3d11Fp16Texture": fp16_texture,
            "d3d11On12Bridge": bridge_ready,
            "d3d12CommandQueue": true,
            "scRgbSwapchain": scrgb_swapchain,
            "format": "R16G16B16A16_FLOAT",
            "featureLevel": format!("0x{:x}", selected_feature_level.0),
            "mpvHwndFound": hwnd.is_some(),
            "mpvHwndCapture": hwnd_capture,
            "strictHwndValidation": strict_hwnd,
            "scope": "WGC FP16 ring + same-adapter D3D11On12/D3D12 + scRGB composition swapchain resource gates; model dispatch and visible product presentation remain gated separately",
        })
    );
    if winrt_initialized {
        unsafe { RoUninitialize() };
    }
    Ok(())
}

// SPDX-License-Identifier: MIT

#define VK_USE_PLATFORM_ANDROID_KHR 1

#include "android_ncnn_frame_processor.h"

#include "rife_ncnn_runtime.h"

#include <allocator.h>
#include <command.h>
#include <gpu.h>
#include <net.h>
#include <pipeline.h>

#include <android/hardware_buffer.h>

#include <algorithm>
#include <chrono>
#include <cstring>
#include <cstdint>
#include <limits>
#include <vector>

namespace {

constexpr char kTypedFp16ProxyShader[] = R"glsl(
#version 450
layout(binding=0, rgba16f) readonly uniform image2D source_frame;
layout(binding=1) writeonly buffer Proxy { float values[]; } proxy;
layout(push_constant) uniform Parameters {
    int source_width;
    int source_height;
    int proxy_width;
    int proxy_height;
    int proxy_cstep;
    float reference_white_nits;
    float source_peak_nits;
} p;
void main() {
    int x = int(gl_GlobalInvocationID.x);
    int y = int(gl_GlobalInvocationID.y);
    if (x >= p.proxy_width || y >= p.proxy_height) return;
    int sx = min((x * p.source_width) / p.proxy_width, p.source_width - 1);
    int sy = min((y * p.source_height) / p.proxy_height, p.source_height - 1);
    vec3 linear_value = max(imageLoad(source_frame, ivec2(sx, sy)).rgb, vec3(0.0));
    float peak_scale = max(p.source_peak_nits / max(p.reference_white_nits, 1.0), 1.0);
    vec3 compressed = log2(vec3(1.0) + min(linear_value, vec3(peak_scale))) /
                      log2(1.0 + peak_scale);
    int offset = y * p.proxy_width + x;
    proxy.values[offset] = clamp(compressed.r, 0.0, 1.0);
    proxy.values[p.proxy_cstep + offset] = clamp(compressed.g, 0.0, 1.0);
    proxy.values[2 * p.proxy_cstep + offset] = clamp(compressed.b, 0.0, 1.0);
}
)glsl";

const char* output_format_qualifier(VkFormat format) {
    switch (format) {
    case VK_FORMAT_R16G16B16A16_SFLOAT:
        return "rgba16f";
    case VK_FORMAT_A2B10G10R10_UNORM_PACK32:
        return "rgb10_a2";
    case VK_FORMAT_R8G8B8A8_UNORM:
    case VK_FORMAT_R8G8B8A8_SRGB:
        return "rgba8";
    default:
        return nullptr;
    }
}

std::string composite_shader(VkFormat format, VkColorSpaceKHR color_space) {
    const char* qualifier = output_format_qualifier(format);
    if (qualifier == nullptr)
        return {};
    const int output_transfer = color_space == VK_COLOR_SPACE_HDR10_ST2084_EXT
        ? 1
        : (color_space == VK_COLOR_SPACE_EXTENDED_SRGB_LINEAR_EXT ? 0 : 2);
    std::string shader = R"glsl(
#version 450
layout(binding=0) readonly buffer Flow { float values[]; } flow_data;
layout(binding=1) readonly buffer Mask { float values[]; } mask_data;
layout(binding=2, rgba16f) readonly uniform image2D earlier_frame;
layout(binding=3, rgba16f) readonly uniform image2D later_frame;
layout(binding=4, OUTPUT_FORMAT) writeonly uniform image2D output_frame;
layout(push_constant) uniform Parameters {
    int output_width; int output_height; int flow_width; int flow_height;
    int flow_cstep; int mask_cstep; float timestep; int output_transfer;
} p;
vec3 sample_frame(readonly image2D frame, vec2 pixel) {
    ivec2 size = imageSize(frame);
    vec2 clamped = clamp(pixel, vec2(0.0), vec2(size) - vec2(1.0));
    ivec2 a = ivec2(floor(clamped));
    ivec2 b = min(a + ivec2(1), size - ivec2(1));
    vec2 f = fract(clamped);
    vec3 c00 = imageLoad(frame, a).rgb, c10 = imageLoad(frame, ivec2(b.x, a.y)).rgb;
    vec3 c01 = imageLoad(frame, ivec2(a.x, b.y)).rgb, c11 = imageLoad(frame, b).rgb;
    return mix(mix(c00, c10, f.x), mix(c01, c11, f.x), f.y);
}
float pq(float value) {
    float l = max(value, 0.0) / 125.0;
    float m1 = 0.1593017578125, m2 = 78.84375;
    float c1 = 0.8359375, c2 = 18.8515625, c3 = 18.6875;
    float lm = pow(l, m1);
    return pow((c1 + c2 * lm) / (1.0 + c3 * lm), m2);
}
vec3 encode_output(vec3 linear_rgb) {
    if (p.output_transfer == 0) return linear_rgb;
    if (p.output_transfer == 1) {
        mat3 rec709_to_2020 = mat3(
            0.627404, 0.069097, 0.016391,
            0.329283, 0.919540, 0.088013,
            0.043313, 0.011362, 0.895595);
        vec3 rec2020 = max(rec709_to_2020 * linear_rgb, vec3(0.0));
        return vec3(pq(rec2020.r), pq(rec2020.g), pq(rec2020.b));
    }
    bvec3 low = lessThanEqual(linear_rgb, vec3(0.0031308));
    vec3 encoded = mix(1.055 * pow(max(linear_rgb, vec3(0.0)), vec3(1.0 / 2.4)) - 0.055,
                       12.92 * linear_rgb, low);
    return clamp(encoded, 0.0, 1.0);
}
void main() {
    int x = int(gl_GlobalInvocationID.x), y = int(gl_GlobalInvocationID.y);
    if (x >= p.output_width || y >= p.output_height) return;
    int fx = min((x * p.flow_width) / p.output_width, p.flow_width - 1);
    int fy = min((y * p.flow_height) / p.output_height, p.flow_height - 1);
    int index = fy * p.flow_width + fx;
    vec4 flow = vec4(flow_data.values[index], flow_data.values[p.flow_cstep + index],
                     flow_data.values[2 * p.flow_cstep + index], flow_data.values[3 * p.flow_cstep + index]);
    vec2 scale = vec2(p.output_width, p.output_height) / vec2(p.flow_width, p.flow_height);
    vec2 pixel = vec2(x, y);
    vec3 a = sample_frame(earlier_frame, pixel + flow.xy * scale);
    vec3 b = sample_frame(later_frame, pixel + flow.zw * scale);
    float mask = clamp(mask_data.values[min(index, p.mask_cstep - 1)], 0.0, 1.0);
    vec3 composed = mix(b, a, mask);
    imageStore(output_frame, ivec2(x, y), vec4(encode_output(composed), 1.0));
}
)glsl";
    const size_t marker = shader.find("OUTPUT_FORMAT");
    shader.replace(marker, std::strlen("OUTPUT_FORMAT"), qualifier);
    (void)output_transfer;
    return shader;
}

uint32_t find_memory_type(
    const ncnn::VulkanDevice* device,
    uint32_t allowed_types) {
    return device->find_memory_index(
        allowed_types,
        0,
        VK_MEMORY_PROPERTY_DEVICE_LOCAL_BIT,
        VK_MEMORY_PROPERTY_HOST_VISIBLE_BIT);
}

bool has_device_extension(const ncnn::GpuInfo& info, const char* required) {
    const auto& extensions = info.deviceExtensionProperties();
    return std::any_of(extensions.begin(), extensions.end(), [required](const auto& extension) {
        return std::strcmp(extension.extensionName, required) == 0;
    });
}

}  // namespace

class AndroidNcnnFrameProcessor::Impl final {
public:
    bool create(
        ANativeWindow* output_window,
        const char* model_param_path,
        const char* model_bin_path,
        std::string* reason) {
        destroy();
        if (output_window == nullptr || model_param_path == nullptr || model_bin_path == nullptr) {
            *reason = "RIFE model paths are unavailable";
            return false;
        }
        if (ncnn::create_gpu_instance() != 0 || ncnn::get_gpu_count() <= 0) {
            *reason = "ncnn Vulkan instance creation failed";
            ncnn::destroy_gpu_instance();
            return false;
        }
        gpu_instance_created_ = true;
        device_ = ncnn::get_gpu_device(0);
        if (device_ == nullptr || !device_->is_valid() ||
            !device_->info.support_fp16_image() ||
            !has_device_extension(
                device_->info,
                "VK_ANDROID_external_memory_android_hardware_buffer")) {
            *reason = "ncnn Vulkan device cannot import typed FP16 AHardwareBuffer images";
            destroy();
            return false;
        }
        get_ahb_properties_ =
            reinterpret_cast<PFN_vkGetAndroidHardwareBufferPropertiesANDROID>(
                ncnn::vkGetDeviceProcAddr(
                    device_->vkdevice(), "vkGetAndroidHardwareBufferPropertiesANDROID"));
        if (get_ahb_properties_ == nullptr) {
            *reason = "ncnn Vulkan device did not enable Android hardware-buffer import";
            destroy();
            return false;
        }
        if (!create_output_swapchain(output_window, reason)) {
            destroy();
            return false;
        }
        blob_allocator_ = device_->acquire_blob_allocator();
        staging_allocator_ = device_->acquire_staging_allocator();
        if (blob_allocator_ == nullptr || staging_allocator_ == nullptr) {
            *reason = "ncnn Vulkan allocators are unavailable";
            destroy();
            return false;
        }
        const auto load_network = [&](RifeNcnnCompatibilityMode mode) {
            if (network_) {
                network_->clear();
                network_.reset();
            }
            options_ = make_rife_ncnn_option(mode, blob_allocator_, staging_allocator_);
            network_ = std::make_unique<ncnn::Net>();
            const RifeNcnnLoadResult result = load_rife_ncnn_network(
                *network_,
                device_,
                model_param_path,
                model_bin_path,
                mode,
                options_);
            model_load_attempts_.push_back(format_rife_ncnn_load_result(result));
            return result.loaded();
        };
        model_load_attempts_.clear();
        if (!load_network(RifeNcnnCompatibilityMode::PackedFp16) &&
            !load_network(RifeNcnnCompatibilityMode::SafeFp32) &&
            !load_network(RifeNcnnCompatibilityMode::SafeFp32HostWeights)) {
            std::string attempts;
            for (const std::string& attempt : model_load_attempts_) {
                if (!attempts.empty())
                    attempts += ";";
                attempts += attempt;
            }
            *reason = "RIFE flow/mask model load failed (" + attempts + ")";
            destroy();
            return false;
        }
        std::vector<uint32_t> proxy_spirv;
        if (ncnn::compile_spirv_module(kTypedFp16ProxyShader, options_, proxy_spirv) != 0 ||
            proxy_spirv.empty()) {
            *reason = "Typed RGBA16F tone-compressed proxy shader compilation failed";
            destroy();
            return false;
        }
        proxy_pipeline_ = std::make_unique<ncnn::Pipeline>(device_);
        proxy_pipeline_->set_local_size_xyz(8, 8, 1);
        if (proxy_pipeline_->create(
                proxy_spirv.data(), proxy_spirv.size() * sizeof(uint32_t), {}) != 0) {
            *reason = "Typed RGBA16F proxy pipeline creation failed";
            destroy();
            return false;
        }
        const std::string composite_source = composite_shader(
            swapchain_format_, swapchain_color_space_);
        std::vector<uint32_t> composite_spirv;
        if (composite_source.empty() || ncnn::compile_spirv_module(
                composite_source.c_str(), options_, composite_spirv) != 0 ||
            composite_spirv.empty()) {
            *reason = "FP16 flow/mask composite shader compilation failed";
            destroy();
            return false;
        }
        composite_pipeline_ = std::make_unique<ncnn::Pipeline>(device_);
        composite_pipeline_->set_local_size_xyz(8, 8, 1);
        if (composite_pipeline_->create(
                composite_spirv.data(), composite_spirv.size() * sizeof(uint32_t), {}) != 0) {
            *reason = "FP16 flow/mask composite pipeline creation failed";
            destroy();
            return false;
        }
        return true;
    }

    void destroy() {
        previous_proxy_.release();
        cleanup_previous_import();
        composite_pipeline_.reset();
        proxy_pipeline_.reset();
        if (network_) {
            network_->clear();
            network_.reset();
        }
        if (device_ != nullptr) {
            for (VkImageView view : swapchain_views_) {
                if (view != VK_NULL_HANDLE)
                    ncnn::vkDestroyImageView(device_->vkdevice(), view, nullptr);
            }
            swapchain_views_.clear();
            swapchain_images_.clear();
            swapchain_layouts_.clear();
            if (swapchain_ != VK_NULL_HANDLE)
                device_->vkDestroySwapchainKHR(device_->vkdevice(), swapchain_, nullptr);
        }
        swapchain_ = VK_NULL_HANDLE;
        if (output_surface_ != VK_NULL_HANDLE && ncnn::vkDestroySurfaceKHR != nullptr)
            ncnn::vkDestroySurfaceKHR(ncnn::get_gpu_instance(), output_surface_, nullptr);
        output_surface_ = VK_NULL_HANDLE;
        if (device_ != nullptr && staging_allocator_ != nullptr)
            device_->reclaim_staging_allocator(staging_allocator_);
        if (device_ != nullptr && blob_allocator_ != nullptr)
            device_->reclaim_blob_allocator(blob_allocator_);
        staging_allocator_ = nullptr;
        blob_allocator_ = nullptr;
        device_ = nullptr;
        get_ahb_properties_ = nullptr;
        if (gpu_instance_created_)
            ncnn::destroy_gpu_instance();
        gpu_instance_created_ = false;
        proxy_width_ = 0;
        proxy_height_ = 0;
        snapshot_ = {};
    }

    bool process(AHardwareBuffer* hardware_buffer, std::string* reason) {
        if (device_ == nullptr || network_ == nullptr || proxy_pipeline_ == nullptr ||
            hardware_buffer == nullptr) {
            *reason = "ncnn frame processor is not initialized";
            return false;
        }
        AHardwareBuffer_Desc description{};
        AHardwareBuffer_describe(hardware_buffer, &description);
        if (description.format != AHARDWAREBUFFER_FORMAT_R16G16B16A16_FLOAT ||
            description.width == 0 || description.height == 0) {
            *reason = "Decoder frame is not a typed RGBA16F AHardwareBuffer";
            return false;
        }
        VkAndroidHardwareBufferFormatPropertiesANDROID format_properties{};
        format_properties.sType =
            VK_STRUCTURE_TYPE_ANDROID_HARDWARE_BUFFER_FORMAT_PROPERTIES_ANDROID;
        VkAndroidHardwareBufferPropertiesANDROID buffer_properties{};
        buffer_properties.sType = VK_STRUCTURE_TYPE_ANDROID_HARDWARE_BUFFER_PROPERTIES_ANDROID;
        buffer_properties.pNext = &format_properties;
        if (get_ahb_properties_(
                device_->vkdevice(), hardware_buffer, &buffer_properties) != VK_SUCCESS ||
            format_properties.format != VK_FORMAT_R16G16B16A16_SFLOAT) {
            *reason = "ncnn Vulkan device rejected the typed RGBA16F AHardwareBuffer";
            return false;
        }

        VkExternalMemoryImageCreateInfo external_info{};
        external_info.sType = VK_STRUCTURE_TYPE_EXTERNAL_MEMORY_IMAGE_CREATE_INFO;
        external_info.handleTypes =
            VK_EXTERNAL_MEMORY_HANDLE_TYPE_ANDROID_HARDWARE_BUFFER_BIT_ANDROID;
        VkImageCreateInfo image_info{};
        image_info.sType = VK_STRUCTURE_TYPE_IMAGE_CREATE_INFO;
        image_info.pNext = &external_info;
        image_info.imageType = VK_IMAGE_TYPE_2D;
        image_info.format = format_properties.format;
        image_info.extent = {description.width, description.height, 1};
        image_info.mipLevels = 1;
        image_info.arrayLayers = 1;
        image_info.samples = VK_SAMPLE_COUNT_1_BIT;
        image_info.tiling = VK_IMAGE_TILING_OPTIMAL;
        image_info.usage = VK_IMAGE_USAGE_STORAGE_BIT | VK_IMAGE_USAGE_SAMPLED_BIT;
        image_info.sharingMode = VK_SHARING_MODE_EXCLUSIVE;
        image_info.initialLayout = VK_IMAGE_LAYOUT_UNDEFINED;
        VkImage image = VK_NULL_HANDLE;
        if (ncnn::vkCreateImage(device_->vkdevice(), &image_info, nullptr, &image) != VK_SUCCESS) {
            *reason = "ncnn session failed to create the imported VkImage";
            return false;
        }
        VkMemoryRequirements memory_requirements{};
        ncnn::vkGetImageMemoryRequirements(device_->vkdevice(), image, &memory_requirements);
        const uint32_t memory_type = find_memory_type(
            device_, memory_requirements.memoryTypeBits & buffer_properties.memoryTypeBits);
        if (memory_type == std::numeric_limits<uint32_t>::max()) {
            ncnn::vkDestroyImage(device_->vkdevice(), image, nullptr);
            *reason = "ncnn session found no compatible AHardwareBuffer memory type";
            return false;
        }
        VkImportAndroidHardwareBufferInfoANDROID import_info{};
        import_info.sType = VK_STRUCTURE_TYPE_IMPORT_ANDROID_HARDWARE_BUFFER_INFO_ANDROID;
        import_info.buffer = hardware_buffer;
        VkMemoryDedicatedAllocateInfo dedicated_info{};
        dedicated_info.sType = VK_STRUCTURE_TYPE_MEMORY_DEDICATED_ALLOCATE_INFO;
        dedicated_info.pNext = &import_info;
        dedicated_info.image = image;
        VkMemoryAllocateInfo allocation_info{};
        allocation_info.sType = VK_STRUCTURE_TYPE_MEMORY_ALLOCATE_INFO;
        allocation_info.pNext = &dedicated_info;
        allocation_info.allocationSize = buffer_properties.allocationSize;
        allocation_info.memoryTypeIndex = memory_type;
        VkDeviceMemory memory = VK_NULL_HANDLE;
        if (ncnn::vkAllocateMemory(device_->vkdevice(), &allocation_info, nullptr, &memory) != VK_SUCCESS ||
            ncnn::vkBindImageMemory(device_->vkdevice(), image, memory, 0) != VK_SUCCESS) {
            if (memory != VK_NULL_HANDLE)
                ncnn::vkFreeMemory(device_->vkdevice(), memory, nullptr);
            ncnn::vkDestroyImage(device_->vkdevice(), image, nullptr);
            *reason = "ncnn session failed to bind imported AHardwareBuffer memory";
            return false;
        }
        VkImageViewCreateInfo view_info{};
        view_info.sType = VK_STRUCTURE_TYPE_IMAGE_VIEW_CREATE_INFO;
        view_info.image = image;
        view_info.viewType = VK_IMAGE_VIEW_TYPE_2D;
        view_info.format = format_properties.format;
        view_info.subresourceRange.aspectMask = VK_IMAGE_ASPECT_COLOR_BIT;
        view_info.subresourceRange.levelCount = 1;
        view_info.subresourceRange.layerCount = 1;
        VkImageView image_view = VK_NULL_HANDLE;
        if (ncnn::vkCreateImageView(device_->vkdevice(), &view_info, nullptr, &image_view) != VK_SUCCESS) {
            ncnn::vkFreeMemory(device_->vkdevice(), memory, nullptr);
            ncnn::vkDestroyImage(device_->vkdevice(), image, nullptr);
            *reason = "ncnn session failed to create imported RGBA16F image view";
            return false;
        }

        ncnn::VkImageMemory image_memory{};
        image_memory.image = image;
        image_memory.imageview = image_view;
        image_memory.width = description.width;
        image_memory.height = description.height;
        image_memory.depth = 1;
        image_memory.format = format_properties.format;
        image_memory.memory = memory;
        image_memory.mapped_ptr = nullptr;
        image_memory.memory_type_index = memory_type;
        image_memory.bind_offset = 0;
        image_memory.bind_capacity = buffer_properties.allocationSize;
        image_memory.access_flags = 0;
        image_memory.image_layout = VK_IMAGE_LAYOUT_UNDEFINED;
        image_memory.stage_flags = VK_PIPELINE_STAGE_TOP_OF_PIPE_BIT;
        image_memory.command_refcount = 0;
        image_memory.refcount = 1;
        ncnn::VkImageMat source(
            description.width,
            description.height,
            1,
            &image_memory,
            8u,
            4,
            nullptr);

        const int target_width = std::max(32, static_cast<int>(description.width / 2 / 32 * 32));
        const int target_height = std::max(32, static_cast<int>(description.height / 2 / 32 * 32));
        if (target_width != proxy_width_ || target_height != proxy_height_) {
            previous_proxy_.release();
            cleanup_previous_import();
            proxy_width_ = target_width;
            proxy_height_ = target_height;
        }
        ncnn::VkMat proxy(proxy_width_, proxy_height_, 3, 4u, 1, blob_allocator_);
        if (proxy.empty()) {
            cleanup_import(image_view, image, memory);
            *reason = "ncnn proxy VkMat allocation failed";
            return false;
        }
        std::vector<ncnn::VkMat> buffer_bindings{proxy};
        std::vector<ncnn::VkImageMat> image_bindings{source};
        std::vector<ncnn::vk_constant_type> constants(7);
        constants[0].i = description.width;
        constants[1].i = description.height;
        constants[2].i = proxy_width_;
        constants[3].i = proxy_height_;
        constants[4].i = proxy.cstep;
        constants[5].f = 100.0F;
        constants[6].f = 1000.0F;
        ncnn::VkCompute proxy_command(device_);
        proxy_command.record_pipeline(
            proxy_pipeline_.get(), buffer_bindings, image_bindings, constants, proxy);
        if (proxy_command.submit_and_wait() != 0) {
            cleanup_import(image_view, image, memory);
            *reason = "RGBA16F tone-compressed proxy dispatch failed";
            return false;
        }
        ++snapshot_.proxy_frames;

        if (!previous_proxy_.empty()) {
            const auto started = std::chrono::steady_clock::now();
            ncnn::Mat timestep_cpu(proxy_width_, proxy_height_, 1);
            timestep_cpu.fill(0.5F);
            ncnn::VkMat timestep_gpu;
            ncnn::VkCompute inference_command(device_);
            inference_command.record_upload(timestep_cpu, timestep_gpu, options_);
            ncnn::Extractor extractor = network_->create_extractor();
            extractor.set_light_mode(false);
            extractor.set_blob_vkallocator(blob_allocator_);
            extractor.set_workspace_vkallocator(blob_allocator_);
            extractor.set_staging_vkallocator(staging_allocator_);
            ncnn::VkMat flow;
            ncnn::VkMat mask;
            if (extractor.input("in0", previous_proxy_) != 0 ||
                extractor.input("in1", proxy) != 0 ||
                extractor.input("in2", timestep_gpu) != 0 ||
                extractor.extract("327", flow, inference_command) != 0 ||
                extractor.extract("332", mask, inference_command) != 0 ||
                inference_command.submit_and_wait() != 0 ||
                flow.empty() || mask.empty()) {
                cleanup_import(image_view, image, memory);
                *reason = "Live ncnn Vulkan RIFE flow/mask inference failed";
                return false;
            }
            ++snapshot_.inferred_pairs;
            snapshot_.latest_inference_ms = std::chrono::duration<double, std::milli>(
                std::chrono::steady_clock::now() - started).count();
            if (!present_composite(image_memory, flow, mask, reason)) {
                cleanup_import(image_view, image, memory);
                return false;
            }
        }
        cleanup_previous_import();
        previous_image_memory_ = image_memory;
        previous_image_ = image;
        previous_image_view_ = image_view;
        previous_memory_ = memory;
        previous_width_ = description.width;
        previous_height_ = description.height;
        previous_proxy_ = proxy;
        return true;
    }

    AndroidNcnnFrameProcessorSnapshot snapshot() const {
        return snapshot_;
    }

private:
    bool present_composite(
        ncnn::VkImageMemory& current_image_memory,
        const ncnn::VkMat& flow,
        const ncnn::VkMat& mask,
        std::string* reason) {
        if (previous_image_ == VK_NULL_HANDLE || swapchain_ == VK_NULL_HANDLE ||
            composite_pipeline_ == nullptr) {
            *reason = "Two original FP16 frames or the output swapchain are unavailable";
            return false;
        }
        VkFenceCreateInfo acquire_fence_info{};
        acquire_fence_info.sType = VK_STRUCTURE_TYPE_FENCE_CREATE_INFO;
        VkFence acquire_fence = VK_NULL_HANDLE;
        if (ncnn::vkCreateFence(
                device_->vkdevice(), &acquire_fence_info, nullptr, &acquire_fence) !=
                VK_SUCCESS) {
            *reason = "Output swapchain acquire fence creation failed";
            return false;
        }
        uint32_t image_index = 0;
        const VkResult acquire_result = device_->vkAcquireNextImageKHR(
            device_->vkdevice(), swapchain_, 1'000'000'000ULL,
            VK_NULL_HANDLE, acquire_fence, &image_index);
        const bool acquired =
            (acquire_result == VK_SUCCESS || acquire_result == VK_SUBOPTIMAL_KHR) &&
            ncnn::vkWaitForFences(
                device_->vkdevice(), 1, &acquire_fence, VK_TRUE,
                1'000'000'000ULL) == VK_SUCCESS;
        ncnn::vkDestroyFence(device_->vkdevice(), acquire_fence, nullptr);
        if (!acquired || image_index >= swapchain_images_.size()) {
            *reason = "Output swapchain image acquisition timed out or became invalid";
            return false;
        }

        ncnn::VkImageMemory output_memory{};
        output_memory.image = swapchain_images_[image_index];
        output_memory.imageview = swapchain_views_[image_index];
        output_memory.width = swapchain_extent_.width;
        output_memory.height = swapchain_extent_.height;
        output_memory.depth = 1;
        output_memory.format = swapchain_format_;
        output_memory.memory = VK_NULL_HANDLE;
        output_memory.memory_type_index = 0;
        output_memory.bind_offset = 0;
        output_memory.bind_capacity = 0;
        output_memory.access_flags = 0;
        output_memory.image_layout = swapchain_layouts_[image_index];
        output_memory.stage_flags = VK_PIPELINE_STAGE_TOP_OF_PIPE_BIT;
        output_memory.command_refcount = 0;
        output_memory.refcount = 1;
        const size_t output_elemsize = swapchain_format_ == VK_FORMAT_R16G16B16A16_SFLOAT
            ? 8u : 4u;
        ncnn::VkImageMat output(
            swapchain_extent_.width,
            swapchain_extent_.height,
            1,
            &output_memory,
            output_elemsize,
            4,
            nullptr);
        ncnn::VkImageMat earlier(
            previous_width_, previous_height_, 1, &previous_image_memory_, 8u, 4, nullptr);
        ncnn::VkImageMat later(
            current_image_memory.width,
            current_image_memory.height,
            1,
            &current_image_memory,
            8u,
            4,
            nullptr);
        ncnn::VkCompute composite_command(device_);
        ncnn::VkMat flow_pack1;
        ncnn::VkMat mask_pack1;
        device_->convert_packing(flow, flow_pack1, 1, composite_command, options_);
        device_->convert_packing(mask, mask_pack1, 1, composite_command, options_);
        std::vector<ncnn::VkMat> buffers{flow_pack1, mask_pack1};
        std::vector<ncnn::VkImageMat> images{earlier, later, output};
        std::vector<ncnn::vk_constant_type> constants(8);
        constants[0].i = swapchain_extent_.width;
        constants[1].i = swapchain_extent_.height;
        constants[2].i = flow_pack1.w;
        constants[3].i = flow_pack1.h;
        constants[4].i = flow_pack1.cstep;
        constants[5].i = mask_pack1.cstep;
        constants[6].f = 0.5F;
        constants[7].i = swapchain_color_space_ == VK_COLOR_SPACE_HDR10_ST2084_EXT
            ? 1
            : (swapchain_color_space_ == VK_COLOR_SPACE_EXTENDED_SRGB_LINEAR_EXT ? 0 : 2);
        composite_command.record_pipeline(
            composite_pipeline_.get(), buffers, images, constants, output);
        if (composite_command.submit_and_wait() != 0 ||
            !transition_to_present(output_memory.image, output_memory.image_layout)) {
            *reason = "FP16 original-frame composite or present-layout transition failed";
            return false;
        }
        VkQueue present_queue = device_->acquire_queue(queue_family_);
        if (present_queue == VK_NULL_HANDLE) {
            *reason = "ncnn present queue is unavailable";
            return false;
        }
        VkPresentInfoKHR present_info{};
        present_info.sType = VK_STRUCTURE_TYPE_PRESENT_INFO_KHR;
        present_info.swapchainCount = 1;
        present_info.pSwapchains = &swapchain_;
        present_info.pImageIndices = &image_index;
        const VkResult present_result = device_->vkQueuePresentKHR(present_queue, &present_info);
        device_->reclaim_queue(queue_family_, present_queue);
        if (present_result != VK_SUCCESS && present_result != VK_SUBOPTIMAL_KHR) {
            *reason = "vkQueuePresentKHR failed for the generated FP16 frame";
            return false;
        }
        swapchain_layouts_[image_index] = VK_IMAGE_LAYOUT_PRESENT_SRC_KHR;
        ++snapshot_.presented_frames;
        return true;
    }

    bool transition_to_present(VkImage image, VkImageLayout old_layout) {
        VkCommandPoolCreateInfo pool_info{};
        pool_info.sType = VK_STRUCTURE_TYPE_COMMAND_POOL_CREATE_INFO;
        pool_info.flags = VK_COMMAND_POOL_CREATE_TRANSIENT_BIT;
        pool_info.queueFamilyIndex = queue_family_;
        VkCommandPool pool = VK_NULL_HANDLE;
        if (ncnn::vkCreateCommandPool(
                device_->vkdevice(), &pool_info, nullptr, &pool) != VK_SUCCESS)
            return false;
        VkCommandBufferAllocateInfo allocate_info{};
        allocate_info.sType = VK_STRUCTURE_TYPE_COMMAND_BUFFER_ALLOCATE_INFO;
        allocate_info.commandPool = pool;
        allocate_info.level = VK_COMMAND_BUFFER_LEVEL_PRIMARY;
        allocate_info.commandBufferCount = 1;
        VkCommandBuffer command = VK_NULL_HANDLE;
        if (ncnn::vkAllocateCommandBuffers(
                device_->vkdevice(), &allocate_info, &command) != VK_SUCCESS) {
            ncnn::vkDestroyCommandPool(device_->vkdevice(), pool, nullptr);
            return false;
        }
        VkCommandBufferBeginInfo begin_info{};
        begin_info.sType = VK_STRUCTURE_TYPE_COMMAND_BUFFER_BEGIN_INFO;
        begin_info.flags = VK_COMMAND_BUFFER_USAGE_ONE_TIME_SUBMIT_BIT;
        ncnn::vkBeginCommandBuffer(command, &begin_info);
        VkImageMemoryBarrier barrier{};
        barrier.sType = VK_STRUCTURE_TYPE_IMAGE_MEMORY_BARRIER;
        barrier.srcAccessMask = VK_ACCESS_SHADER_WRITE_BIT;
        barrier.dstAccessMask = 0;
        barrier.oldLayout = old_layout;
        barrier.newLayout = VK_IMAGE_LAYOUT_PRESENT_SRC_KHR;
        barrier.srcQueueFamilyIndex = VK_QUEUE_FAMILY_IGNORED;
        barrier.dstQueueFamilyIndex = VK_QUEUE_FAMILY_IGNORED;
        barrier.image = image;
        barrier.subresourceRange.aspectMask = VK_IMAGE_ASPECT_COLOR_BIT;
        barrier.subresourceRange.levelCount = 1;
        barrier.subresourceRange.layerCount = 1;
        ncnn::vkCmdPipelineBarrier(
            command,
            VK_PIPELINE_STAGE_COMPUTE_SHADER_BIT,
            VK_PIPELINE_STAGE_BOTTOM_OF_PIPE_BIT,
            0,
            0,
            nullptr,
            0,
            nullptr,
            1,
            &barrier);
        if (ncnn::vkEndCommandBuffer(command) != VK_SUCCESS) {
            ncnn::vkDestroyCommandPool(device_->vkdevice(), pool, nullptr);
            return false;
        }
        VkQueue queue = device_->acquire_queue(queue_family_);
        if (queue == VK_NULL_HANDLE) {
            ncnn::vkDestroyCommandPool(device_->vkdevice(), pool, nullptr);
            return false;
        }
        VkSubmitInfo submit_info{};
        submit_info.sType = VK_STRUCTURE_TYPE_SUBMIT_INFO;
        submit_info.commandBufferCount = 1;
        submit_info.pCommandBuffers = &command;
        const bool submitted = ncnn::vkQueueSubmit(
            queue, 1, &submit_info, VK_NULL_HANDLE) == VK_SUCCESS &&
            ncnn::vkQueueWaitIdle(queue) == VK_SUCCESS;
        device_->reclaim_queue(queue_family_, queue);
        ncnn::vkDestroyCommandPool(device_->vkdevice(), pool, nullptr);
        return submitted;
    }

    void cleanup_previous_import() {
        if (device_ != nullptr && previous_image_ != VK_NULL_HANDLE)
            cleanup_import(previous_image_view_, previous_image_, previous_memory_);
        previous_image_ = VK_NULL_HANDLE;
        previous_image_view_ = VK_NULL_HANDLE;
        previous_memory_ = VK_NULL_HANDLE;
        previous_image_memory_ = {};
        previous_width_ = 0;
        previous_height_ = 0;
    }

    bool create_output_swapchain(ANativeWindow* output_window, std::string* reason) {
        auto create_android_surface = reinterpret_cast<PFN_vkCreateAndroidSurfaceKHR>(
            ncnn::vkGetInstanceProcAddr(
                ncnn::get_gpu_instance(), "vkCreateAndroidSurfaceKHR"));
        if (create_android_surface == nullptr ||
            ncnn::vkGetPhysicalDeviceSurfaceSupportKHR == nullptr ||
            ncnn::vkGetPhysicalDeviceSurfaceCapabilitiesKHR == nullptr ||
            ncnn::vkGetPhysicalDeviceSurfaceFormatsKHR == nullptr ||
            device_->vkCreateSwapchainKHR == nullptr ||
            device_->vkGetSwapchainImagesKHR == nullptr ||
            device_->vkQueuePresentKHR == nullptr) {
            *reason = "ncnn Vulkan device lacks Android Surface/swapchain entry points";
            return false;
        }
        VkAndroidSurfaceCreateInfoKHR surface_info{};
        surface_info.sType = VK_STRUCTURE_TYPE_ANDROID_SURFACE_CREATE_INFO_KHR;
        surface_info.window = output_window;
        if (create_android_surface(
                ncnn::get_gpu_instance(), &surface_info, nullptr, &output_surface_) != VK_SUCCESS) {
            *reason = "ncnn Vulkan instance cannot create the real output Surface";
            return false;
        }
        queue_family_ = device_->info.compute_queue_family_index();
        VkBool32 present_supported = VK_FALSE;
        if (ncnn::vkGetPhysicalDeviceSurfaceSupportKHR(
                device_->info.physicalDevice(), queue_family_, output_surface_,
                &present_supported) != VK_SUCCESS || present_supported != VK_TRUE) {
            *reason = "ncnn compute queue cannot present to the real Android Surface";
            return false;
        }
        VkSurfaceCapabilitiesKHR capabilities{};
        if (ncnn::vkGetPhysicalDeviceSurfaceCapabilitiesKHR(
                device_->info.physicalDevice(), output_surface_, &capabilities) != VK_SUCCESS ||
            (capabilities.supportedUsageFlags & VK_IMAGE_USAGE_STORAGE_BIT) == 0) {
            *reason = "Android output swapchain does not support FP16 compute storage writes";
            return false;
        }
        uint32_t format_count = 0;
        if (ncnn::vkGetPhysicalDeviceSurfaceFormatsKHR(
                device_->info.physicalDevice(), output_surface_, &format_count, nullptr) !=
                VK_SUCCESS || format_count == 0) {
            *reason = "Android output Surface exposes no Vulkan format";
            return false;
        }
        std::vector<VkSurfaceFormatKHR> formats(format_count);
        ncnn::vkGetPhysicalDeviceSurfaceFormatsKHR(
            device_->info.physicalDevice(), output_surface_, &format_count, formats.data());
        auto selected = std::find_if(formats.begin(), formats.end(), [](const auto& candidate) {
            return candidate.format == VK_FORMAT_R16G16B16A16_SFLOAT &&
                   candidate.colorSpace == VK_COLOR_SPACE_EXTENDED_SRGB_LINEAR_EXT;
        });
        if (selected == formats.end()) {
            selected = std::find_if(formats.begin(), formats.end(), [](const auto& candidate) {
                return candidate.format == VK_FORMAT_A2B10G10R10_UNORM_PACK32 &&
                       candidate.colorSpace == VK_COLOR_SPACE_HDR10_ST2084_EXT;
            });
        }
        if (selected == formats.end()) {
            selected = std::find_if(formats.begin(), formats.end(), [](const auto& candidate) {
                return candidate.format == VK_FORMAT_R8G8B8A8_UNORM ||
                       candidate.format == VK_FORMAT_R8G8B8A8_SRGB;
            });
        }
        if (selected == formats.end()) {
            *reason = "No supported linear-HDR, HDR10, or SDR output swapchain format is available";
            return false;
        }
        swapchain_format_ = selected->format;
        swapchain_color_space_ = selected->colorSpace;
        VkExtent2D extent = capabilities.currentExtent;
        if (extent.width == std::numeric_limits<uint32_t>::max()) {
            extent.width = std::clamp(
                static_cast<uint32_t>(std::max(1, ANativeWindow_getWidth(output_window))),
                capabilities.minImageExtent.width,
                capabilities.maxImageExtent.width);
            extent.height = std::clamp(
                static_cast<uint32_t>(std::max(1, ANativeWindow_getHeight(output_window))),
                capabilities.minImageExtent.height,
                capabilities.maxImageExtent.height);
        }
        swapchain_extent_ = extent;
        uint32_t image_count = std::max(2U, capabilities.minImageCount);
        if (capabilities.maxImageCount > 0)
            image_count = std::min(image_count, capabilities.maxImageCount);
        VkSwapchainCreateInfoKHR swapchain_info{};
        swapchain_info.sType = VK_STRUCTURE_TYPE_SWAPCHAIN_CREATE_INFO_KHR;
        swapchain_info.surface = output_surface_;
        swapchain_info.minImageCount = image_count;
        swapchain_info.imageFormat = swapchain_format_;
        swapchain_info.imageColorSpace = swapchain_color_space_;
        swapchain_info.imageExtent = swapchain_extent_;
        swapchain_info.imageArrayLayers = 1;
        swapchain_info.imageUsage = VK_IMAGE_USAGE_STORAGE_BIT;
        swapchain_info.imageSharingMode = VK_SHARING_MODE_EXCLUSIVE;
        swapchain_info.preTransform = capabilities.currentTransform;
        swapchain_info.compositeAlpha = (capabilities.supportedCompositeAlpha &
            VK_COMPOSITE_ALPHA_OPAQUE_BIT_KHR) != 0
            ? VK_COMPOSITE_ALPHA_OPAQUE_BIT_KHR
            : VK_COMPOSITE_ALPHA_INHERIT_BIT_KHR;
        swapchain_info.presentMode = VK_PRESENT_MODE_FIFO_KHR;
        swapchain_info.clipped = VK_TRUE;
        if (device_->vkCreateSwapchainKHR(
                device_->vkdevice(), &swapchain_info, nullptr, &swapchain_) != VK_SUCCESS) {
            *reason = "HDR-capable Vulkan swapchain creation failed";
            return false;
        }
        uint32_t swapchain_image_count = 0;
        device_->vkGetSwapchainImagesKHR(
            device_->vkdevice(), swapchain_, &swapchain_image_count, nullptr);
        swapchain_images_.resize(swapchain_image_count);
        if (swapchain_image_count == 0 || device_->vkGetSwapchainImagesKHR(
                device_->vkdevice(), swapchain_, &swapchain_image_count,
                swapchain_images_.data()) != VK_SUCCESS) {
            *reason = "Vulkan output swapchain returned no images";
            return false;
        }
        swapchain_views_.resize(swapchain_image_count, VK_NULL_HANDLE);
        swapchain_layouts_.resize(swapchain_image_count, VK_IMAGE_LAYOUT_UNDEFINED);
        for (size_t index = 0; index < swapchain_images_.size(); ++index) {
            VkImageViewCreateInfo view_info{};
            view_info.sType = VK_STRUCTURE_TYPE_IMAGE_VIEW_CREATE_INFO;
            view_info.image = swapchain_images_[index];
            view_info.viewType = VK_IMAGE_VIEW_TYPE_2D;
            view_info.format = swapchain_format_;
            view_info.subresourceRange.aspectMask = VK_IMAGE_ASPECT_COLOR_BIT;
            view_info.subresourceRange.levelCount = 1;
            view_info.subresourceRange.layerCount = 1;
            if (ncnn::vkCreateImageView(
                    device_->vkdevice(), &view_info, nullptr,
                    &swapchain_views_[index]) != VK_SUCCESS) {
                *reason = "Vulkan output swapchain image-view creation failed";
                return false;
            }
        }
        return true;
    }

    void cleanup_import(VkImageView view, VkImage image, VkDeviceMemory memory) {
        ncnn::vkDestroyImageView(device_->vkdevice(), view, nullptr);
        ncnn::vkDestroyImage(device_->vkdevice(), image, nullptr);
        ncnn::vkFreeMemory(device_->vkdevice(), memory, nullptr);
    }

    bool gpu_instance_created_ = false;
    const ncnn::VulkanDevice* device_ = nullptr;
    PFN_vkGetAndroidHardwareBufferPropertiesANDROID get_ahb_properties_ = nullptr;
    VkSurfaceKHR output_surface_ = VK_NULL_HANDLE;
    VkSwapchainKHR swapchain_ = VK_NULL_HANDLE;
    VkFormat swapchain_format_ = VK_FORMAT_UNDEFINED;
    VkColorSpaceKHR swapchain_color_space_ = VK_COLOR_SPACE_SRGB_NONLINEAR_KHR;
    VkExtent2D swapchain_extent_{};
    uint32_t queue_family_ = 0;
    std::vector<VkImage> swapchain_images_;
    std::vector<VkImageView> swapchain_views_;
    std::vector<VkImageLayout> swapchain_layouts_;
    VkImage previous_image_ = VK_NULL_HANDLE;
    VkImageView previous_image_view_ = VK_NULL_HANDLE;
    VkDeviceMemory previous_memory_ = VK_NULL_HANDLE;
    ncnn::VkImageMemory previous_image_memory_{};
    int previous_width_ = 0;
    int previous_height_ = 0;
    ncnn::VkAllocator* blob_allocator_ = nullptr;
    ncnn::VkAllocator* staging_allocator_ = nullptr;
    ncnn::Option options_;
    std::vector<std::string> model_load_attempts_;
    std::unique_ptr<ncnn::Net> network_;
    std::unique_ptr<ncnn::Pipeline> proxy_pipeline_;
    std::unique_ptr<ncnn::Pipeline> composite_pipeline_;
    ncnn::VkMat previous_proxy_;
    int proxy_width_ = 0;
    int proxy_height_ = 0;
    AndroidNcnnFrameProcessorSnapshot snapshot_;
};

AndroidNcnnFrameProcessor::AndroidNcnnFrameProcessor()
    : impl_(std::make_unique<Impl>()) {}

AndroidNcnnFrameProcessor::~AndroidNcnnFrameProcessor() {
    destroy();
}

bool AndroidNcnnFrameProcessor::create(
    ANativeWindow* output_window,
    const char* model_param_path,
    const char* model_bin_path,
    std::string* reason) {
    return impl_->create(output_window, model_param_path, model_bin_path, reason);
}

void AndroidNcnnFrameProcessor::destroy() {
    if (impl_)
        impl_->destroy();
}

bool AndroidNcnnFrameProcessor::process(
    AHardwareBuffer* hardware_buffer,
    std::string* reason) {
    return impl_->process(hardware_buffer, reason);
}

AndroidNcnnFrameProcessorSnapshot AndroidNcnnFrameProcessor::snapshot() const {
    return impl_->snapshot();
}

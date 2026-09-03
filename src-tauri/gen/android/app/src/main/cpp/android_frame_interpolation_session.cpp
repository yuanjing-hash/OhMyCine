// SPDX-License-Identifier: MIT

#include "android_frame_interpolation_session.h"
#include "android_ncnn_frame_processor.h"

#include <android/api-level.h>
#include <android/hardware_buffer.h>
#include <media/NdkImage.h>
#define VK_USE_PLATFORM_ANDROID_KHR 1
#include <vulkan/vulkan.h>

#include <algorithm>
#include <array>
#include <cstring>
#include <limits>
#include <poll.h>
#include <utility>
#include <vector>
#include <unistd.h>

namespace {

[[maybe_unused]] bool has_extension(
    const std::vector<VkExtensionProperties>& extensions,
    const char* name) {
    return std::any_of(extensions.begin(), extensions.end(), [name](const auto& extension) {
        return std::strcmp(extension.extensionName, name) == 0;
    });
}

[[maybe_unused]] uint32_t find_memory_type(
    VkPhysicalDevice physical_device,
    uint32_t allowed_types) {
    VkPhysicalDeviceMemoryProperties memory_properties{};
    vkGetPhysicalDeviceMemoryProperties(physical_device, &memory_properties);
    for (uint32_t index = 0; index < memory_properties.memoryTypeCount; ++index) {
        if ((allowed_types & (1U << index)) != 0)
            return index;
    }
    return std::numeric_limits<uint32_t>::max();
}

}  // namespace

class AndroidFrameInterpolationSession::VulkanImporter final {
public:
    ~VulkanImporter() {
        destroy();
    }

    bool create(
        ANativeWindow* output_window,
        const char* model_param_path,
        const char* model_bin_path,
        std::string* reason) {
        destroy();
        if (!frame_processor_.create(
                output_window, model_param_path, model_bin_path, reason))
            return false;
        // The decoder import, proxy, inference, composite and swapchain all
        // live on ncnn's VulkanDevice. Do not create a second logical device:
        // flow/mask buffers cannot be consumed safely across devices.
        output_surface_ready_ = true;
        return true;
#if 0
        const std::array<const char*, 2> instance_extensions{
            VK_KHR_SURFACE_EXTENSION_NAME,
            VK_KHR_ANDROID_SURFACE_EXTENSION_NAME,
        };
        VkApplicationInfo application_info{};
        application_info.sType = VK_STRUCTURE_TYPE_APPLICATION_INFO;
        application_info.pApplicationName = "OhMyCine Android frame interpolation";
        application_info.apiVersion = VK_API_VERSION_1_1;
        VkInstanceCreateInfo instance_info{};
        instance_info.sType = VK_STRUCTURE_TYPE_INSTANCE_CREATE_INFO;
        instance_info.pApplicationInfo = &application_info;
        instance_info.enabledExtensionCount = instance_extensions.size();
        instance_info.ppEnabledExtensionNames = instance_extensions.data();
        if (vkCreateInstance(&instance_info, nullptr, &instance_) != VK_SUCCESS) {
            *reason = "Vulkan 1.1 session instance creation failed";
            return false;
        }

        VkAndroidSurfaceCreateInfoKHR surface_info{};
        surface_info.sType = VK_STRUCTURE_TYPE_ANDROID_SURFACE_CREATE_INFO_KHR;
        surface_info.window = output_window;
        if (vkCreateAndroidSurfaceKHR(instance_, &surface_info, nullptr, &surface_) != VK_SUCCESS) {
            *reason = "Vulkan output Surface creation failed";
            return false;
        }

        uint32_t physical_device_count = 0;
        if (vkEnumeratePhysicalDevices(instance_, &physical_device_count, nullptr) != VK_SUCCESS ||
            physical_device_count == 0) {
            *reason = "No Vulkan physical device is available";
            return false;
        }
        std::vector<VkPhysicalDevice> physical_devices(physical_device_count);
        vkEnumeratePhysicalDevices(instance_, &physical_device_count, physical_devices.data());
        for (VkPhysicalDevice candidate : physical_devices) {
            uint32_t extension_count = 0;
            vkEnumerateDeviceExtensionProperties(candidate, nullptr, &extension_count, nullptr);
            std::vector<VkExtensionProperties> extensions(extension_count);
            vkEnumerateDeviceExtensionProperties(
                candidate, nullptr, &extension_count, extensions.data());
            if (!has_extension(
                    extensions,
                    VK_ANDROID_EXTERNAL_MEMORY_ANDROID_HARDWARE_BUFFER_EXTENSION_NAME) ||
                !has_extension(extensions, VK_KHR_EXTERNAL_SEMAPHORE_FD_EXTENSION_NAME) ||
                !has_extension(extensions, VK_KHR_SWAPCHAIN_EXTENSION_NAME) ||
                !has_extension(extensions, VK_EXT_QUEUE_FAMILY_FOREIGN_EXTENSION_NAME)) {
                continue;
            }
            uint32_t queue_count = 0;
            vkGetPhysicalDeviceQueueFamilyProperties(candidate, &queue_count, nullptr);
            std::vector<VkQueueFamilyProperties> queue_properties(queue_count);
            vkGetPhysicalDeviceQueueFamilyProperties(
                candidate, &queue_count, queue_properties.data());
            for (uint32_t queue_index = 0; queue_index < queue_count; ++queue_index) {
                VkBool32 can_present = VK_FALSE;
                vkGetPhysicalDeviceSurfaceSupportKHR(
                    candidate, queue_index, surface_, &can_present);
                if (queue_properties[queue_index].queueCount == 0 ||
                    (queue_properties[queue_index].queueFlags & VK_QUEUE_GRAPHICS_BIT) == 0 ||
                    can_present != VK_TRUE) {
                    continue;
                }
                physical_device_ = candidate;
                queue_family_ = queue_index;
                break;
            }
            if (physical_device_ != VK_NULL_HANDLE)
                break;
        }
        if (physical_device_ == VK_NULL_HANDLE) {
            *reason = "No same-device Vulkan AHardwareBuffer/present queue is available";
            return false;
        }

        float priority = 1.0F;
        VkDeviceQueueCreateInfo queue_info{};
        queue_info.sType = VK_STRUCTURE_TYPE_DEVICE_QUEUE_CREATE_INFO;
        queue_info.queueFamilyIndex = queue_family_;
        queue_info.queueCount = 1;
        queue_info.pQueuePriorities = &priority;
        const std::array<const char*, 4> device_extensions{
            VK_ANDROID_EXTERNAL_MEMORY_ANDROID_HARDWARE_BUFFER_EXTENSION_NAME,
            VK_KHR_EXTERNAL_SEMAPHORE_FD_EXTENSION_NAME,
            VK_KHR_SWAPCHAIN_EXTENSION_NAME,
            VK_EXT_QUEUE_FAMILY_FOREIGN_EXTENSION_NAME,
        };
        VkDeviceCreateInfo device_info{};
        device_info.sType = VK_STRUCTURE_TYPE_DEVICE_CREATE_INFO;
        device_info.queueCreateInfoCount = 1;
        device_info.pQueueCreateInfos = &queue_info;
        device_info.enabledExtensionCount = device_extensions.size();
        device_info.ppEnabledExtensionNames = device_extensions.data();
        if (vkCreateDevice(physical_device_, &device_info, nullptr, &device_) != VK_SUCCESS) {
            *reason = "Vulkan frame session device creation failed";
            return false;
        }
        vkGetDeviceQueue(device_, queue_family_, 0, &queue_);
        get_ahb_properties_ =
            reinterpret_cast<PFN_vkGetAndroidHardwareBufferPropertiesANDROID>(
                vkGetDeviceProcAddr(device_, "vkGetAndroidHardwareBufferPropertiesANDROID"));
        import_semaphore_fd_ = reinterpret_cast<PFN_vkImportSemaphoreFdKHR>(
            vkGetDeviceProcAddr(device_, "vkImportSemaphoreFdKHR"));
        if (get_ahb_properties_ == nullptr || import_semaphore_fd_ == nullptr) {
            *reason = "Required Vulkan Android interop entry points are unavailable";
            return false;
        }

        VkCommandPoolCreateInfo pool_info{};
        pool_info.sType = VK_STRUCTURE_TYPE_COMMAND_POOL_CREATE_INFO;
        pool_info.flags = VK_COMMAND_POOL_CREATE_TRANSIENT_BIT |
                          VK_COMMAND_POOL_CREATE_RESET_COMMAND_BUFFER_BIT;
        pool_info.queueFamilyIndex = queue_family_;
        if (vkCreateCommandPool(device_, &pool_info, nullptr, &command_pool_) != VK_SUCCESS) {
            *reason = "Vulkan frame session command pool creation failed";
            return false;
        }
        uint32_t surface_format_count = 0;
        const VkResult formats_status = vkGetPhysicalDeviceSurfaceFormatsKHR(
            physical_device_, surface_, &surface_format_count, nullptr);
        if (formats_status != VK_SUCCESS || surface_format_count == 0) {
            *reason = "The output Surface exposes no Vulkan presentation format";
            return false;
        }
        output_surface_ready_ = true;
        return true;
#endif
    }

    bool import_and_wait(AndroidFrameSourceImage source, int64_t* timestamp_ns) {
        if (source.image == nullptr)
            return false;
        struct ImageCleanup final {
            AndroidFrameSourceImage source;
            ~ImageCleanup() {
                if (source.acquire_fence_fd >= 0)
                    close(source.acquire_fence_fd);
                if (source.image != nullptr)
                    AImage_delete(source.image);
            }
        } cleanup{source};

        if (source.acquire_fence_fd >= 0) {
            pollfd fence_poll{source.acquire_fence_fd, POLLIN, 0};
            if (poll(&fence_poll, 1, 1'000) <= 0)
                return false;
            close(source.acquire_fence_fd);
            cleanup.source.acquire_fence_fd = -1;
            source.acquire_fence_fd = -1;
        }

        AHardwareBuffer* hardware_buffer = nullptr;
        if (AImage_getHardwareBuffer(source.image, &hardware_buffer) != AMEDIA_OK ||
            hardware_buffer == nullptr ||
            AImage_getTimestamp(source.image, timestamp_ns) != AMEDIA_OK) {
            return false;
        }
        std::string processor_reason;
        if (!frame_processor_.process(hardware_buffer, &processor_reason)) {
            last_processor_reason_ = std::move(processor_reason);
            return false;
        }
        return true;
#if 0
        AHardwareBuffer_Desc description{};
        AHardwareBuffer_describe(hardware_buffer, &description);
        if (description.format != AHARDWAREBUFFER_FORMAT_R16G16B16A16_FLOAT)
            return false;

        VkAndroidHardwareBufferFormatPropertiesANDROID format_properties{};
        format_properties.sType =
            VK_STRUCTURE_TYPE_ANDROID_HARDWARE_BUFFER_FORMAT_PROPERTIES_ANDROID;
        VkAndroidHardwareBufferPropertiesANDROID buffer_properties{};
        buffer_properties.sType = VK_STRUCTURE_TYPE_ANDROID_HARDWARE_BUFFER_PROPERTIES_ANDROID;
        buffer_properties.pNext = &format_properties;
        if (get_ahb_properties_(device_, hardware_buffer, &buffer_properties) != VK_SUCCESS ||
            buffer_properties.allocationSize == 0 ||
            format_properties.format != VK_FORMAT_R16G16B16A16_SFLOAT) {
            return false;
        }

        VkExternalMemoryImageCreateInfo external_image_info{};
        external_image_info.sType = VK_STRUCTURE_TYPE_EXTERNAL_MEMORY_IMAGE_CREATE_INFO;
        external_image_info.handleTypes =
            VK_EXTERNAL_MEMORY_HANDLE_TYPE_ANDROID_HARDWARE_BUFFER_BIT_ANDROID;
        VkImageCreateInfo image_info{};
        image_info.sType = VK_STRUCTURE_TYPE_IMAGE_CREATE_INFO;
        image_info.pNext = &external_image_info;
        image_info.imageType = VK_IMAGE_TYPE_2D;
        image_info.format = format_properties.format;
        image_info.extent = {description.width, description.height, 1};
        image_info.mipLevels = 1;
        image_info.arrayLayers = description.layers;
        image_info.samples = VK_SAMPLE_COUNT_1_BIT;
        image_info.tiling = VK_IMAGE_TILING_OPTIMAL;
        image_info.usage = VK_IMAGE_USAGE_SAMPLED_BIT | VK_IMAGE_USAGE_TRANSFER_SRC_BIT;
        image_info.sharingMode = VK_SHARING_MODE_EXCLUSIVE;
        image_info.initialLayout = VK_IMAGE_LAYOUT_UNDEFINED;
        VkImage image = VK_NULL_HANDLE;
        if (vkCreateImage(device_, &image_info, nullptr, &image) != VK_SUCCESS)
            return false;

        VkMemoryRequirements memory_requirements{};
        vkGetImageMemoryRequirements(device_, image, &memory_requirements);
        const uint32_t memory_type = find_memory_type(
            physical_device_,
            memory_requirements.memoryTypeBits & buffer_properties.memoryTypeBits);
        if (memory_type == std::numeric_limits<uint32_t>::max()) {
            vkDestroyImage(device_, image, nullptr);
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
        if (vkAllocateMemory(device_, &allocation_info, nullptr, &memory) != VK_SUCCESS ||
            vkBindImageMemory(device_, image, memory, 0) != VK_SUCCESS) {
            if (memory != VK_NULL_HANDLE)
                vkFreeMemory(device_, memory, nullptr);
            vkDestroyImage(device_, image, nullptr);
            return false;
        }

        VkCommandBufferAllocateInfo command_allocate{};
        command_allocate.sType = VK_STRUCTURE_TYPE_COMMAND_BUFFER_ALLOCATE_INFO;
        command_allocate.commandPool = command_pool_;
        command_allocate.level = VK_COMMAND_BUFFER_LEVEL_PRIMARY;
        command_allocate.commandBufferCount = 1;
        VkCommandBuffer command = VK_NULL_HANDLE;
        if (vkAllocateCommandBuffers(device_, &command_allocate, &command) != VK_SUCCESS) {
            vkFreeMemory(device_, memory, nullptr);
            vkDestroyImage(device_, image, nullptr);
            return false;
        }
        VkCommandBufferBeginInfo begin_info{};
        begin_info.sType = VK_STRUCTURE_TYPE_COMMAND_BUFFER_BEGIN_INFO;
        begin_info.flags = VK_COMMAND_BUFFER_USAGE_ONE_TIME_SUBMIT_BIT;
        vkBeginCommandBuffer(command, &begin_info);
        VkImageMemoryBarrier acquire_barrier{};
        acquire_barrier.sType = VK_STRUCTURE_TYPE_IMAGE_MEMORY_BARRIER;
        acquire_barrier.srcAccessMask = VK_ACCESS_MEMORY_WRITE_BIT;
        acquire_barrier.dstAccessMask = VK_ACCESS_SHADER_READ_BIT;
        acquire_barrier.oldLayout = VK_IMAGE_LAYOUT_GENERAL;
        acquire_barrier.newLayout = VK_IMAGE_LAYOUT_SHADER_READ_ONLY_OPTIMAL;
        acquire_barrier.srcQueueFamilyIndex = VK_QUEUE_FAMILY_FOREIGN_EXT;
        acquire_barrier.dstQueueFamilyIndex = queue_family_;
        acquire_barrier.image = image;
        acquire_barrier.subresourceRange.aspectMask = VK_IMAGE_ASPECT_COLOR_BIT;
        acquire_barrier.subresourceRange.levelCount = 1;
        acquire_barrier.subresourceRange.layerCount = 1;
        vkCmdPipelineBarrier(
            command,
            VK_PIPELINE_STAGE_ALL_COMMANDS_BIT,
            VK_PIPELINE_STAGE_COMPUTE_SHADER_BIT,
            0,
            0,
            nullptr,
            0,
            nullptr,
            1,
            &acquire_barrier);
        VkImageMemoryBarrier release_barrier = acquire_barrier;
        release_barrier.srcAccessMask = VK_ACCESS_SHADER_READ_BIT;
        release_barrier.dstAccessMask = 0;
        release_barrier.oldLayout = VK_IMAGE_LAYOUT_SHADER_READ_ONLY_OPTIMAL;
        release_barrier.newLayout = VK_IMAGE_LAYOUT_GENERAL;
        release_barrier.srcQueueFamilyIndex = queue_family_;
        release_barrier.dstQueueFamilyIndex = VK_QUEUE_FAMILY_FOREIGN_EXT;
        vkCmdPipelineBarrier(
            command,
            VK_PIPELINE_STAGE_COMPUTE_SHADER_BIT,
            VK_PIPELINE_STAGE_BOTTOM_OF_PIPE_BIT,
            0,
            0,
            nullptr,
            0,
            nullptr,
            1,
            &release_barrier);
        if (vkEndCommandBuffer(command) != VK_SUCCESS) {
            vkFreeCommandBuffers(device_, command_pool_, 1, &command);
            vkFreeMemory(device_, memory, nullptr);
            vkDestroyImage(device_, image, nullptr);
            return false;
        }
        VkSubmitInfo submit_info{};
        submit_info.sType = VK_STRUCTURE_TYPE_SUBMIT_INFO;
        submit_info.commandBufferCount = 1;
        submit_info.pCommandBuffers = &command;
        VkFenceCreateInfo fence_info{};
        fence_info.sType = VK_STRUCTURE_TYPE_FENCE_CREATE_INFO;
        VkFence completion_fence = VK_NULL_HANDLE;
        const bool submitted =
            vkCreateFence(device_, &fence_info, nullptr, &completion_fence) == VK_SUCCESS &&
            vkQueueSubmit(queue_, 1, &submit_info, completion_fence) == VK_SUCCESS &&
            vkWaitForFences(
                device_, 1, &completion_fence, VK_TRUE, 1'000'000'000ULL) == VK_SUCCESS;
        if (completion_fence != VK_NULL_HANDLE)
            vkDestroyFence(device_, completion_fence, nullptr);
        vkFreeCommandBuffers(device_, command_pool_, 1, &command);
        vkFreeMemory(device_, memory, nullptr);
        vkDestroyImage(device_, image, nullptr);
        return submitted;
#endif
    }

    bool output_surface_ready() const {
        return output_surface_ready_;
    }

    AndroidNcnnFrameProcessorSnapshot processor_snapshot() const {
        return frame_processor_.snapshot();
    }

    const std::string& failure_reason() const {
        return last_processor_reason_;
    }

private:
    void destroy() {
        frame_processor_.destroy();
        if (device_ != VK_NULL_HANDLE)
            vkDeviceWaitIdle(device_);
        if (command_pool_ != VK_NULL_HANDLE)
            vkDestroyCommandPool(device_, command_pool_, nullptr);
        if (device_ != VK_NULL_HANDLE)
            vkDestroyDevice(device_, nullptr);
        if (surface_ != VK_NULL_HANDLE)
            vkDestroySurfaceKHR(instance_, surface_, nullptr);
        if (instance_ != VK_NULL_HANDLE)
            vkDestroyInstance(instance_, nullptr);
        instance_ = VK_NULL_HANDLE;
        physical_device_ = VK_NULL_HANDLE;
        device_ = VK_NULL_HANDLE;
        queue_ = VK_NULL_HANDLE;
        command_pool_ = VK_NULL_HANDLE;
        surface_ = VK_NULL_HANDLE;
        get_ahb_properties_ = nullptr;
        import_semaphore_fd_ = nullptr;
        output_surface_ready_ = false;
    }

    VkInstance instance_ = VK_NULL_HANDLE;
    VkPhysicalDevice physical_device_ = VK_NULL_HANDLE;
    VkDevice device_ = VK_NULL_HANDLE;
    VkQueue queue_ = VK_NULL_HANDLE;
    VkCommandPool command_pool_ = VK_NULL_HANDLE;
    VkSurfaceKHR surface_ = VK_NULL_HANDLE;
    [[maybe_unused]] uint32_t queue_family_ = 0;
    PFN_vkGetAndroidHardwareBufferPropertiesANDROID get_ahb_properties_ = nullptr;
    PFN_vkImportSemaphoreFdKHR import_semaphore_fd_ = nullptr;
    bool output_surface_ready_ = false;
    AndroidNcnnFrameProcessor frame_processor_;
    std::string last_processor_reason_;
};

AndroidFrameInterpolationSession::AndroidFrameInterpolationSession() = default;

AndroidFrameInterpolationSession::~AndroidFrameInterpolationSession() {
    stop();
}

bool AndroidFrameInterpolationSession::prepare(
    ANativeWindow* output_window,
    int width,
    int height,
    int data_space,
    uint64_t generation,
    const char* model_param_path,
    const char* model_bin_path) {
    stop();
    if (android_get_device_api_level() < 29 || output_window == nullptr ||
        width <= 0 || height <= 0) {
        record_failure("Invalid Android frame session parameters");
        return false;
    }
    ANativeWindow_acquire(output_window);
    output_window_ = output_window;
    auto importer = std::make_unique<VulkanImporter>();
    std::string reason;
    if (!importer->create(output_window, model_param_path, model_bin_path, &reason)) {
        ANativeWindow_release(output_window_);
        output_window_ = nullptr;
        record_failure(reason.c_str());
        return false;
    }
    if (!source_.create(width, height, data_space, generation)) {
        ANativeWindow_release(output_window_);
        output_window_ = nullptr;
        record_failure("RGBA16F decoder input Surface creation failed");
        return false;
    }
    {
        std::lock_guard<std::mutex> lock(mutex_);
        importer_ = std::move(importer);
        generation_ = generation;
        imported_frames_ = 0;
        import_failures_ = 0;
        stale_frames_ = 0;
        latest_timestamp_ns_ = 0;
        prepared_ = true;
        running_ = false;
        output_surface_ready_ = importer_->output_surface_ready();
        first_frame_imported_ = false;
        first_frame_presented_ = false;
        reason_ = "Waiting for two decoded FP16 frames and the first generated present";
    }
    return true;
}

bool AndroidFrameInterpolationSession::start() {
    std::lock_guard<std::mutex> lock(mutex_);
    if (!prepared_ || running_ || importer_ == nullptr)
        return false;
    stop_requested_.store(false);
    running_ = true;
    worker_ = std::thread(&AndroidFrameInterpolationSession::consume_frames, this);
    return true;
}

void AndroidFrameInterpolationSession::stop() {
    stop_requested_.store(true);
    source_.destroy();
    if (worker_.joinable())
        worker_.join();
    std::unique_ptr<VulkanImporter> importer;
    ANativeWindow* output_window = nullptr;
    {
        std::lock_guard<std::mutex> lock(mutex_);
        importer = std::move(importer_);
        output_window = output_window_;
        output_window_ = nullptr;
        prepared_ = false;
        running_ = false;
        output_surface_ready_ = false;
        first_frame_presented_ = false;
    }
    importer.reset();
    if (output_window != nullptr)
        ANativeWindow_release(output_window);
}

ANativeWindow* AndroidFrameInterpolationSession::input_window() const {
    return source_.window();
}

AndroidFrameInterpolationSessionSnapshot AndroidFrameInterpolationSession::snapshot() const {
    const AndroidFrameSourceSnapshot source_snapshot = source_.snapshot();
    std::lock_guard<std::mutex> lock(mutex_);
    const AndroidNcnnFrameProcessorSnapshot processor_snapshot = importer_ == nullptr
        ? AndroidNcnnFrameProcessorSnapshot{}
        : importer_->processor_snapshot();
    return {
        generation_,
        source_snapshot.acquired_frames,
        source_snapshot.dropped_frames,
        imported_frames_,
        import_failures_,
        stale_frames_,
        processor_snapshot.proxy_frames,
        processor_snapshot.inferred_pairs,
        processor_snapshot.latest_inference_ms,
        latest_timestamp_ns_,
        source_snapshot.queued_frames,
        prepared_,
        running_,
        output_surface_ready_,
        first_frame_imported_,
        first_frame_presented_ || processor_snapshot.presented_frames > 0,
        reason_,
    };
}

void AndroidFrameInterpolationSession::consume_frames() {
    while (!stop_requested_.load()) {
        AndroidFrameSourceImage image = source_.wait_acquire_for_generation(generation_, 50);
        if (image.image == nullptr)
            continue;
        int64_t timestamp_ns = 0;
        bool imported = false;
        {
            std::lock_guard<std::mutex> lock(mutex_);
            if (importer_ != nullptr)
                imported = importer_->import_and_wait(image, &timestamp_ns);
        }
        // import_and_wait always releases the AImage and consumes/closes its
        // acquire fence, including failure paths.
        std::lock_guard<std::mutex> lock(mutex_);
        if (imported) {
            ++imported_frames_;
            if (latest_timestamp_ns_ != 0 && timestamp_ns <= latest_timestamp_ns_) {
                ++stale_frames_;
            } else {
                latest_timestamp_ns_ = timestamp_ns;
            }
            first_frame_imported_ = true;
            if (importer_ != nullptr &&
                importer_->processor_snapshot().presented_frames > 0) {
                first_frame_presented_ = true;
            }
        } else {
            ++import_failures_;
            const std::string processor_reason = importer_ == nullptr
                ? std::string{}
                : importer_->failure_reason();
            reason_ = processor_reason.empty()
                ? "AImage acquire-fence wait or Vulkan AHardwareBuffer import failed"
                : processor_reason;
        }
    }
    std::lock_guard<std::mutex> lock(mutex_);
    running_ = false;
}

void AndroidFrameInterpolationSession::record_failure(const char* reason) {
    std::lock_guard<std::mutex> lock(mutex_);
    reason_ = reason == nullptr ? "Android frame session failed" : reason;
    prepared_ = false;
    running_ = false;
    output_surface_ready_ = false;
    first_frame_imported_ = false;
    first_frame_presented_ = false;
}

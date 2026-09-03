#include <android/hardware_buffer.h>
#include <android/api-level.h>
#include <android/data_space.h>
#include <android/native_window.h>
#include <jni.h>
#include <media/NdkImageReader.h>
#define VK_USE_PLATFORM_ANDROID_KHR 1
#include <vulkan/vulkan.h>

#include <algorithm>
#include <cstdint>
#include <cstring>
#include <sstream>
#include <string>
#include <vector>

#include "android_dataspace_compat.h"

namespace {

struct ProbeResult {
    bool image_reader_fp16 = false;
    bool sdr_dataspace = false;
    bool pq_dataspace = false;
    bool hlg_dataspace = false;
    bool linear_hdr_dataspace = false;
    bool hdr_dataspace = false;
    bool vulkan_11 = false;
    bool ahb_external_memory = false;
    bool fp16_shader = false;
    bool ncnn_vulkan = false;
    bool ncnn_model_loaded = false;
    bool ncnn_inference_self_test = false;
    std::string gpu_name;
    std::string ncnn_diagnostic;
    std::string reason;
};

bool has_extension(const std::vector<VkExtensionProperties>& extensions, const char* name) {
    return std::any_of(extensions.begin(), extensions.end(), [name](const auto& extension) {
        return std::strcmp(extension.extensionName, name) == 0;
    });
}

std::string escape_json(const std::string& value) {
    std::string escaped;
    escaped.reserve(value.size());
    for (const char character : value) {
        if (character == '\\' || character == '"')
            escaped.push_back('\\');
        escaped.push_back(character);
    }
    return escaped;
}

extern "C" int ohmycine_ncnn_probe(
    const char* model_param_path,
    const char* model_bin_path,
    char* gpu_name,
    size_t gpu_name_size,
    char* diagnostic,
    size_t diagnostic_size);

ProbeResult probe(const char* model_param_path, const char* model_bin_path) {
    ProbeResult result;
    if (android_get_device_api_level() < 29) {
        result.reason = "Android API 29 or newer is required";
        return result;
    }

    AImageReader* reader = nullptr;
    media_status_t reader_status = AImageReader_newWithUsage(
        16,
        16,
        AIMAGE_FORMAT_RGBA_FP16,
        AHARDWAREBUFFER_USAGE_GPU_SAMPLED_IMAGE | AHARDWAREBUFFER_USAGE_GPU_COLOR_OUTPUT,
        3,
        &reader);
    if (reader_status == AMEDIA_OK && reader != nullptr) {
        ANativeWindow* window = nullptr;
        result.image_reader_fp16 = AImageReader_getWindow(reader, &window) == AMEDIA_OK && window;
        if (result.image_reader_fp16) {
            result.sdr_dataspace =
                ohmycine::android_compat::set_buffers_dataspace(window, ADATASPACE_SRGB_LINEAR) == 0 &&
                ohmycine::android_compat::get_buffers_dataspace(window) == ADATASPACE_SRGB_LINEAR;
            result.pq_dataspace =
                ohmycine::android_compat::set_buffers_dataspace(window, ADATASPACE_BT2020_PQ) == 0 &&
                ohmycine::android_compat::get_buffers_dataspace(window) == ADATASPACE_BT2020_PQ;
            result.hlg_dataspace =
                ohmycine::android_compat::set_buffers_dataspace(window, ADATASPACE_BT2020_HLG) == 0 &&
                ohmycine::android_compat::get_buffers_dataspace(window) == ADATASPACE_BT2020_HLG;
            result.linear_hdr_dataspace =
                ohmycine::android_compat::set_buffers_dataspace(window, ADATASPACE_SCRGB_LINEAR) == 0 &&
                ohmycine::android_compat::get_buffers_dataspace(window) == ADATASPACE_SCRGB_LINEAR;
            // HDR10+/Dolby Vision are mapped by mpv/libplacebo before frame
            // synthesis, so a verified PQ, HLG, or linear-HDR carrier is
            // sufficient. SDR has an independent linear-FP16 gate.
            result.hdr_dataspace = result.pq_dataspace || result.hlg_dataspace ||
                                   result.linear_hdr_dataspace;
            ohmycine::android_compat::set_buffers_dataspace(window, ADATASPACE_UNKNOWN);
        }
    }

    AHardwareBuffer* hardware_buffer = nullptr;
    AHardwareBuffer_Desc buffer_desc{};
    buffer_desc.width = 16;
    buffer_desc.height = 16;
    buffer_desc.layers = 1;
    buffer_desc.format = AHARDWAREBUFFER_FORMAT_R16G16B16A16_FLOAT;
    buffer_desc.usage = AHARDWAREBUFFER_USAGE_GPU_SAMPLED_IMAGE |
                        AHARDWAREBUFFER_USAGE_GPU_COLOR_OUTPUT;
    if (AHardwareBuffer_allocate(&buffer_desc, &hardware_buffer) != 0) {
        result.reason = "AHardwareBuffer RGBA16F allocation failed";
        if (reader)
            AImageReader_delete(reader);
        return result;
    }

    uint32_t instance_version = VK_API_VERSION_1_0;
    if (auto enumerate_version = reinterpret_cast<PFN_vkEnumerateInstanceVersion>(
            vkGetInstanceProcAddr(VK_NULL_HANDLE, "vkEnumerateInstanceVersion"))) {
        enumerate_version(&instance_version);
    }
    result.vulkan_11 = instance_version >= VK_API_VERSION_1_1;

    VkApplicationInfo app_info{};
    app_info.sType = VK_STRUCTURE_TYPE_APPLICATION_INFO;
    app_info.pApplicationName = "OhMyCine frame interpolation probe";
    app_info.apiVersion = VK_API_VERSION_1_1;
    VkInstanceCreateInfo instance_info{};
    instance_info.sType = VK_STRUCTURE_TYPE_INSTANCE_CREATE_INFO;
    instance_info.pApplicationInfo = &app_info;
    VkInstance instance = VK_NULL_HANDLE;
    if (vkCreateInstance(&instance_info, nullptr, &instance) != VK_SUCCESS) {
        result.reason = "Vulkan 1.1 instance creation failed";
        AHardwareBuffer_release(hardware_buffer);
        if (reader)
            AImageReader_delete(reader);
        return result;
    }

    uint32_t physical_device_count = 0;
    vkEnumeratePhysicalDevices(instance, &physical_device_count, nullptr);
    std::vector<VkPhysicalDevice> physical_devices(physical_device_count);
    vkEnumeratePhysicalDevices(instance, &physical_device_count, physical_devices.data());
    const auto get_physical_device_features2 =
        reinterpret_cast<PFN_vkGetPhysicalDeviceFeatures2>(
            vkGetInstanceProcAddr(instance, "vkGetPhysicalDeviceFeatures2"));

    for (const VkPhysicalDevice physical_device : physical_devices) {
        if (!get_physical_device_features2)
            break;
        uint32_t extension_count = 0;
        vkEnumerateDeviceExtensionProperties(physical_device, nullptr, &extension_count, nullptr);
        std::vector<VkExtensionProperties> extensions(extension_count);
        vkEnumerateDeviceExtensionProperties(
            physical_device, nullptr, &extension_count, extensions.data());
        if (!has_extension(extensions,
                           VK_ANDROID_EXTERNAL_MEMORY_ANDROID_HARDWARE_BUFFER_EXTENSION_NAME))
            continue;

        VkPhysicalDevice16BitStorageFeatures storage16{};
        storage16.sType = VK_STRUCTURE_TYPE_PHYSICAL_DEVICE_16BIT_STORAGE_FEATURES;
        VkPhysicalDeviceShaderFloat16Int8Features shader16{};
        shader16.sType = VK_STRUCTURE_TYPE_PHYSICAL_DEVICE_SHADER_FLOAT16_INT8_FEATURES;
        storage16.pNext = &shader16;
        VkPhysicalDeviceFeatures2 features{};
        features.sType = VK_STRUCTURE_TYPE_PHYSICAL_DEVICE_FEATURES_2;
        features.pNext = &storage16;
        get_physical_device_features2(physical_device, &features);
        if (!storage16.storageBuffer16BitAccess || !shader16.shaderFloat16)
            continue;

        uint32_t queue_count = 0;
        vkGetPhysicalDeviceQueueFamilyProperties(physical_device, &queue_count, nullptr);
        std::vector<VkQueueFamilyProperties> queues(queue_count);
        vkGetPhysicalDeviceQueueFamilyProperties(physical_device, &queue_count, queues.data());
        auto queue = std::find_if(queues.begin(), queues.end(), [](const auto& properties) {
            return properties.queueCount > 0 && (properties.queueFlags & VK_QUEUE_COMPUTE_BIT);
        });
        if (queue == queues.end())
            continue;

        float priority = 1.0F;
        VkDeviceQueueCreateInfo queue_info{};
        queue_info.sType = VK_STRUCTURE_TYPE_DEVICE_QUEUE_CREATE_INFO;
        queue_info.queueFamilyIndex = static_cast<uint32_t>(queue - queues.begin());
        queue_info.queueCount = 1;
        queue_info.pQueuePriorities = &priority;
        const char* required_extensions[] = {
            VK_ANDROID_EXTERNAL_MEMORY_ANDROID_HARDWARE_BUFFER_EXTENSION_NAME,
        };
        VkDeviceCreateInfo device_info{};
        device_info.sType = VK_STRUCTURE_TYPE_DEVICE_CREATE_INFO;
        device_info.pNext = &storage16;
        device_info.queueCreateInfoCount = 1;
        device_info.pQueueCreateInfos = &queue_info;
        device_info.enabledExtensionCount = 1;
        device_info.ppEnabledExtensionNames = required_extensions;
        VkDevice device = VK_NULL_HANDLE;
        if (vkCreateDevice(physical_device, &device_info, nullptr, &device) != VK_SUCCESS)
            continue;

        auto get_ahb_properties = reinterpret_cast<PFN_vkGetAndroidHardwareBufferPropertiesANDROID>(
            vkGetDeviceProcAddr(device, "vkGetAndroidHardwareBufferPropertiesANDROID"));
        VkAndroidHardwareBufferFormatPropertiesANDROID format_properties{};
        format_properties.sType = VK_STRUCTURE_TYPE_ANDROID_HARDWARE_BUFFER_FORMAT_PROPERTIES_ANDROID;
        VkAndroidHardwareBufferPropertiesANDROID properties{};
        properties.sType = VK_STRUCTURE_TYPE_ANDROID_HARDWARE_BUFFER_PROPERTIES_ANDROID;
        properties.pNext = &format_properties;
        if (get_ahb_properties &&
            get_ahb_properties(device, hardware_buffer, &properties) == VK_SUCCESS &&
            properties.allocationSize > 0 && properties.memoryTypeBits != 0) {
            VkExternalMemoryImageCreateInfo external_image{};
            external_image.sType = VK_STRUCTURE_TYPE_EXTERNAL_MEMORY_IMAGE_CREATE_INFO;
            external_image.handleTypes =
                VK_EXTERNAL_MEMORY_HANDLE_TYPE_ANDROID_HARDWARE_BUFFER_BIT_ANDROID;
            VkImageCreateInfo image_info{};
            image_info.sType = VK_STRUCTURE_TYPE_IMAGE_CREATE_INFO;
            image_info.pNext = &external_image;
            image_info.imageType = VK_IMAGE_TYPE_2D;
            image_info.format = VK_FORMAT_R16G16B16A16_SFLOAT;
            image_info.extent = {buffer_desc.width, buffer_desc.height, 1};
            image_info.mipLevels = 1;
            image_info.arrayLayers = 1;
            image_info.samples = VK_SAMPLE_COUNT_1_BIT;
            image_info.tiling = VK_IMAGE_TILING_OPTIMAL;
            image_info.usage = VK_IMAGE_USAGE_SAMPLED_BIT | VK_IMAGE_USAGE_STORAGE_BIT |
                               VK_IMAGE_USAGE_COLOR_ATTACHMENT_BIT;
            image_info.sharingMode = VK_SHARING_MODE_EXCLUSIVE;
            image_info.initialLayout = VK_IMAGE_LAYOUT_UNDEFINED;
            VkImage imported_image = VK_NULL_HANDLE;
            VkDeviceMemory imported_memory = VK_NULL_HANDLE;
            if (vkCreateImage(device, &image_info, nullptr, &imported_image) == VK_SUCCESS) {
                VkMemoryRequirements memory_requirements{};
                vkGetImageMemoryRequirements(device, imported_image, &memory_requirements);
                VkPhysicalDeviceMemoryProperties memory_properties{};
                vkGetPhysicalDeviceMemoryProperties(physical_device, &memory_properties);
                const uint32_t compatible_types =
                    properties.memoryTypeBits & memory_requirements.memoryTypeBits;
                uint32_t memory_type = UINT32_MAX;
                for (uint32_t index = 0; index < memory_properties.memoryTypeCount; ++index) {
                    if ((compatible_types & (1U << index)) != 0) {
                        memory_type = index;
                        break;
                    }
                }
                if (memory_type != UINT32_MAX) {
                    VkImportAndroidHardwareBufferInfoANDROID import_info{};
                    import_info.sType =
                        VK_STRUCTURE_TYPE_IMPORT_ANDROID_HARDWARE_BUFFER_INFO_ANDROID;
                    import_info.buffer = hardware_buffer;
                    VkMemoryDedicatedAllocateInfo dedicated_info{};
                    dedicated_info.sType = VK_STRUCTURE_TYPE_MEMORY_DEDICATED_ALLOCATE_INFO;
                    dedicated_info.pNext = &import_info;
                    dedicated_info.image = imported_image;
                    VkMemoryAllocateInfo allocation_info{};
                    allocation_info.sType = VK_STRUCTURE_TYPE_MEMORY_ALLOCATE_INFO;
                    allocation_info.pNext = &dedicated_info;
                    allocation_info.allocationSize = properties.allocationSize;
                    allocation_info.memoryTypeIndex = memory_type;
                    if (vkAllocateMemory(device, &allocation_info, nullptr, &imported_memory) ==
                            VK_SUCCESS &&
                        vkBindImageMemory(device, imported_image, imported_memory, 0) == VK_SUCCESS) {
                        VkPhysicalDeviceProperties device_properties{};
                        vkGetPhysicalDeviceProperties(physical_device, &device_properties);
                        result.gpu_name = device_properties.deviceName;
                        result.fp16_shader = true;
                        result.ahb_external_memory = true;
                    }
                }
            }
            if (imported_memory != VK_NULL_HANDLE)
                vkFreeMemory(device, imported_memory, nullptr);
            if (imported_image != VK_NULL_HANDLE)
                vkDestroyImage(device, imported_image, nullptr);
            if (result.ahb_external_memory) {
                vkDestroyDevice(device, nullptr);
                break;
            }
        }
        vkDestroyDevice(device, nullptr);
    }

    char ncnn_gpu_name[VK_MAX_PHYSICAL_DEVICE_NAME_SIZE]{};
    char ncnn_diagnostic[768]{};
    const int ncnn_status = ohmycine_ncnn_probe(
        model_param_path,
        model_bin_path,
        ncnn_gpu_name,
        sizeof(ncnn_gpu_name),
        ncnn_diagnostic,
        sizeof(ncnn_diagnostic));
    result.ncnn_vulkan = ncnn_status >= 1;
    result.ncnn_model_loaded = ncnn_status >= 2;
    result.ncnn_inference_self_test = ncnn_status >= 3;
    if (result.gpu_name.empty() && ncnn_gpu_name[0] != '\0')
        result.gpu_name = ncnn_gpu_name;
    if (ncnn_diagnostic[0] != '\0')
        result.ncnn_diagnostic = ncnn_diagnostic;

    if (!result.image_reader_fp16)
        result.reason = "RGBA16F AImageReader Surface creation failed";
    else if (!result.sdr_dataspace)
        result.reason = "linear SDR FP16 ANativeWindow dataspace is unavailable";
    else if (!result.vulkan_11)
        result.reason = "Vulkan 1.1 is unavailable";
    else if (!result.fp16_shader)
        result.reason = "Vulkan FP16 shader/storage features are unavailable";
    else if (!result.ahb_external_memory)
        result.reason = "AHardwareBuffer Vulkan external-memory import is unavailable";
    else if (!result.ncnn_vulkan)
        result.reason = "ncnn Vulkan runtime did not find a compute device";
    else if (!result.ncnn_model_loaded)
        result.reason = "RIFE ncnn model load failed (" + result.ncnn_diagnostic + ")";
    else if (!result.ncnn_inference_self_test)
        result.reason = "RIFE Vulkan flow/mask inference failed (" +
            result.ncnn_diagnostic + ")";

    vkDestroyInstance(instance, nullptr);
    AHardwareBuffer_release(hardware_buffer);
    if (reader)
        AImageReader_delete(reader);
    return result;
}

}  // namespace

extern "C" JNIEXPORT jstring JNICALL
Java_com_ohmycine_player_mpv_AndroidFrameInterpolationNative_nativeProbe(
    JNIEnv* environment,
    jobject /* receiver */,
    jstring model_param_path,
    jstring model_bin_path) {
    const char* param_path = model_param_path
                                 ? environment->GetStringUTFChars(model_param_path, nullptr)
                                 : nullptr;
    const char* bin_path = model_bin_path
                               ? environment->GetStringUTFChars(model_bin_path, nullptr)
                               : nullptr;
    const ProbeResult result = probe(param_path, bin_path);
    if (param_path)
        environment->ReleaseStringUTFChars(model_param_path, param_path);
    if (bin_path)
        environment->ReleaseStringUTFChars(model_bin_path, bin_path);
    std::ostringstream json;
    json << "{\"imageReaderFp16\":" << (result.image_reader_fp16 ? "true" : "false")
         << ",\"sdrDataspace\":" << (result.sdr_dataspace ? "true" : "false")
         << ",\"pqDataspace\":" << (result.pq_dataspace ? "true" : "false")
         << ",\"hlgDataspace\":" << (result.hlg_dataspace ? "true" : "false")
         << ",\"linearHdrDataspace\":"
         << (result.linear_hdr_dataspace ? "true" : "false")
         << ",\"hdrDataspace\":" << (result.hdr_dataspace ? "true" : "false")
         << ",\"vulkan11\":" << (result.vulkan_11 ? "true" : "false")
         << ",\"ahardwareBufferExternalMemory\":"
         << (result.ahb_external_memory ? "true" : "false")
         << ",\"shaderFloat16\":" << (result.fp16_shader ? "true" : "false")
         << ",\"ncnnVulkan\":" << (result.ncnn_vulkan ? "true" : "false")
         << ",\"ncnnModelLoaded\":" << (result.ncnn_model_loaded ? "true" : "false")
         << ",\"ncnnInferenceSelfTest\":"
         << (result.ncnn_inference_self_test ? "true" : "false")
         << ",\"gpuName\":\"" << escape_json(result.gpu_name) << "\""
         << ",\"ncnnDiagnostic\":\"" << escape_json(result.ncnn_diagnostic) << "\""
         << ",\"reason\":\"" << escape_json(result.reason) << "\"} ";
    return environment->NewStringUTF(json.str().c_str());
}

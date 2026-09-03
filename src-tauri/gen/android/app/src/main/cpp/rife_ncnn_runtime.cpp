#include "rife_ncnn_runtime.h"

#include "rife_warp.h"

#include <android/log.h>
#include <layer.h>

#include <sstream>

namespace {

DEFINE_LAYER_CREATOR(RifeWarp)

constexpr const char* kLogTag = "OhMyCineFrameGen";

}  // namespace

const char* rife_ncnn_mode_name(RifeNcnnCompatibilityMode mode) {
    switch (mode) {
    case RifeNcnnCompatibilityMode::PackedFp16:
        return "packed-fp16";
    case RifeNcnnCompatibilityMode::SafeFp32:
        return "safe-fp32";
    case RifeNcnnCompatibilityMode::SafeFp32HostWeights:
        return "safe-fp32-host-weights";
    }
    return "unknown";
}

ncnn::Option make_rife_ncnn_option(
    RifeNcnnCompatibilityMode mode,
    ncnn::VkAllocator* blob_allocator,
    ncnn::VkAllocator* staging_allocator) {
    ncnn::Option options;
    options.use_vulkan_compute = true;
    options.use_fp16_arithmetic = false;
    options.blob_vkallocator = blob_allocator;
    options.workspace_vkallocator = blob_allocator;
    options.staging_vkallocator = staging_allocator;

    if (mode == RifeNcnnCompatibilityMode::PackedFp16) {
        options.use_fp16_storage = true;
        options.use_fp16_packed = true;
        options.use_packing_layout = true;
        return options;
    }

    // Some mobile drivers advertise optional subgroup/cooperative-matrix or
    // packed-storage features but reject one of the pipelines while ncnn is
    // loading the graph. The compatibility path remains Vulkan compute; it
    // only uses unpacked FP32 proxy tensors and conservative shader variants.
    // Decoded source frames and final composition stay RGBA16F.
    options.use_fp16_storage = false;
    options.use_fp16_packed = false;
    options.use_fp16_uniform = false;
    options.use_packing_layout = false;
    options.use_int8_inference = false;
    options.use_int8_storage = false;
    options.use_int8_packed = false;
    options.use_int8_uniform = false;
    options.use_subgroup_ops = false;
    options.use_cooperative_matrix = false;
    options.use_shader_local_memory = false;
    options.use_winograd_convolution = false;
    options.use_winograd23_convolution = false;
    options.use_winograd43_convolution = false;
    options.use_winograd63_convolution = false;
    options.use_weights_in_host_memory =
        mode == RifeNcnnCompatibilityMode::SafeFp32HostWeights;
    return options;
}

RifeNcnnLoadResult load_rife_ncnn_network(
    ncnn::Net& network,
    const ncnn::VulkanDevice* device,
    const char* model_param_path,
    const char* model_bin_path,
    RifeNcnnCompatibilityMode mode,
    const ncnn::Option& options) {
    RifeNcnnLoadResult result;
    result.mode = mode;
    network.opt = options;
    network.register_custom_layer("rife.Warp", RifeWarp_layer_creator);
    network.set_vulkan_device(device);
    result.param_status = network.load_param(model_param_path);
    if (result.param_status == 0)
        result.model_status = network.load_model(model_bin_path);
    __android_log_print(
        result.loaded() ? ANDROID_LOG_INFO : ANDROID_LOG_WARN,
        kLogTag,
        "RIFE ncnn load mode=%s param=%d model=%d",
        rife_ncnn_mode_name(mode),
        result.param_status,
        result.model_status);
    return result;
}

std::string format_rife_ncnn_load_result(const RifeNcnnLoadResult& result) {
    std::ostringstream value;
    value << rife_ncnn_mode_name(result.mode)
          << ":param=" << result.param_status
          << ",model=" << result.model_status;
    return value.str();
}

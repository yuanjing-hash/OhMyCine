#pragma once

#include <net.h>

#include <string>

enum class RifeNcnnCompatibilityMode {
    PackedFp16,
    SafeFp32,
    SafeFp32HostWeights,
};

struct RifeNcnnLoadResult {
    RifeNcnnCompatibilityMode mode = RifeNcnnCompatibilityMode::PackedFp16;
    int param_status = -1;
    int model_status = -1;

    bool loaded() const {
        return param_status == 0 && model_status == 0;
    }
};

const char* rife_ncnn_mode_name(RifeNcnnCompatibilityMode mode);

ncnn::Option make_rife_ncnn_option(
    RifeNcnnCompatibilityMode mode,
    ncnn::VkAllocator* blob_allocator = nullptr,
    ncnn::VkAllocator* staging_allocator = nullptr);

RifeNcnnLoadResult load_rife_ncnn_network(
    ncnn::Net& network,
    const ncnn::VulkanDevice* device,
    const char* model_param_path,
    const char* model_bin_path,
    RifeNcnnCompatibilityMode mode,
    const ncnn::Option& options);

std::string format_rife_ncnn_load_result(const RifeNcnnLoadResult& result);

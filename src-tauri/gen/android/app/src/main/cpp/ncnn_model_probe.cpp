#include <gpu.h>
#include <net.h>

#include "rife_ncnn_runtime.h"

#include <algorithm>
#include <cmath>
#include <cstring>
#include <memory>
#include <sstream>
#include <string>

namespace {

bool run_vulkan_flow_mask_self_test(ncnn::Net& network) {
    constexpr int kTestSize = 32;
    ncnn::Mat first(kTestSize, kTestSize, 3);
    ncnn::Mat second(kTestSize, kTestSize, 3);
    ncnn::Mat timestep(kTestSize, kTestSize, 1);
    if (first.empty() || second.empty() || timestep.empty())
        return false;

    first.fill(0.2F);
    second.fill(0.8F);
    timestep.fill(0.5F);

    ncnn::Extractor extractor = network.create_extractor();
    extractor.set_light_mode(false);
    if (extractor.input("in0", first) != 0 ||
        extractor.input("in1", second) != 0 ||
        extractor.input("in2", timestep) != 0) {
        return false;
    }

    // Blob 327 is the final four-channel bidirectional flow and blob 332 is
    // the sigmoid blend mask. We intentionally do not consume out0 for either
    // SDR or HDR: both paths composite the original FP16 pixels, with SDR
    // linearized before the common warp/blend pass and encoded back to SDR at
    // presentation. This keeps SDR supported without creating an 8-bit/HDR
    // split in the backend.
    ncnn::Mat flow;
    ncnn::Mat mask;
    if (extractor.extract("327", flow) != 0 || extractor.extract("332", mask) != 0)
        return false;
    if (flow.w != kTestSize || flow.h != kTestSize || flow.c != 4 ||
        mask.w != kTestSize || mask.h != kTestSize || mask.c != 1) {
        return false;
    }

    for (int channel = 0; channel < flow.c; ++channel) {
        const float* values = flow.channel(channel);
        for (int index = 0; index < flow.w * flow.h; ++index) {
            if (!std::isfinite(values[index]))
                return false;
        }
    }
    const float* mask_values = mask.channel(0);
    for (int index = 0; index < mask.w * mask.h; ++index) {
        if (!std::isfinite(mask_values[index]) || mask_values[index] < 0.0F ||
            mask_values[index] > 1.0F) {
            return false;
        }
    }
    return true;
}

void copy_text(const std::string& value, char* output, size_t output_size) {
    if (output == nullptr || output_size == 0)
        return;
    const size_t length = std::min(output_size - 1, value.size());
    std::memcpy(output, value.data(), length);
    output[length] = '\0';
}

}  // namespace

extern "C" int ohmycine_ncnn_probe(
    const char* model_param_path,
    const char* model_bin_path,
    char* gpu_name,
    size_t gpu_name_size,
    char* diagnostic,
    size_t diagnostic_size) {
    ncnn::create_gpu_instance();
    const int gpu_count = ncnn::get_gpu_count();
    if (gpu_count <= 0) {
        ncnn::destroy_gpu_instance();
        return 0;
    }

    if (gpu_name && gpu_name_size > 0) {
        const ncnn::GpuInfo& info = ncnn::get_gpu_info(0);
        const char* name = info.device_name();
        const size_t length = std::min(gpu_name_size - 1, std::strlen(name));
        std::memcpy(gpu_name, name, length);
        gpu_name[length] = '\0';
    }

    int status = 1;
    if (model_param_path && model_bin_path) {
        const ncnn::VulkanDevice* device = ncnn::get_gpu_device(0);
        const RifeNcnnCompatibilityMode modes[] = {
            RifeNcnnCompatibilityMode::PackedFp16,
            RifeNcnnCompatibilityMode::SafeFp32,
            RifeNcnnCompatibilityMode::SafeFp32HostWeights,
        };
        std::ostringstream attempts;
        bool loaded = false;
        bool inferred = false;
        for (const RifeNcnnCompatibilityMode mode : modes) {
            ncnn::Net network;
            const RifeNcnnLoadResult load = load_rife_ncnn_network(
                network,
                device,
                model_param_path,
                model_bin_path,
                mode,
                make_rife_ncnn_option(mode));
            if (attempts.tellp() > 0)
                attempts << ";";
            attempts << format_rife_ncnn_load_result(load);
            if (load.loaded()) {
                loaded = true;
                inferred = run_vulkan_flow_mask_self_test(network);
                attempts << ",inference=" << (inferred ? 0 : -1);
            }
            network.clear();
            if (inferred)
                break;
        }
        copy_text(attempts.str(), diagnostic, diagnostic_size);
        status = inferred ? 3 : (loaded ? 2 : 1);
    }
    ncnn::destroy_gpu_instance();
    return status;
}

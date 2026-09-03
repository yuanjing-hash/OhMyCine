#include <gpu.h>
#include <layer.h>
#include <net.h>

#include "rife_warp.h"

#include <algorithm>
#include <cmath>
#include <cstddef>
#include <cstring>

namespace {

DEFINE_LAYER_CREATOR(RifeWarp)

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

}  // namespace

extern "C" int ohmycine_ncnn_probe(
    const char* model_param_path,
    const char* model_bin_path,
    char* gpu_name,
    size_t gpu_name_size) {
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
        ncnn::Net network;
        network.opt.use_vulkan_compute = true;
        network.opt.use_fp16_storage = true;
        network.opt.use_fp16_packed = true;
        network.opt.use_fp16_arithmetic = false;
        network.register_custom_layer("rife.Warp", RifeWarp_layer_creator);
        network.set_vulkan_device(0);
        if (network.load_param(model_param_path) == 0 &&
            network.load_model(model_bin_path) == 0) {
            status = 2;
            if (run_vulkan_flow_mask_self_test(network))
                status = 3;
        }
        network.clear();
    }
    ncnn::destroy_gpu_instance();
    return status;
}

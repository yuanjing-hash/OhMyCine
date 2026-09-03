// SPDX-License-Identifier: MIT
// Derived from nihui/rife-ncnn-vulkan at
// a7532fc3f9f8f008cd6eecd6f2ffe2a9698e0cf7. See the bundled NOTICE.

#pragma once

#include <layer.h>
#include <pipeline.h>

#include <vector>

class RifeWarp final : public ncnn::Layer {
public:
    RifeWarp();

    int create_pipeline(const ncnn::Option& opt) override;
    int destroy_pipeline(const ncnn::Option& opt) override;
    int forward(
        const std::vector<ncnn::Mat>& bottom_blobs,
        std::vector<ncnn::Mat>& top_blobs,
        const ncnn::Option& opt) const override;
    int forward(
        const std::vector<ncnn::VkMat>& bottom_blobs,
        std::vector<ncnn::VkMat>& top_blobs,
        ncnn::VkCompute& command,
        const ncnn::Option& opt) const override;

private:
    ncnn::Pipeline* pipeline_pack1_ = nullptr;
    ncnn::Pipeline* pipeline_pack4_ = nullptr;
    ncnn::Pipeline* pipeline_pack8_ = nullptr;
};

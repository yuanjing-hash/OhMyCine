// SPDX-License-Identifier: MIT

#pragma once

#include <android/hardware_buffer.h>
#include <android/native_window.h>

#include <cstdint>
#include <memory>
#include <string>

struct AndroidNcnnFrameProcessorSnapshot {
    uint64_t proxy_frames = 0;
    uint64_t inferred_pairs = 0;
    uint64_t presented_frames = 0;
    double latest_inference_ms = 0.0;
};

class AndroidNcnnFrameProcessor final {
public:
    AndroidNcnnFrameProcessor();
    ~AndroidNcnnFrameProcessor();

    AndroidNcnnFrameProcessor(const AndroidNcnnFrameProcessor&) = delete;
    AndroidNcnnFrameProcessor& operator=(const AndroidNcnnFrameProcessor&) = delete;

    bool create(
        ANativeWindow* output_window,
        const char* model_param_path,
        const char* model_bin_path,
        std::string* reason);
    void destroy();
    bool process(AHardwareBuffer* hardware_buffer, std::string* reason);
    AndroidNcnnFrameProcessorSnapshot snapshot() const;

private:
    class Impl;
    std::unique_ptr<Impl> impl_;
};

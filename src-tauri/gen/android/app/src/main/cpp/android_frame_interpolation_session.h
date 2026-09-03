// SPDX-License-Identifier: MIT

#pragma once

#include "android_frame_source.h"

#include <android/native_window.h>

#include <atomic>
#include <cstdint>
#include <memory>
#include <mutex>
#include <string>
#include <thread>

struct AndroidFrameInterpolationSessionSnapshot {
    uint64_t generation = 0;
    uint64_t acquired_frames = 0;
    uint64_t source_dropped_frames = 0;
    uint64_t imported_frames = 0;
    uint64_t import_failures = 0;
    uint64_t stale_frames = 0;
    uint64_t proxy_frames = 0;
    uint64_t inferred_pairs = 0;
    double latest_inference_ms = 0.0;
    int64_t latest_timestamp_ns = 0;
    int queued_frames = 0;
    bool prepared = false;
    bool running = false;
    bool output_surface_ready = false;
    bool first_frame_imported = false;
    bool first_frame_presented = false;
    std::string reason;
};

class AndroidFrameInterpolationSession final {
public:
    AndroidFrameInterpolationSession();
    ~AndroidFrameInterpolationSession();

    AndroidFrameInterpolationSession(const AndroidFrameInterpolationSession&) = delete;
    AndroidFrameInterpolationSession& operator=(const AndroidFrameInterpolationSession&) = delete;

    bool prepare(
        ANativeWindow* output_window,
        int width,
        int height,
        int data_space,
        uint64_t generation,
        const char* model_param_path,
        const char* model_bin_path);
    bool start();
    void stop();
    ANativeWindow* input_window() const;
    AndroidFrameInterpolationSessionSnapshot snapshot() const;

private:
    class VulkanImporter;
    void consume_frames();
    void record_failure(const char* reason);

    mutable std::mutex mutex_;
    AndroidFrameSource source_;
    std::unique_ptr<VulkanImporter> importer_;
    ANativeWindow* output_window_ = nullptr;
    std::thread worker_;
    std::atomic<bool> stop_requested_{false};
    uint64_t generation_ = 0;
    uint64_t imported_frames_ = 0;
    uint64_t import_failures_ = 0;
    uint64_t stale_frames_ = 0;
    int64_t latest_timestamp_ns_ = 0;
    bool prepared_ = false;
    bool running_ = false;
    bool output_surface_ready_ = false;
    bool first_frame_imported_ = false;
    // This remains false until the later composite/swapchain stage has really
    // presented a frame. Importing a decoder image is not presentation.
    bool first_frame_presented_ = false;
    std::string reason_;
};

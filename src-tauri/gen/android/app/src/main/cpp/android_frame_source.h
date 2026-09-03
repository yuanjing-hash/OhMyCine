// SPDX-License-Identifier: MIT

#pragma once

#include <android/hardware_buffer.h>
#include <android/native_window.h>
#include <media/NdkImageReader.h>

#include <cstdint>
#include <condition_variable>
#include <deque>
#include <mutex>

struct AndroidFrameSourceSnapshot {
    uint64_t generation = 0;
    uint64_t acquired_frames = 0;
    uint64_t dropped_frames = 0;
    int queued_frames = 0;
    int64_t latest_timestamp_ns = 0;
};

struct AndroidFrameSourceImage {
    AImage* image = nullptr;
    int acquire_fence_fd = -1;
};

class AndroidFrameSource final {
public:
    AndroidFrameSource() = default;
    ~AndroidFrameSource();

    AndroidFrameSource(const AndroidFrameSource&) = delete;
    AndroidFrameSource& operator=(const AndroidFrameSource&) = delete;

    bool create(int width, int height, int data_space, uint64_t generation);
    void destroy();
    ANativeWindow* window() const;
    AndroidFrameSourceSnapshot snapshot() const;

    // Transfers ownership of the oldest queued image to the caller. The caller
    // must call AImage_delete after Vulkan has finished reading its buffer.
    AndroidFrameSourceImage acquire_for_generation(uint64_t generation);
    AndroidFrameSourceImage wait_acquire_for_generation(uint64_t generation, int timeout_ms);

private:
    static void on_image_available(void* context, AImageReader* reader);
    void enqueue_latest(AImageReader* reader);

    static constexpr size_t kRingCapacity = 4;
    mutable std::mutex mutex_;
    std::condition_variable image_available_;
    AImageReader* reader_ = nullptr;
    ANativeWindow* window_ = nullptr;
    std::deque<AndroidFrameSourceImage> images_;
    uint64_t generation_ = 0;
    uint64_t acquired_frames_ = 0;
    uint64_t dropped_frames_ = 0;
    int64_t latest_timestamp_ns_ = 0;
    bool accepting_frames_ = false;
};

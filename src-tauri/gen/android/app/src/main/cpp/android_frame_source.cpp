// SPDX-License-Identifier: MIT

#include "android_frame_source.h"
#include "android_dataspace_compat.h"

#include <android/api-level.h>
#include <android/data_space.h>

#include <chrono>
#include <unistd.h>

namespace {

void delete_source_image(AndroidFrameSourceImage image) {
    if (image.acquire_fence_fd >= 0)
        close(image.acquire_fence_fd);
    if (image.image != nullptr)
        AImage_delete(image.image);
}

}  // namespace

AndroidFrameSource::~AndroidFrameSource() {
    destroy();
}

bool AndroidFrameSource::create(
    int width,
    int height,
    int data_space,
    uint64_t generation) {
    destroy();
    if (android_get_device_api_level() < 29 || width <= 0 || height <= 0)
        return false;

    AImageReader* reader = nullptr;
    const media_status_t status = AImageReader_newWithUsage(
        width,
        height,
        AIMAGE_FORMAT_RGBA_FP16,
        AHARDWAREBUFFER_USAGE_GPU_SAMPLED_IMAGE | AHARDWAREBUFFER_USAGE_GPU_COLOR_OUTPUT,
        static_cast<int32_t>(kRingCapacity),
        &reader);
    if (status != AMEDIA_OK || reader == nullptr)
        return false;

    ANativeWindow* native_window = nullptr;
    if (AImageReader_getWindow(reader, &native_window) != AMEDIA_OK || native_window == nullptr) {
        AImageReader_delete(reader);
        return false;
    }
    if (ohmycine::android_compat::set_buffers_dataspace(native_window, data_space) != 0 ||
        ohmycine::android_compat::get_buffers_dataspace(native_window) != data_space) {
        AImageReader_delete(reader);
        return false;
    }

    {
        std::lock_guard<std::mutex> lock(mutex_);
        reader_ = reader;
        window_ = native_window;
        generation_ = generation;
        acquired_frames_ = 0;
        dropped_frames_ = 0;
        latest_timestamp_ns_ = 0;
        accepting_frames_ = true;
    }
    AImageReader_ImageListener listener{this, &AndroidFrameSource::on_image_available};
    if (AImageReader_setImageListener(reader, &listener) != AMEDIA_OK) {
        destroy();
        return false;
    }
    return true;
}

void AndroidFrameSource::destroy() {
    AImageReader* reader = nullptr;
    std::deque<AndroidFrameSourceImage> stale_images;
    {
        std::lock_guard<std::mutex> lock(mutex_);
        accepting_frames_ = false;
        reader = reader_;
        reader_ = nullptr;
        window_ = nullptr;
        stale_images.swap(images_);
        ++generation_;
    }
    image_available_.notify_all();
    if (reader != nullptr) {
        AImageReader_ImageListener no_listener{nullptr, nullptr};
        AImageReader_setImageListener(reader, &no_listener);
    }
    for (const AndroidFrameSourceImage image : stale_images)
        delete_source_image(image);
    if (reader != nullptr)
        AImageReader_delete(reader);
}

ANativeWindow* AndroidFrameSource::window() const {
    std::lock_guard<std::mutex> lock(mutex_);
    return window_;
}

AndroidFrameSourceSnapshot AndroidFrameSource::snapshot() const {
    std::lock_guard<std::mutex> lock(mutex_);
    return AndroidFrameSourceSnapshot{
        generation_,
        acquired_frames_,
        dropped_frames_,
        static_cast<int>(images_.size()),
        latest_timestamp_ns_,
    };
}

AndroidFrameSourceImage AndroidFrameSource::acquire_for_generation(uint64_t generation) {
    std::lock_guard<std::mutex> lock(mutex_);
    if (!accepting_frames_ || generation != generation_ || images_.empty())
        return {};
    AndroidFrameSourceImage image = images_.front();
    images_.pop_front();
    return image;
}

AndroidFrameSourceImage AndroidFrameSource::wait_acquire_for_generation(
    uint64_t generation,
    int timeout_ms) {
    std::unique_lock<std::mutex> lock(mutex_);
    image_available_.wait_for(
        lock,
        std::chrono::milliseconds(timeout_ms),
        [this, generation] {
            return !accepting_frames_ || generation != generation_ || !images_.empty();
        });
    if (!accepting_frames_ || generation != generation_ || images_.empty())
        return {};
    AndroidFrameSourceImage image = images_.front();
    images_.pop_front();
    return image;
}

void AndroidFrameSource::on_image_available(void* context, AImageReader* reader) {
    if (context != nullptr)
        static_cast<AndroidFrameSource*>(context)->enqueue_latest(reader);
}

void AndroidFrameSource::enqueue_latest(AImageReader* reader) {
    if (android_get_device_api_level() < 29)
        return;

    AImage* image = nullptr;
    int acquire_fence_fd = -1;
    const media_status_t status =
        AImageReader_acquireLatestImageAsync(reader, &image, &acquire_fence_fd);
    if (status != AMEDIA_OK || image == nullptr)
        return;

    AHardwareBuffer* hardware_buffer = nullptr;
    int64_t timestamp_ns = 0;
    if (AImage_getHardwareBuffer(image, &hardware_buffer) != AMEDIA_OK ||
        hardware_buffer == nullptr || AImage_getTimestamp(image, &timestamp_ns) != AMEDIA_OK) {
        delete_source_image({image, acquire_fence_fd});
        return;
    }

    std::lock_guard<std::mutex> lock(mutex_);
    if (!accepting_frames_ || reader != reader_) {
        delete_source_image({image, acquire_fence_fd});
        return;
    }
    while (images_.size() >= kRingCapacity - 1) {
        delete_source_image(images_.front());
        images_.pop_front();
        ++dropped_frames_;
    }
    images_.push_back({image, acquire_fence_fd});
    ++acquired_frames_;
    latest_timestamp_ns_ = timestamp_ns;
    image_available_.notify_one();
}

// SPDX-License-Identifier: MIT

#include "android_frame_source.h"
#include "android_frame_interpolation_session.h"

#include <android/api-level.h>
#include <android/data_space.h>
#include <android/native_window_jni.h>
#include <jni.h>

#include <memory>
#include <mutex>
#include <sstream>

namespace {

std::mutex g_source_mutex;
std::unique_ptr<AndroidFrameSource> g_source;
std::unique_ptr<AndroidFrameInterpolationSession> g_session;

int data_space_for_mode(jint mode) {
    switch (mode) {
    case 1:
    case 2:
    case 3:
        // HDR10/HDR10+/HLG/Dolby Vision have already been mapped by
        // mpv/libplacebo. Request a linear FP16 carrier for synthesis; the
        // output swapchain applies PQ or keeps scRGB as appropriate.
        return ADATASPACE_SCRGB_LINEAR;
    default:
        return ADATASPACE_SRGB_LINEAR;
    }
}

std::string escape_json(const std::string& value) {
    std::string escaped;
    escaped.reserve(value.size());
    for (char character : value) {
        if (character == '\\' || character == '"')
            escaped.push_back('\\');
        if (character == '\n' || character == '\r' || character == '\t') {
            escaped.push_back(' ');
        } else {
            escaped.push_back(character);
        }
    }
    return escaped;
}

}  // namespace

extern "C" JNIEXPORT jobject JNICALL
Java_com_ohmycine_player_mpv_AndroidFrameInterpolationNative_nativeCreateInputSurface(
    JNIEnv* environment,
    jobject /* receiver */,
    jint width,
    jint height,
    jint data_space_mode,
    jlong generation) {
    if (android_get_device_api_level() < 29)
        return nullptr;

    std::lock_guard<std::mutex> lock(g_source_mutex);
    auto source = std::make_unique<AndroidFrameSource>();
    if (!source->create(
            width,
            height,
            data_space_for_mode(data_space_mode),
            static_cast<uint64_t>(generation))) {
        return nullptr;
    }
    jobject surface = ANativeWindow_toSurface(environment, source->window());
    if (surface == nullptr)
        return nullptr;
    if (g_source)
        g_source->destroy();
    g_source = std::move(source);
    return surface;
}

extern "C" JNIEXPORT void JNICALL
Java_com_ohmycine_player_mpv_AndroidFrameInterpolationNative_nativeDestroyInputSurface(
    JNIEnv* /* environment */,
    jobject /* receiver */) {
    std::lock_guard<std::mutex> lock(g_source_mutex);
    if (g_session)
        g_session->stop();
    g_session.reset();
    if (g_source)
        g_source->destroy();
    g_source.reset();
}

extern "C" JNIEXPORT jobject JNICALL
Java_com_ohmycine_player_mpv_AndroidFrameInterpolationNative_nativePrepareSession(
    JNIEnv* environment,
    jobject /* receiver */,
    jobject output_surface,
    jint width,
    jint height,
    jint data_space_mode,
    jlong generation,
    jstring model_param_path,
    jstring model_bin_path) {
    if (android_get_device_api_level() < 29 || output_surface == nullptr ||
        model_param_path == nullptr || model_bin_path == nullptr)
        return nullptr;
    ANativeWindow* output_window = ANativeWindow_fromSurface(environment, output_surface);
    if (output_window == nullptr)
        return nullptr;
    const char* param_path = environment->GetStringUTFChars(model_param_path, nullptr);
    const char* bin_path = environment->GetStringUTFChars(model_bin_path, nullptr);
    if (param_path == nullptr || bin_path == nullptr) {
        if (param_path != nullptr)
            environment->ReleaseStringUTFChars(model_param_path, param_path);
        if (bin_path != nullptr)
            environment->ReleaseStringUTFChars(model_bin_path, bin_path);
        ANativeWindow_release(output_window);
        return nullptr;
    }
    auto session = std::make_unique<AndroidFrameInterpolationSession>();
    const bool prepared = session->prepare(
        output_window,
        width,
        height,
        data_space_for_mode(data_space_mode),
        static_cast<uint64_t>(generation),
        param_path,
        bin_path);
    environment->ReleaseStringUTFChars(model_param_path, param_path);
    environment->ReleaseStringUTFChars(model_bin_path, bin_path);
    ANativeWindow_release(output_window);
    if (!prepared)
        return nullptr;
    jobject input_surface = ANativeWindow_toSurface(environment, session->input_window());
    if (input_surface == nullptr)
        return nullptr;
    std::lock_guard<std::mutex> lock(g_source_mutex);
    if (g_session)
        g_session->stop();
    g_session = std::move(session);
    return input_surface;
}

extern "C" JNIEXPORT jboolean JNICALL
Java_com_ohmycine_player_mpv_AndroidFrameInterpolationNative_nativeStartSession(
    JNIEnv* /* environment */,
    jobject /* receiver */) {
    std::lock_guard<std::mutex> lock(g_source_mutex);
    return g_session && g_session->start() ? JNI_TRUE : JNI_FALSE;
}

extern "C" JNIEXPORT void JNICALL
Java_com_ohmycine_player_mpv_AndroidFrameInterpolationNative_nativeStopSession(
    JNIEnv* /* environment */,
    jobject /* receiver */) {
    std::unique_ptr<AndroidFrameInterpolationSession> session;
    {
        std::lock_guard<std::mutex> lock(g_source_mutex);
        session = std::move(g_session);
    }
    if (session)
        session->stop();
}

extern "C" JNIEXPORT jstring JNICALL
Java_com_ohmycine_player_mpv_AndroidFrameInterpolationNative_nativeSessionSnapshot(
    JNIEnv* environment,
    jobject /* receiver */) {
    std::lock_guard<std::mutex> lock(g_source_mutex);
    if (!g_session)
        return environment->NewStringUTF("{\"prepared\":false,\"running\":false}");
    const AndroidFrameInterpolationSessionSnapshot value = g_session->snapshot();
    std::ostringstream json;
    json << "{\"prepared\":" << (value.prepared ? "true" : "false")
         << ",\"running\":" << (value.running ? "true" : "false")
         << ",\"generation\":" << value.generation
         << ",\"acquiredFrames\":" << value.acquired_frames
         << ",\"sourceDroppedFrames\":" << value.source_dropped_frames
         << ",\"importedFrames\":" << value.imported_frames
         << ",\"importFailures\":" << value.import_failures
         << ",\"staleFrames\":" << value.stale_frames
         << ",\"proxyFrames\":" << value.proxy_frames
         << ",\"inferredPairs\":" << value.inferred_pairs
         << ",\"latestInferenceMs\":" << value.latest_inference_ms
         << ",\"latestTimestampNs\":" << value.latest_timestamp_ns
         << ",\"queuedFrames\":" << value.queued_frames
         << ",\"outputSurfaceReady\":"
         << (value.output_surface_ready ? "true" : "false")
         << ",\"firstFrameImported\":"
         << (value.first_frame_imported ? "true" : "false")
         << ",\"firstFramePresented\":"
         << (value.first_frame_presented ? "true" : "false")
         << ",\"reason\":\"" << escape_json(value.reason) << "\"}";
    return environment->NewStringUTF(json.str().c_str());
}

extern "C" JNIEXPORT jstring JNICALL
Java_com_ohmycine_player_mpv_AndroidFrameInterpolationNative_nativeFrameSourceSnapshot(
    JNIEnv* environment,
    jobject /* receiver */) {
    std::lock_guard<std::mutex> lock(g_source_mutex);
    if (!g_source)
        return environment->NewStringUTF("{\"created\":false}");
    const AndroidFrameSourceSnapshot value = g_source->snapshot();
    std::ostringstream json;
    json << "{\"created\":true,\"generation\":" << value.generation
         << ",\"acquiredFrames\":" << value.acquired_frames
         << ",\"droppedFrames\":" << value.dropped_frames
         << ",\"queuedFrames\":" << value.queued_frames
         << ",\"latestTimestampNs\":" << value.latest_timestamp_ns << "}";
    return environment->NewStringUTF(json.str().c_str());
}

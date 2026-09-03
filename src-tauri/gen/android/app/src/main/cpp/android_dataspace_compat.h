#pragma once

#include <android/native_window.h>
#include <dlfcn.h>
#include <errno.h>
#include <stdint.h>

// These entry points live in libnativewindow starting with API 28, while the application keeps
// minSdk 24. Resolve them at runtime so Android 7-9 can still load the JNI library and retain
// ordinary playback. The Kotlin capability gate enables interpolation only on API 29+ after these
// calls and the remaining FP16/Vulkan probes succeed.
namespace ohmycine::android_compat {

using SetBuffersDataSpace = int32_t (*)(ANativeWindow*, int32_t);
using GetBuffersDataSpace = int32_t (*)(ANativeWindow*);

inline void* nativewindow_symbol(const char* name) {
    static void* library = dlopen("libnativewindow.so", RTLD_NOW | RTLD_LOCAL);
    return dlsym(library != nullptr ? library : RTLD_DEFAULT, name);
}

inline int32_t set_buffers_dataspace(ANativeWindow* window, int32_t dataspace) {
    static const auto function = reinterpret_cast<SetBuffersDataSpace>(
        nativewindow_symbol("ANativeWindow_setBuffersDataSpace"));
    return function != nullptr ? function(window, dataspace) : -ENOSYS;
}

inline int32_t get_buffers_dataspace(ANativeWindow* window) {
    static const auto function = reinterpret_cast<GetBuffersDataSpace>(
        nativewindow_symbol("ANativeWindow_getBuffersDataSpace"));
    return function != nullptr ? function(window) : 0;
}

}  // namespace ohmycine::android_compat

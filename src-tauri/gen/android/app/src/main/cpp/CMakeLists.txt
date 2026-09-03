cmake_minimum_required(VERSION 3.22.1)
project(ohmycine_framegen LANGUAGES CXX)

set(ncnn_DIR "${CMAKE_CURRENT_SOURCE_DIR}/third_party/ncnn/lib/cmake/ncnn")
find_package(ncnn REQUIRED)

add_library(
    ohmycine_framegen
    SHARED
    frame_interpolation_probe.cpp
    android_frame_interpolation_session.cpp
    android_ncnn_frame_processor.cpp
    android_frame_source.cpp
    android_frame_source_jni.cpp
    ncnn_model_probe.cpp
    rife_warp.cpp
)
target_compile_features(ohmycine_framegen PRIVATE cxx_std_20)
target_compile_definitions(ohmycine_framegen PRIVATE __ANDROID_UNAVAILABLE_SYMBOLS_ARE_WEAK__)
target_compile_options(
    ohmycine_framegen
    PRIVATE
    -Wall
    -Wextra
    -Werror
    -Wno-unguarded-availability
    -Wno-unguarded-availability-new
)
target_link_libraries(ohmycine_framegen PRIVATE android log mediandk ncnn vulkan)

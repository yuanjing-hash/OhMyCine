#!/bin/bash
set -euo pipefail
. ../../include/path.sh
build=_build$ndk_suffix
if [ "${1:-}" != build ]; then exit 255; fi
cmake -S . -B "$build" -G Ninja \
    -DCMAKE_TOOLCHAIN_FILE="$DIR/sdk/android-ndk-r29/build/cmake/android.toolchain.cmake" \
    -DANDROID_ABI=arm64-v8a -DANDROID_PLATFORM=android-24 -DANDROID_STL=c++_shared \
    -DCMAKE_BUILD_TYPE=Release -DCMAKE_POSITION_INDEPENDENT_CODE=ON \
    -DCMAKE_INSTALL_PREFIX=/usr/local -DSHADERC_SKIP_TESTS=ON \
    -DSHADERC_SKIP_EXAMPLES=ON -DSHADERC_SKIP_EXECUTABLES=ON \
    -DSPIRV_SKIP_TESTS=ON -DSPIRV_SKIP_EXECUTABLES=ON -DBUILD_TESTING=OFF
cmake --build "$build" --parallel "$cores"
DESTDIR="$prefix_dir" cmake --install "$build"
cat > "$prefix_dir/lib/pkgconfig/shaderc.pc" <<'EOF'
prefix=/usr/local
libdir=${prefix}/lib
includedir=${prefix}/include
Name: shaderc
Description: Pinned Android static shader compiler
Version: 2025.5
Libs: -L${libdir} -lshaderc_combined -lc++
Cflags: -I${includedir}
EOF

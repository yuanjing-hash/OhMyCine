use std::{env, path::PathBuf};

fn main() {
    println!("cargo:rerun-if-env-changed=OHMYCINE_DANDANPLAY_APP_ID");
    println!("cargo:rerun-if-env-changed=OHMYCINE_DANDANPLAY_APP_SECRET");
    let target = env::var("TARGET").unwrap_or_default();
    if target == "aarch64-linux-android" {
        let root = PathBuf::from(env::var("CARGO_MANIFEST_DIR").expect("manifest directory"));
        let headers = root.join("native/ffmpeg-headers");
        assert!(
            headers.join("libavformat/avformat.h").exists(),
            "Run node scripts/setup-remux.mjs --android first"
        );
        println!("cargo:rerun-if-changed=native/remux.c");
        println!("cargo:rerun-if-changed=native/ffmpeg-headers");
        cc::Build::new()
            .file(root.join("native/remux.c"))
            .include(headers)
            .flag("-std=c11")
            .compile("ohmycine_remux");
        let libs = root.join("gen/android/app/src/main/jniLibs/arm64-v8a");
        println!("cargo:rerun-if-changed={}", libs.display());
        println!("cargo:rustc-link-search=native={}", libs.display());
        for library in ["avformat", "avcodec", "avutil"] {
            assert!(
                libs.join(format!("lib{library}.so")).exists(),
                "Run node scripts/setup-libmpv-android.mjs first"
            );
            println!("cargo:rustc-link-lib=dylib={library}");
        }
    }

    // Both Windows toolchains link libmpv-sys with `-lmpv`: GNU resolves
    // libmpv.dll.a while MSVC resolves mpv.lib. Keep the vendored search path
    // Windows-only so native Linux continues to use system libmpv/pkg-config.
    if matches!(
        target.as_str(),
        "x86_64-pc-windows-gnu" | "x86_64-pc-windows-msvc"
    ) {
        let manifest_dir =
            PathBuf::from(env::var("CARGO_MANIFEST_DIR").expect("CARGO_MANIFEST_DIR is set"));
        let lib_dir = manifest_dir.join("lib");

        println!("cargo:rustc-link-search=native={}", lib_dir.display());
    }

    tauri_build::build()
}

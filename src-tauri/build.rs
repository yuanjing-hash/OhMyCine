use std::{env, path::PathBuf};

fn main() {
    println!("cargo:rustc-check-cfg=cfg(ohmycine_framegen_directml_probe)");
    println!("cargo:rerun-if-env-changed=OHMYCINE_DANDANPLAY_APP_ID");
    println!("cargo:rerun-if-env-changed=OHMYCINE_DANDANPLAY_APP_SECRET");
    let target = env::var("TARGET").unwrap_or_default();

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

        if target == "x86_64-pc-windows-msvc" {
            configure_windows_frame_interpolation_probe(&manifest_dir, &lib_dir);
        }
    }

    tauri_build::build()
}

fn configure_windows_frame_interpolation_probe(manifest_dir: &std::path::Path, lib_dir: &std::path::Path) {
    let runtime_dir = lib_dir.join("frame-interpolation");
    let include_dir = runtime_dir.join("include");
    let ort_lib = runtime_dir.join("onnxruntime.lib");
    let bridge = manifest_dir.join("native/windows_frame_interpolation_probe.cpp");
    if !include_dir.join("onnxruntime_cxx_api.h").is_file() || !ort_lib.is_file() || !bridge.is_file() {
        println!("cargo:warning=DirectML frame-interpolation probe disabled; run npm run setup:libmpv");
        return;
    }

    cc::Build::new()
        .cpp(true)
        .file(&bridge)
        .include(&include_dir)
        .flag_if_supported("/std:c++20")
        .flag_if_supported("/EHsc")
        .warnings_into_errors(true)
        .compile("ohmycine_framegen_directml_probe");
    println!("cargo:rustc-link-search=native={}", runtime_dir.display());
    println!("cargo:rustc-link-lib=dylib=onnxruntime");
    println!("cargo:rustc-cfg=ohmycine_framegen_directml_probe");

    if let Ok(output_dir) = env::var("OUT_DIR") {
        let mut profile_dir = PathBuf::from(output_dir);
        for _ in 0..3 {
            profile_dir.pop();
        }
        for name in ["onnxruntime.dll", "onnxruntime_providers_shared.dll", "DirectML.dll"] {
            let source = runtime_dir.join(name);
            if source.is_file() {
                let _ = std::fs::copy(&source, profile_dir.join(name));
                let deps = profile_dir.join("deps");
                let _ = std::fs::create_dir_all(&deps);
                let _ = std::fs::copy(&source, deps.join(name));
            }
        }
    }
}

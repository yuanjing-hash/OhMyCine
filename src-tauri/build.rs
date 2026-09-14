use std::{env, path::PathBuf};

fn main() {
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
    }

    tauri_build::build()
}

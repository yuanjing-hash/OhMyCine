use std::{env, path::PathBuf};

fn main() {
    let target = env::var("TARGET").unwrap_or_default();

    // The Windows GNU cross-build links libmpv-sys with `-lmpv`, which needs the
    // import library extracted by scripts/setup-libmpv.mjs. Keep this scoped to
    // Windows so native Linux checks continue to use system libmpv/pkg-config.
    if target == "x86_64-pc-windows-gnu" {
        let manifest_dir = PathBuf::from(env::var("CARGO_MANIFEST_DIR").expect("CARGO_MANIFEST_DIR is set"));
        let lib_dir = manifest_dir.join("lib");

        println!("cargo:rustc-link-search=native={}", lib_dir.display());
    }

    tauri_build::build()
}

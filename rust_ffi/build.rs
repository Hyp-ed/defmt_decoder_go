fn main() {
    let crate_dir = std::env::var("CARGO_MANIFEST_DIR").unwrap();
    let lib_name = std::env::var("CARGO_PKG_NAME").unwrap().replace('-', "_");

    let config = cbindgen::Config {
        language: cbindgen::Language::C,
        usize_is_size_t: true,
        ..Default::default()
    };

    cbindgen::Builder::new()
        .with_crate(&crate_dir)
        .with_config(config)
        .generate()
        .expect("cbindgen failed")
        .write_to_file("include/defmt_decoder_wrapper.h");

    let target_os = std::env::var("CARGO_CFG_TARGET_OS").unwrap();
    match target_os.as_str() {
        "macos" => {
            println!("cargo:rustc-cdylib-link-arg=-Wl,-install_name,@rpath/lib{lib_name}.dylib")
        }
        "linux" => println!("cargo:rustc-cdylib-link-arg=-Wl,-soname,lib{lib_name}.so"),
        // a DLL has no install name / soname, it is always looked up by its file name
        // ({lib_name}.dll) next to the executable or on PATH. cgo links with MinGW, so build for
        // the *-pc-windows-gnu target, which also emits the import library lib{lib_name}.dll.a
        "windows" => {
            let target_env = std::env::var("CARGO_CFG_TARGET_ENV").unwrap();
            if target_env != "gnu" {
                println!(
                    "cargo:warning=cgo links with MinGW, build for the *-pc-windows-gnu target instead of {target_env}"
                );
            }
        }
        _ => {}
    }
}

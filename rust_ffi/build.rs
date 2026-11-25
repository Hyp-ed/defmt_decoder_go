fn main() {
    let crate_dir = std::env::var("CARGO_MANIFEST_DIR").unwrap();

    cbindgen::Builder::new()
        .with_crate(&crate_dir)
        .with_language(cbindgen::Language::C)
        .generate()
        .expect("cbindgen failed")
        .write_to_file("include/defmt_decoder.h");

    println!("cargo:rustc-cdylib-link-arg=-Wl,-install_name,@rpath/libdefmt_decoder_wrapper.dylib");
}

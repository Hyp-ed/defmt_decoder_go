# cargo puts the output under target/<triple>/release when a target is given explicitly
# (e.g. CARGO_BUILD_TARGET=x86_64-pc-windows-gnu on Windows), else under target/release
ifdef CARGO_BUILD_TARGET
	RUST_OUT := rust_ffi/target/$(CARGO_BUILD_TARGET)/release
else
	RUST_OUT := rust_ffi/target/release
endif
LIB_NAME := defmt_decoder_wrapper

ifeq ($(OS),Windows_NT)
	LIB_FILES := $(RUST_OUT)/$(LIB_NAME).dll $(RUST_OUT)/lib$(LIB_NAME).dll.a
else ifeq ($(shell uname -s),Darwin)
	LIB_FILES := $(RUST_OUT)/lib$(LIB_NAME).dylib
else
	LIB_FILES := $(RUST_OUT)/lib$(LIB_NAME).so
endif

.PHONY: test
test: build_rust
	go test ./

.PHONY: build_rust
build_rust:
	$(MAKE) -C rust_ffi
	mkdir -p build/lib build/include
	cp rust_ffi/include/* build/include/
	cp $(LIB_FILES) build/lib/

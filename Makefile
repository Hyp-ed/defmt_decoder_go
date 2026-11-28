# cargo puts the output under target/<triple>/release when a target is given explicitly
# (e.g. CARGO_BUILD_TARGET=x86_64-pc-windows-gnu on Windows), else under target/release
ifdef CARGO_BUILD_TARGET
	RUST_OUT := rust_ffi/target/$(CARGO_BUILD_TARGET)/release
else
	RUST_OUT := rust_ffi/target/release
endif
LIB_NAME := defmt_decoder_wrapper

# the static library is lib<name>.a on every target, the *-pc-windows-gnu one included
LIB_FILES := $(RUST_OUT)/lib$(LIB_NAME).a

ifeq ($(OS),Windows_NT)
	DEST_DIR := windows_amd64
else ifeq ($(shell uname -s),Darwin)
	DEST_DIR := macos_arm64
else
	# Linux reports aarch64, not arm64
	ifneq ($(filter arm64 aarch64,$(shell uname -m)),)
		DEST_DIR := linux_arm64
	else
		DEST_DIR := linux_amd64
	endif
endif

.PHONY: test_stripped
test_stripped: build_rust strip
	go test -v ./

.PHONY: test
test: build_rust
	go test ./

.PHONY: strip
strip: build_rust
ifeq ($(shell uname -s),Darwin)
	strip -S build/lib/$(DEST_DIR)/*.a
	ranlib build/lib/$(DEST_DIR)/*.a
else
	strip -g build/lib/$(DEST_DIR)/*.a
endif

.PHONY: build_rust
build_rust:
	$(MAKE) -C rust_ffi
	mkdir -p build/lib/$(DEST_DIR) build/include
	cp rust_ffi/include/* build/include/
	cp $(LIB_FILES) build/lib/$(DEST_DIR)/

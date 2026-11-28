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

# where build_rust and strip put the header and library. Defaults to the dev folder, so only
# the *_release targets below ever write to the committed build/
OUT ?= build-dev

# ---- dev: build-dev/ (git-ignored), linked with -tags defmt_decoder_dev (cgo_dev.go) ----

.PHONY: build_dev
build_dev:
	$(MAKE) build_rust OUT=build-dev

.PHONY: test_dev
test_dev: build_dev
	go test -tags defmt_decoder_dev ./

.PHONY: test_dev_stripped
test_dev_stripped:
	$(MAKE) strip OUT=build-dev
	go test -tags defmt_decoder_dev -v ./

.PHONY: test
test: test_dev

# ---- release: build/, committed by CI to the release PR and linked by default
# (cgo_release.go), so it is what users of the module get ----

# stripped, as it gets committed. CI runs this; locally it changes tracked files
.PHONY: build_release
build_release:
	@echo "" >&2
	@echo "WARNING: building into build/, which is committed and is what users of the module get." >&2
	@echo "         CI updates it on the release PR. Don't commit a local build." >&2
	@echo "" >&2
	$(MAKE) strip OUT=build

# the committed build/ libraries as they are, without building anything
.PHONY: test_release
test_release:
	@echo "" >&2
	@echo "WARNING: testing the committed build/ libraries, not your local changes. They come" >&2
	@echo "         from the last release PR and may not match the current Rust code." >&2
	@echo "" >&2
	go test -v ./

.PHONY: strip
strip: build_rust
ifeq ($(shell uname -s),Darwin)
	strip -S $(OUT)/lib/$(DEST_DIR)/*.a
	ranlib $(OUT)/lib/$(DEST_DIR)/*.a
else
	strip -g $(OUT)/lib/$(DEST_DIR)/*.a
endif

.PHONY: build_rust
build_rust:
	$(MAKE) -C rust_ffi
	mkdir -p $(OUT)/lib/$(DEST_DIR) $(OUT)/include
	cp rust_ffi/include/* $(OUT)/include/
	cp $(LIB_FILES) $(OUT)/lib/$(DEST_DIR)/

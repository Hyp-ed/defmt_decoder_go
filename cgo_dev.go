//go:build defmt_decoder_dev

// With -tags defmt_decoder_dev (what the make *_dev targets pass), links the library from a
// local build in build-dev/, which is git-ignored. Without the tag, cgo_release.go links the
// committed build/ instead, which is what users of the module get. The tag is named after the
// package because tags apply to the whole build: a user's own "dev" tag must not switch this.

package defmt_decoder

/*
#cgo CFLAGS: -I${SRCDIR}/build-dev/include/

// the static library is linked by its full path, so a stray .dylib/.so next to it can't be
// picked instead. The system libraries after it are what Rust's std needs, from
// `cargo rustc --release --target <triple> --crate-type staticlib -- --print native-static-libs`
#cgo linux,amd64 LDFLAGS: ${SRCDIR}/build-dev/lib/linux_amd64/libdefmt_decoder_wrapper.a -lgcc_s -lutil -lrt -lpthread -lm -ldl -lc
#cgo linux,arm64 LDFLAGS: ${SRCDIR}/build-dev/lib/linux_arm64/libdefmt_decoder_wrapper.a -lgcc_s -lutil -lrt -lpthread -lm -ldl -lc
#cgo darwin,amd64 LDFLAGS: ${SRCDIR}/build-dev/lib/macos_amd64/libdefmt_decoder_wrapper.a -framework CoreFoundation -lc -lm
#cgo darwin,arm64 LDFLAGS: ${SRCDIR}/build-dev/lib/macos_arm64/libdefmt_decoder_wrapper.a -framework CoreFoundation -lc -lm
#cgo windows,amd64 LDFLAGS: ${SRCDIR}/build-dev/lib/windows_amd64/libdefmt_decoder_wrapper.a -lkernel32 -ladvapi32 -lntdll -luserenv -lws2_32 -ldbghelp -lsynchronization -lole32 -loleaut32
*/
import "C"

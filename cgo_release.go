//go:build !defmt_decoder_dev

// The default, and what users of the module get: links the libraries CI built and committed
// to build/ on the release PR. Local dev builds go to build-dev/ instead, see cgo_dev.go.

package defmt_decoder

/*
#cgo CFLAGS: -I${SRCDIR}/build/include/

// the static library is linked by its full path, so a stray .dylib/.so next to it can't be
// picked instead. The system libraries after it are what Rust's std needs, from
// `cargo rustc --release --target <triple> --crate-type staticlib -- --print native-static-libs`
#cgo linux,amd64 LDFLAGS: ${SRCDIR}/build/lib/linux_amd64/libdefmt_decoder_wrapper.a -lgcc_s -lutil -lrt -lpthread -lm -ldl -lc
#cgo linux,arm64 LDFLAGS: ${SRCDIR}/build/lib/linux_arm64/libdefmt_decoder_wrapper.a -lgcc_s -lutil -lrt -lpthread -lm -ldl -lc
#cgo darwin,amd64 LDFLAGS: ${SRCDIR}/build/lib/macos_amd64/libdefmt_decoder_wrapper.a -framework CoreFoundation -lc -lm
#cgo darwin,arm64 LDFLAGS: ${SRCDIR}/build/lib/macos_arm64/libdefmt_decoder_wrapper.a -framework CoreFoundation -lc -lm
#cgo windows,amd64 LDFLAGS: ${SRCDIR}/build/lib/windows_amd64/libdefmt_decoder_wrapper.a -lkernel32 -ladvapi32 -lntdll -luserenv -lws2_32 -ldbghelp -lsynchronization -lole32 -loleaut32
*/
import "C"

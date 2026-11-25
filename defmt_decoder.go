package defmt_decoder

/*
#cgo CFLAGS: -I${SRCDIR}/build/include/
#cgo LDFLAGS: -L${SRCDIR}/build/lib -ldefmt_decoder_wrapper -Wl,-rpath,${SRCDIR}/build/lib
#include "defmt_decoder_wrapper.h"
*/
import "C"

func Init() {}

func Decode() {}

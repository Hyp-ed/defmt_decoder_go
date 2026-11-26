package defmt_decoder

import "strings"

/*
#cgo CFLAGS: -I${SRCDIR}/build/include/
#cgo LDFLAGS: -L${SRCDIR}/build/lib -ldefmt_decoder_wrapper -Wl,-rpath,${SRCDIR}/build/lib
#include "defmt_decoder_wrapper.h"
*/
import "C"
import (
	"fmt"
	"unsafe"
)

// InitErrorCode is the overall result of Init.
type InitErrorCode int

const (
	// InitTwice means Init was already called; the new tables are discarded and the old ones kept.
	InitTwice InitErrorCode = -1
	// ErrorInAtLeastOneEntry means at least one path failed to load. The other paths were
	// loaded and can be decoded.
	ErrorInAtLeastOneEntry InitErrorCode = -2
)

// InitError is returned by Init. For ErrorInAtLeastOneEntry, err_codes[i] is the status of paths[i].
type InitError struct {
	code      InitErrorCode
	err_codes []InitEntryErrorCode
}

// InitEntryErrorCode is the load status of a single path passed to Init.
type InitEntryErrorCode int

const (
	NoError             InitEntryErrorCode = 0  // the file loaded successfully
	StringEncodingError InitEntryErrorCode = -1 // the path is not valid UTF-8
	ReadFileError       InitEntryErrorCode = -2 // the file could not be read
	TableParsingError   InitEntryErrorCode = -3 // the defmt table could not be parsed
	NoDefmtSectionFound InitEntryErrorCode = -4 // the file has no .defmt section
)

func (e *InitError) Error() string {
	switch e.code {
	case InitTwice:
		return "Init cannot be called twice"
	case ErrorInAtLeastOneEntry:
		var result strings.Builder
		result.WriteString("At least one entry has failed: error codes: [\n")
		for i := 0; i < len(e.err_codes); i++ {
			switch e.err_codes[i] {
			case NoError:
				fmt.Fprintf(&result, "\t%d: No error,", i)
			case StringEncodingError:
				fmt.Fprintf(&result, "\t%d: String encoding error,", i)
			case ReadFileError:
				fmt.Fprintf(&result, "\t%d: Read file error,", i)
			case TableParsingError:
				fmt.Fprintf(&result, "\t%d: Table parsing error,", i)
			case NoDefmtSectionFound:
				fmt.Fprintf(&result, "\t%d: String encoding error,", i)
			default:
				fmt.Fprintf(&result, "\t%d,", i)
			}
		}
		result.WriteString("]")
		return result.String()
	default:
		return "Unknown error"
	}
}

// Init loads the defmt tables of the ELF files at paths. It must be called exactly once,
// before Decode. The position of a path in paths is the index later passed to Decode.
//
// Returns nil if every path loaded.
func Init(paths []string) *InitError {
	raw_paths := make([]*C.char, len(paths))

	for i := range paths {
		raw_paths[i] = C.CString(paths[i])
		defer C.free(unsafe.Pointer(raw_paths[i]))
	}

	raw_paths_start := &raw_paths[0]

	result := make([]C.int32_t, len(paths))

	code := C.defmt_decoder_ctx_init(raw_paths_start, C.size_t(len(raw_paths)), &result[0], C.size_t(len(result)))

	if code == 0 {
		return nil
	}

	if InitErrorCode(code) == InitTwice {
		return new(InitError{InitTwice, make([]InitEntryErrorCode, 0)})
	}

	error := new(InitError{ErrorInAtLeastOneEntry, make([]InitEntryErrorCode, 0)})

	for _, err_code := range result {
		error.err_codes = append(error.err_codes, InitEntryErrorCode(err_code))
	}

	return error
}

// DecodeError is returned by Decode.
type DecodeError struct {
	code DecodeErrorCode
}

// DecodeErrorCode is the reason Decode failed.
type DecodeErrorCode int

const (
	EntryFailedToInitialize DecodeErrorCode = -1 // the file at index failed to load in Init
	CalledBeforeInit        DecodeErrorCode = -2 // Init has not been called yet
	UnexpectedEOF           DecodeErrorCode = -3 // input ends mid-frame; retry once more bytes are available
	Malformed               DecodeErrorCode = -4 // input does not start with a valid frame
	IndexOutOfBounds        DecodeErrorCode = -5 // index is not smaller than the number of paths passed to Init
)

// Decode decodes the defmt frame at the start of input with the table of the file at index
// (its position in the paths passed to Init). If location info is available, a
// "\n└─ module @ file:line" line is appended. If colored is true, the log level is wrapped in
// ANSI color codes.
//
// Returns the formatted frame and the number of input bytes it occupied, so the next frame
// starts at input[consumed:].
func Decode(index int, input []uint8, colored bool) (string, int, *DecodeError) {
	const BUFFER_SIZE int = 32
	output := make([]C.uint8_t, BUFFER_SIZE)
	consumed := C.size_t(0)

	result := C.defmt_decoder_decode(C.size_t(index), (*_Ctype_uint8_t)(&input[0]), C.size_t(len(input)), &output[0], C.size_t(len(output)), &consumed, C.bool(colored))

	if result < 0 {
		error_code := DecodeErrorCode(result)
		return "", 0, new(DecodeError{error_code})
	}

	if int(result) > len(output) {
		output = make([]C.uint8_t, result)
		result = C.defmt_decoder_decode(
			C.size_t(index),
			(*_Ctype_uint8_t)(&input[0]),
			C.size_t(len(input)),
			&output[0],
			C.size_t(len(output)),
			&consumed,
			C.bool(colored),
		)

		if result < 0 {
			error_code := DecodeErrorCode(result)
			return "", 0, new(DecodeError{error_code})
		}
	}

	return string(output), int(consumed), nil
}

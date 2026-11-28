package defmt_decoder

/*
#include "defmt_decoder_wrapper.h"
*/
import "C"
import (
	"fmt"
	"io"
	"log"
	"math"
	"strings"
	"sync"
	"unsafe"
)

// Decoder is the defmt stream of one ELF file passed to Init. Raw bytes from the target are
// written to it, and the decoded frames are read back as text, one frame per line: the
// message, then a "└─ module @ file:line" line if location info is available, each ending in
// "\n". Wrap it in a bufio.Scanner to get one line at a time.
//
// Read blocks until a frame is available or Close is called. A Decoder is safe for concurrent
// use, typically one goroutine writing and one reading. Once you close the stream, it cannot
// be openned nor initialized again.
type Decoder struct {
	// this field is never going to be changed, and the underlying rust implementation has a mutex to
	// guard the array element this index is referring to.
	idx    int
	mu     sync.Mutex
	cond   *sync.Cond // signalled by Write and Close, waited on by Read
	closed bool
}

func newDecoder(idx int) *Decoder {
	d := &Decoder{idx: idx}
	d.cond = sync.NewCond(&d.mu)
	return d
}

// Write adds raw bytes from the target to the stream. p doesn't need to start or end at a
// frame boundary. It always takes all of p unless it returns an error, which is a
// *DecodeError, or io.ErrClosedPipe after Close.
func (d *Decoder) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return 0, io.ErrClosedPipe
	}

	// the C side counts in an int32
	for written := 0; written < len(p); {
		chunk := p[written:min(len(p), written+math.MaxInt32)]
		result := C.defmt_decoder_received(C.size_t(d.idx), (*C.uint8_t)(unsafe.Pointer(&chunk[0])), C.size_t(len(chunk)))

		if result < 0 {
			return written, &DecodeError{DecodeErrorCode(result)}
		}
		written += len(chunk)
	}

	d.cond.Broadcast()

	return len(p), nil
}

// Read reads decoded text into p. It blocks until at least one byte is available, and returns
// io.EOF once Close was called and every complete frame has been read; bytes of an incomplete
// last frame are dropped. Corrupt frames are logged and skipped. A frame longer than p is
// split across Reads. Any other error is a *DecodeError.
func (d *Decoder) Read(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(p) == 0 {
		return 0, nil
	}

	for {
		result := C.defmt_decoder_decode(C.size_t(d.idx), (*C.uint8_t)(unsafe.Pointer(&p[0])), C.size_t(len(p)))

		switch {
		case result > 0:
			return int(result), nil
		case DecodeErrorCode(result) == noFrameYet:
			if d.closed {
				return 0, io.EOF
			}

			d.cond.Wait() // releases mu until Write or Close
		case DecodeErrorCode(result) == malformedFrame:
			log.Printf("defmt_decoder: skipped a malformed frame on decoder %d", d.idx)
		default:
			return 0, &DecodeError{DecodeErrorCode(result)}
		}
	}
}

// Close ends the stream: a blocked Read returns io.EOF once every complete frame has been
// read, and later Writes fail with io.ErrClosedPipe. It always returns nil.
func (d *Decoder) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.closed = true

	d.cond.Broadcast()
	return nil
}

// InitErrorCode is the overall result of Init.
type InitErrorCode int

const (
	// InitTwice means Init was already called; the new tables are discarded and the old ones kept.
	InitTwice InitErrorCode = -1
	// ErrorInAtLeastOneEntry means at least one path failed to load. The other paths were
	// loaded and can be decoded.
	ErrorInAtLeastOneEntry InitErrorCode = -2
	// NoPaths means Init was called with no paths. Nothing is loaded and Init can be called again.
	NoPaths InitErrorCode = -3
	// InitPanicked means the Rust side panicked; the reason was printed to stderr. Nothing
	// usable was loaded.
	InitPanicked InitErrorCode = -6 // DEFMT_DECODER_PANICKED
)

// InitEntryErrorCode is the load status of a single path passed to Init.
type InitEntryErrorCode int

const (
	NoError             InitEntryErrorCode = 0  // the file loaded successfully
	StringEncodingError InitEntryErrorCode = -1 // the path is not valid UTF-8
	ReadFileError       InitEntryErrorCode = -2 // the file could not be read
	TableParsingError   InitEntryErrorCode = -3 // the defmt table could not be parsed
	NoDefmtSectionFound InitEntryErrorCode = -4 // the file has no .defmt section
)

func (c InitEntryErrorCode) String() string {
	switch c {
	case NoError:
		return "no error"
	case StringEncodingError:
		return "path is not valid UTF-8"
	case ReadFileError:
		return "file could not be read"
	case TableParsingError:
		return "defmt table could not be parsed"
	case NoDefmtSectionFound:
		return "no .defmt section found"
	default:
		return fmt.Sprintf("unknown error %d", int(c))
	}
}

// InitError is returned by Init.
type InitError struct {
	code       InitErrorCode
	entryCodes []InitEntryErrorCode
}

// Code returns why Init failed.
func (e *InitError) Code() InitErrorCode {
	return e.code
}

// EntryCodes returns the load status of each path passed to Init, in the same order, for
// ErrorInAtLeastOneEntry. It is empty for the other codes.
func (e *InitError) EntryCodes() []InitEntryErrorCode {
	return e.entryCodes
}

func (e *InitError) Error() string {
	switch e.code {
	case InitTwice:
		return "defmt_decoder: Init cannot be called twice"
	case ErrorInAtLeastOneEntry:
		var result strings.Builder
		result.WriteString("defmt_decoder: at least one path failed to load:")
		for i, code := range e.entryCodes {
			if code != NoError {
				fmt.Fprintf(&result, "\n\t%d: %s", i, code)
			}
		}
		return result.String()
	case NoPaths:
		return "defmt_decoder: Init must be called with at least one path"
	case InitPanicked:
		return "defmt_decoder: Init panicked on the Rust side, see stderr for the reason"
	default:
		return fmt.Sprintf("defmt_decoder: unknown Init error %d", int(e.code))
	}
}

// Init loads the defmt tables of the ELF files at paths and returns one Decoder per path, in
// the same order. It must be called exactly once.
//
// If some paths fail to load, it returns an *InitError with code ErrorInAtLeastOneEntry along
// with the decoders: the failed paths' entries are nil and the others can be used. For any
// other error the decoders are nil.
func Init(paths []string) ([]*Decoder, error) {
	if len(paths) == 0 {
		return nil, &InitError{code: NoPaths}
	}

	raw_paths := make([]*C.char, len(paths))

	for i := range paths {
		raw_paths[i] = C.CString(paths[i])
		defer C.free(unsafe.Pointer(raw_paths[i]))
	}

	result := make([]C.int32_t, len(paths))

	code := C.defmt_decoder_ctx_init(&raw_paths[0], C.size_t(len(raw_paths)), &result[0], C.size_t(len(result)))

	switch InitErrorCode(code) {
	case InitTwice, InitPanicked:
		return nil, &InitError{code: InitErrorCode(code)}
	}

	decoders := make([]*Decoder, len(paths))
	entryCodes := make([]InitEntryErrorCode, len(paths))

	for i, entryCode := range result {
		entryCodes[i] = InitEntryErrorCode(entryCode)
		if entryCodes[i] == NoError {
			decoders[i] = newDecoder(i)
		}
	}

	if code == 0 {
		return decoders, nil
	}

	return decoders, &InitError{code: ErrorInAtLeastOneEntry, entryCodes: entryCodes}
}

// DecodeErrorCode is the reason a Decoder's Read or Write failed.
type DecodeErrorCode int

const (
	EntryFailedToInitialize DecodeErrorCode = -1 // the Decoder's file failed to load in Init
	CalledBeforeInit        DecodeErrorCode = -2 // Init has not been called yet
	IndexOutOfBounds        DecodeErrorCode = -5 // the Decoder's index is not smaller than the number of paths passed to Init
	DecodePanicked          DecodeErrorCode = -6 // the Rust side panicked, the reason was printed to stderr (DEFMT_DECODER_PANICKED)

	// handled inside Read, never returned
	noFrameYet     DecodeErrorCode = -3 // no complete frame received yet
	malformedFrame DecodeErrorCode = -4 // the next frame was corrupt and has been skipped
)

// DecodeError is returned by a Decoder's Read and Write. Decoders returned by Init only hit it
// if the Rust side panicked (DecodePanicked) or disagrees with this package, so it indicates a
// bug rather than bad input.
type DecodeError struct {
	code DecodeErrorCode
}

// Code returns why the Read or Write failed.
func (e *DecodeError) Code() DecodeErrorCode {
	return e.code
}

func (e *DecodeError) Error() string {
	switch e.code {
	case EntryFailedToInitialize:
		return "defmt_decoder: the file of this decoder failed to load"
	case CalledBeforeInit:
		return "defmt_decoder: decoder used before Init"
	case IndexOutOfBounds:
		return "defmt_decoder: decoder index out of bounds"
	case DecodePanicked:
		return "defmt_decoder: panicked on the Rust side, see stderr for the reason"
	default:
		return fmt.Sprintf("defmt_decoder: unknown decode error %d", int(e.code))
	}
}

package defmt_decoder

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// Test data in testdata/ was captured from the renode_test firmware running in Renode:
//   - renode_test.elf: release build, rzcobs encoding, no debug info (so no location lines)
//   - defmt_stream.bin: rzcobs-encoded, 0x00-delimited frames sent over USART3
//   - expected.txt: one decoded line per frame (cross-checked with defmt-print)
//   - expected_lengths.tsv: "<byte length>\t<line>" for every line of expected.txt
//   - no_defmt_section.elf: renode_test.elf with the .defmt section removed (llvm-objcopy)
//   - raw_debug/: the same firmware built with raw encoding (defmt/encoding-raw) and debug info,
//     so frames come back to back with no delimiters and decode with a location line.
//     expected.txt is the defmt-print output
const (
	elfPath           = "testdata/renode_test.elf"
	streamPath        = "testdata/defmt_stream.bin"
	expectedPath      = "testdata/expected.txt"
	expectedLensPath  = "testdata/expected_lengths.tsv"
	noDefmtSectionElf = "testdata/no_defmt_section.elf"
	rawDebugElf       = "testdata/raw_debug/renode_test.elf"
	rawDebugStream    = "testdata/raw_debug/defmt_stream.bin"
	rawDebugExpected  = "testdata/raw_debug/expected.txt"
)

// traceFrame is the raw frame of `defmt::trace!("trace level")`, the same in both ELFs
var traceFrame = []byte{0x20, 0x00}

// Init can only succeed once per process, so TestMain calls it once with every kind of path,
// and the tests decode with these indices. Scenarios that need a fresh process (before Init,
// Init twice, ...) run in a subprocess, see runScenario.
const (
	idxGood = iota
	idxMissing
	idxNotElf
	idxTruncatedElf
	idxNoDefmtSection
	idxInvalidUTF8
	idxDirectory
	idxEmptyPath
	idxGoodDuplicate
	idxRawDebug
	numPaths
)

var expectedInitCodes = []InitEntryErrorCode{
	idxGood:           NoError,
	idxMissing:        ReadFileError,
	idxNotElf:         TableParsingError,
	idxTruncatedElf:   TableParsingError,
	idxNoDefmtSection: NoDefmtSectionFound,
	idxInvalidUTF8:    StringEncodingError,
	idxDirectory:      ReadFileError,
	idxEmptyPath:      ReadFileError,
	idxGoodDuplicate:  NoError,
	idxRawDebug:       NoError,
}

var mainInitErr *InitError

// scenarioEnv selects a subprocess scenario, see runScenario
const scenarioEnv = "DEFMT_DECODER_TEST_SCENARIO"

func TestMain(m *testing.M) {
	if name := os.Getenv(scenarioEnv); name != "" {
		os.Exit(runScenarioInProcess(name))
	}

	tmp, err := os.MkdirTemp("", "defmt_decoder_test")
	if err != nil {
		panic(err)
	}
	truncated, err := writeTruncatedElf(tmp)
	if err != nil {
		panic(err)
	}

	paths := make([]string, numPaths)
	paths[idxGood] = elfPath
	paths[idxMissing] = "testdata/does_not_exist.elf"
	paths[idxNotElf] = expectedPath
	paths[idxTruncatedElf] = truncated
	paths[idxNoDefmtSection] = noDefmtSectionElf
	paths[idxInvalidUTF8] = "testdata/\xff\xfe.elf"
	paths[idxDirectory] = "testdata"
	paths[idxEmptyPath] = ""
	paths[idxGoodDuplicate] = elfPath
	paths[idxRawDebug] = rawDebugElf
	mainInitErr = Init(paths)

	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

// writeTruncatedElf writes the first 4KiB of the test ELF (valid header, cut off sections) to dir
func writeTruncatedElf(dir string) (string, error) {
	content, err := os.ReadFile(elfPath)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "truncated.elf")
	return path, os.WriteFile(path, content[:4096], 0o644)
}

// rzcobsDecode decodes one rzcobs-encoded frame (without the 0x00 delimiter) into raw defmt
// bytes. Port of rzcobs_decode from defmt-decoder's stream/rzcobs.rs. The result may carry
// trailing zero padding after the frame.
func rzcobsDecode(data []byte) ([]byte, error) {
	var res []byte
	i := len(data) - 1
	next := func() (byte, error) {
		if i < 0 {
			return 0, errors.New("rzcobs: unexpected end")
		}
		b := data[i]
		i--
		return b, nil
	}
	for i >= 0 {
		x, _ := next()
		switch {
		case x == 0:
			return nil, errors.New("rzcobs: zero byte inside frame")
		case x <= 0x7f:
			for k := 0; k < 7; k++ {
				if x&(1<<(6-k)) != 0 {
					res = append(res, 0)
					continue
				}
				b, err := next()
				if err != nil {
					return nil, err
				}
				res = append(res, b)
			}
		default:
			n := 134
			if x != 0xff {
				n = int(x&0x7f) + 7
				res = append(res, 0)
			}
			for k := 0; k < n; k++ {
				b, err := next()
				if err != nil {
					return nil, err
				}
				res = append(res, b)
			}
		}
	}
	for l, r := 0, len(res)-1; l < r; l, r = l+1, r-1 {
		res[l], res[r] = res[r], res[l]
	}
	return res, nil
}

type testFrame struct {
	raw      []byte // the raw defmt frame, exactly `consumed` bytes long
	expected string // expected output of Decode
}

// loadFrames returns the frames of defmt_stream.bin with their expected output
func loadFrames(t testing.TB) []testFrame {
	t.Helper()
	stream, err := os.ReadFile(streamPath)
	if err != nil {
		t.Fatal(err)
	}
	expected := readLines(t, expectedPath)

	var frames []testFrame
	for _, encoded := range bytes.Split(stream, []byte{0}) {
		if len(encoded) == 0 {
			continue
		}
		raw, err := rzcobsDecode(encoded)
		if err != nil {
			t.Fatalf("frame %d: %v", len(frames), err)
		}
		frames = append(frames, testFrame{raw: raw})
	}
	if len(frames) != len(expected) {
		t.Fatalf("stream has %d frames, %s has %d lines", len(frames), expectedPath, len(expected))
	}

	// rzcobs pads frames with zeros, cut each frame down to what the decoder consumes. The cut
	// off part must be padding only, otherwise consumed stopped in the middle of the frame
	for i := range frames {
		_, consumed, derr := Decode(idxGood, frames[i].raw)
		if derr != nil {
			t.Fatalf("frame %d (% x): %v", i, frames[i].raw, derr)
		}
		if consumed < 1 || consumed > len(frames[i].raw) {
			t.Fatalf("frame %d: consumed %d of %d bytes", i, consumed, len(frames[i].raw))
		}
		for _, b := range frames[i].raw[consumed:] {
			if b != 0 {
				t.Fatalf("frame %d: consumed %d of % x, but the rest is not zero padding", i, consumed, frames[i].raw)
			}
		}
		frames[i].raw = frames[i].raw[:consumed]
		frames[i].expected = expected[i]
	}
	return frames
}

func readLines(t testing.TB, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return lines
}

func assertDecode(t *testing.T, index int, input []byte, wantOut string, wantConsumed int) {
	t.Helper()
	out, consumed, err := Decode(index, input)
	if err != nil {
		t.Fatalf("Decode(% x): unexpected error %v", input, err)
	}
	if out != wantOut {
		t.Errorf("Decode(% x) = %q, want %q", input, out, wantOut)
	}
	if consumed != wantConsumed {
		t.Errorf("Decode(% x) consumed %d, want %d", input, consumed, wantConsumed)
	}
}

func assertDecodeError(t *testing.T, index int, input []byte, want DecodeErrorCode) {
	t.Helper()
	out, consumed, err := Decode(index, input)
	if err == nil {
		t.Fatalf("Decode(%d, % x) = %q, want error %d", index, input, out, want)
	}
	if err.code != want {
		t.Errorf("Decode(%d, % x) error %d (%v), want %d", index, input, err.code, err, want)
	}
	if out != "" || consumed != 0 {
		t.Errorf("Decode(%d, % x) returned %q, consumed %d alongside an error", index, input, out, consumed)
	}
}

// ---------------------------------------------------------------------------------------------
// Init
// ---------------------------------------------------------------------------------------------

func TestInitErrorCodes(t *testing.T) {
	if mainInitErr == nil {
		t.Fatal("Init succeeded although some paths are invalid")
	}
	if mainInitErr.code != ErrorInAtLeastOneEntry {
		t.Fatalf("Init code = %d, want ErrorInAtLeastOneEntry", mainInitErr.code)
	}
	if len(mainInitErr.err_codes) != numPaths {
		t.Fatalf("got %d entry codes, want %d", len(mainInitErr.err_codes), numPaths)
	}
	for i, want := range expectedInitCodes {
		if got := mainInitErr.err_codes[i]; got != want {
			t.Errorf("entry %d: code %d, want %d", i, got, want)
		}
	}
}

func TestInitErrorMessage(t *testing.T) {
	msg := mainInitErr.Error()
	for _, want := range []string{
		"0: No error",
		"1: Read file error",
		"2: Table parsing error",
		"4: No defmt section found",
		"5: String encoding error",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("Init error message %q does not contain %q", msg, want)
		}
	}

	cases := map[InitErrorCode]string{
		InitTwice: "Init cannot be called twice",
		NoPaths:   "Init must be called with at least one path",
		-100:      "Unknown error",
	}
	for code, want := range cases {
		if got := (&InitError{code: code}).Error(); got != want {
			t.Errorf("InitError{%d}.Error() = %q, want %q", code, got, want)
		}
	}
	if got := (&InitError{ErrorInAtLeastOneEntry, []InitEntryErrorCode{-100}}).Error(); !strings.Contains(got, "0,") {
		t.Errorf("unknown entry code not listed: %q", got)
	}
}

func TestInitTwiceInSameProcess(t *testing.T) {
	err := Init([]string{elfPath})
	if err == nil || err.code != InitTwice {
		t.Fatalf("second Init = %v, want InitTwice", err)
	}
	// the original tables are kept
	assertDecode(t, idxGood, traceFrame, "TRACE trace level", 2)
	assertDecodeError(t, idxMissing, traceFrame, EntryFailedToInitialize)
}

func TestInitNoPathsAfterInit(t *testing.T) {
	for _, paths := range [][]string{nil, {}} {
		if err := Init(paths); err == nil || err.code != NoPaths {
			t.Errorf("Init(%#v) = %v, want NoPaths", paths, err)
		}
	}
}

// Init and Decode return concrete pointer types. Stored in an `error` interface, a nil result
// is a non-nil error whose Error() panics on the nil receiver.
func TestNilErrorInInterfacePanics(t *testing.T) {
	var initErr error = (*InitError)(nil)
	var decodeErr error = (*DecodeError)(nil)
	for _, err := range []error{initErr, decodeErr} {
		if err == nil {
			t.Fatal("typed nil pointer in an error interface compared equal to nil")
		}
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%T(nil).Error() did not panic", err)
				}
			}()
			_ = err.Error()
		}()
	}
}

// ---------------------------------------------------------------------------------------------
// Decode: output
// ---------------------------------------------------------------------------------------------

func TestDecodeAllFrames(t *testing.T) {
	for i, f := range loadFrames(t) {
		out, consumed, err := Decode(idxGood, f.raw)
		if err != nil {
			t.Errorf("frame %d: %v", i, err)
			continue
		}
		if out != f.expected {
			t.Errorf("frame %d: got %q, want %q", i, out, f.expected)
		}
		if consumed != len(f.raw) {
			t.Errorf("frame %d: consumed %d, want %d", i, consumed, len(f.raw))
		}
		if !utf8.ValidString(out) {
			t.Errorf("frame %d: output is not valid UTF-8: %q", i, out)
		}
	}
}

// expected_lengths.tsv holds the byte length of each line, which matters for the output buffer
// handling (unicode lines have more bytes than characters)
func TestDecodeOutputLengths(t *testing.T) {
	frames := loadFrames(t)
	lines := readLines(t, expectedLensPath)
	if len(lines) != len(frames) {
		t.Fatalf("%s has %d lines, want %d", expectedLensPath, len(lines), len(frames))
	}
	for i, line := range lines {
		lenStr, text, ok := strings.Cut(line, "\t")
		want, err := strconv.Atoi(lenStr)
		if !ok || err != nil {
			t.Fatalf("%s:%d: malformed line %q", expectedLensPath, i+1, line)
		}
		if text != frames[i].expected {
			t.Fatalf("%s:%d: %q does not match %s", expectedLensPath, i+1, text, expectedPath)
		}
		out, _, derr := Decode(idxGood, frames[i].raw)
		if derr != nil {
			t.Fatalf("frame %d: %v", i, derr)
		}
		if len(out) != want {
			t.Errorf("frame %d: output is %d bytes, want %d (%q)", i, len(out), want, out)
		}
	}
}

// Decode starts with a 32 byte output buffer and retries with a bigger one if the frame does
// not fit. Outputs right at and around a buffer size (and the 64 byte frames, made for this)
// must decode intact.
func TestDecodeOutputBufferBoundaries(t *testing.T) {
	byLen := map[int][]testFrame{}
	for _, f := range loadFrames(t) {
		byLen[len(f.expected)] = append(byLen[len(f.expected)], f)
	}
	for _, n := range []int{31, 32, 33, 63, 64, 65, 132} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			if len(byLen[n]) == 0 {
				t.Skipf("no frame decodes to exactly %d bytes", n)
			}
			for _, f := range byLen[n] {
				assertDecode(t, idxGood, f.raw, f.expected, len(f.raw))
			}
		})
	}
	if got := len(byLen[64]); got != 7 {
		t.Errorf("found %d frames decoding to exactly 64 bytes, want the 7 EXACT64 frames", got)
	}
	if len(byLen[32]) == 0 {
		t.Error("no frame decodes to exactly 32 bytes (the initial buffer size)")
	}
}

func TestDecodeExact64(t *testing.T) {
	var n int
	for _, f := range loadFrames(t) {
		if !strings.Contains(f.expected, "EXACT64") {
			continue
		}
		n++
		out, consumed, err := Decode(idxGood, f.raw)
		if err != nil {
			t.Fatalf("%q: %v", f.expected, err)
		}
		if len(out) != 64 || out != f.expected || consumed != len(f.raw) {
			t.Errorf("got %q (%d bytes, consumed %d), want %q (64 bytes, consumed %d)",
				out, len(out), consumed, f.expected, len(f.raw))
		}
	}
	if n != 7 {
		t.Errorf("found %d EXACT64 frames, want 7", n)
	}
}

func TestDecodeDuplicatePath(t *testing.T) {
	for _, f := range loadFrames(t)[:20] {
		assertDecode(t, idxGoodDuplicate, f.raw, f.expected, len(f.raw))
	}
}

func TestDecodeDoesNotModifyInput(t *testing.T) {
	for _, f := range loadFrames(t) {
		input := append([]byte(nil), f.raw...)
		input = append(input, 0xaa, 0xbb)
		before := append([]byte(nil), input...)
		Decode(idxGood, input)
		if !bytes.Equal(input, before) {
			t.Fatalf("Decode modified its input: % x -> % x", before, input)
		}
	}
}

// ---------------------------------------------------------------------------------------------
// Decode: consumed and streams
// ---------------------------------------------------------------------------------------------

// All frames back to back, decoded by advancing by consumed
func TestDecodeConcatenatedStream(t *testing.T) {
	frames := loadFrames(t)
	var stream []byte
	for _, f := range frames {
		stream = append(stream, f.raw...)
	}

	input := stream
	for i, f := range frames {
		out, consumed, err := Decode(idxGood, input)
		if err != nil {
			t.Fatalf("frame %d at offset %d: %v", i, len(stream)-len(input), err)
		}
		if out != f.expected || consumed != len(f.raw) {
			t.Fatalf("frame %d: got %q consumed %d, want %q consumed %d", i, out, consumed, f.expected, len(f.raw))
		}
		input = input[consumed:]
	}
	if len(input) != 0 {
		t.Errorf("%d bytes left after decoding every frame", len(input))
	}
	assertDecodeError(t, idxGood, input, EmptyInput)
}

// One and a half frames: the first decodes, the half frame reports UnexpectedEOF without
// consuming anything, and decodes once the rest arrives
func TestDecodeOneAndAHalfFrames(t *testing.T) {
	frames := loadFrames(t)
	for i := 0; i+1 < len(frames); i++ {
		first, second := frames[i], frames[i+1]
		half := len(second.raw) / 2
		if half == 0 {
			half = 1
		}

		buf := append(append([]byte(nil), first.raw...), second.raw[:half]...)
		out, consumed, err := Decode(idxGood, buf)
		if err != nil {
			t.Fatalf("frame %d + half of %d: %v", i, i+1, err)
		}
		if out != first.expected || consumed != len(first.raw) {
			t.Fatalf("frame %d + half of %d: got %q consumed %d, want %q consumed %d",
				i, i+1, out, consumed, first.expected, len(first.raw))
		}

		buf = buf[consumed:]
		if half < len(second.raw) {
			assertDecodeError(t, idxGood, buf, UnexpectedEOF)
		}

		buf = append(buf, second.raw[half:]...)
		assertDecode(t, idxGood, buf, second.expected, len(second.raw))
	}
}

// Every strict prefix of a frame is incomplete
func TestDecodeEveryTruncation(t *testing.T) {
	for i, f := range loadFrames(t) {
		for n := 1; n < len(f.raw); n++ {
			out, consumed, err := Decode(idxGood, f.raw[:n])
			if err == nil {
				t.Errorf("frame %d (%q) cut to %d/%d bytes decoded to %q, consumed %d",
					i, f.expected, n, len(f.raw), out, consumed)
				continue
			}
			if err.code != UnexpectedEOF {
				t.Errorf("frame %d (%q) cut to %d/%d bytes: %v, want UnexpectedEOF",
					i, f.expected, n, len(f.raw), err)
			}
		}
	}
}

// Bytes after a frame are not consumed, whatever they are
func TestDecodeTrailingBytes(t *testing.T) {
	frames := loadFrames(t)
	for _, tail := range [][]byte{{0x00}, {0xff}, {0xff, 0xff}, bytes.Repeat([]byte{0}, 100), frames[0].raw} {
		for _, f := range frames {
			input := append(append([]byte(nil), f.raw...), tail...)
			assertDecode(t, idxGood, input, f.expected, len(f.raw))
		}
	}
}

func TestDecodeConcurrent(t *testing.T) {
	frames := loadFrames(t)
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for r := 0; r < 20; r++ {
				for i := g % len(frames); i < len(frames); i += 3 {
					out, consumed, err := Decode(idxGood, frames[i].raw)
					if err != nil || out != frames[i].expected || consumed != len(frames[i].raw) {
						errs <- fmt.Errorf("goroutine %d frame %d: %q %d %v", g, i, out, consumed, err)
						return
					}
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// ---------------------------------------------------------------------------------------------
// Decode: raw stream with location info
// ---------------------------------------------------------------------------------------------

// loadRawDebugExpected parses defmt-print output into one expected Decode output per frame.
// defmt-print pads the level to 5 characters and ends location lines with a space, Decode
// does neither
func loadRawDebugExpected(t testing.TB) []string {
	t.Helper()
	var frames []string
	for _, line := range readLines(t, rawDebugExpected) {
		if strings.HasPrefix(line, "└─ ") {
			if len(frames) == 0 {
				t.Fatalf("%s starts with a location line", rawDebugExpected)
			}
			frames[len(frames)-1] += "\n" + strings.TrimSuffix(line, " ")
			continue
		}
		for _, level := range []string{"TRACE", "DEBUG", "INFO", "WARN", "ERROR"} {
			if rest, ok := strings.CutPrefix(line, level+" "); ok {
				line = level + " " + strings.TrimLeft(rest, " ")
				break
			}
		}
		frames = append(frames, line)
	}
	return frames
}

// matchesDefmtPrint reports whether a Decode output matches a defmt-print frame. defmt-print
// leaves out the location of println! frames, Decode does not
func matchesDefmtPrint(out, want string) bool {
	if strings.Contains(want, "\n└─ ") {
		return out == want
	}
	text, _, _ := strings.Cut(out, "\n└─ ")
	return text == want
}

func TestDecodeRawStreamWithLocations(t *testing.T) {
	stream, err := os.ReadFile(rawDebugStream)
	if err != nil {
		t.Fatal(err)
	}
	expected := loadRawDebugExpected(t)

	input := stream
	var withLocation int
	for i, want := range expected {
		out, consumed, derr := Decode(idxRawDebug, input)
		if derr != nil {
			t.Fatalf("frame %d at offset %d: %v", i, len(stream)-len(input), derr)
		}
		if !matchesDefmtPrint(out, want) {
			t.Errorf("frame %d: got %q, want %q", i, out, want)
		}
		if strings.Contains(out, "\n└─ ") {
			withLocation++
		}
		input = input[consumed:]
	}
	if len(input) != 0 {
		t.Errorf("%d bytes left after decoding %d frames", len(input), len(expected))
	}
	if withLocation != len(expected) {
		t.Errorf("%d of %d frames have a location line, want all", withLocation, len(expected))
	}
}

// The same bytes decoded with the ELF without debug info give the same text, minus location
func TestDecodeWithoutLocations(t *testing.T) {
	stream, err := os.ReadFile(rawDebugStream)
	if err != nil {
		t.Fatal(err)
	}
	for input := stream; len(input) > 0; {
		with, consumed, derr := Decode(idxRawDebug, input)
		if derr != nil {
			t.Fatal(derr)
		}
		without, consumed2, derr := Decode(idxGood, input)
		if derr != nil {
			t.Fatal(derr)
		}
		text, _, _ := strings.Cut(with, "\n└─ ")
		if without != text || consumed != consumed2 {
			t.Fatalf("without locations got %q consumed %d, want %q consumed %d", without, consumed2, text, consumed)
		}
		input = input[consumed:]
	}
}

// Feed the raw stream in chunks of every size, like reads from a serial port: frames split
// across chunks report UnexpectedEOF until the rest arrives
func TestDecodeRawStreamChunked(t *testing.T) {
	stream, err := os.ReadFile(rawDebugStream)
	if err != nil {
		t.Fatal(err)
	}
	expected := loadRawDebugExpected(t)

	for _, chunk := range []int{1, 2, 3, 5, 7, 16, 64, 1000} {
		var buf, pending []byte
		var got []string
		for off := 0; off < len(stream); off += chunk {
			buf = append(buf, stream[off:min(off+chunk, len(stream))]...)
			for len(buf) > 0 {
				out, consumed, derr := Decode(idxRawDebug, buf)
				if derr != nil {
					if derr.code != UnexpectedEOF {
						t.Fatalf("chunk size %d, offset %d: %v", chunk, off, derr)
					}
					break
				}
				got = append(got, out)
				buf = buf[consumed:]
			}
		}
		pending = buf
		if len(pending) != 0 {
			t.Errorf("chunk size %d: %d bytes left over", chunk, len(pending))
		}
		if len(got) != len(expected) {
			t.Fatalf("chunk size %d: decoded %d frames, want %d", chunk, len(got), len(expected))
		}
		for i := range got {
			if !matchesDefmtPrint(got[i], expected[i]) {
				t.Errorf("chunk size %d, frame %d: got %q, want %q", chunk, i, got[i], expected[i])
			}
		}
	}
}

// The raw stream cut in the middle of a frame: everything before decodes, the cut frame is
// UnexpectedEOF and nothing is consumed
func TestDecodeRawStreamCutMidFrame(t *testing.T) {
	stream, err := os.ReadFile(rawDebugStream)
	if err != nil {
		t.Fatal(err)
	}
	// offsets of every frame boundary
	var bounds []int
	for off := 0; off < len(stream); {
		bounds = append(bounds, off)
		_, consumed, derr := Decode(idxRawDebug, stream[off:])
		if derr != nil {
			t.Fatal(derr)
		}
		off += consumed
	}
	bounds = append(bounds, len(stream))

	for b := 0; b+1 < len(bounds); b++ {
		start, end := bounds[b], bounds[b+1]
		if end-start < 2 {
			continue
		}
		cut := stream[:start+(end-start)/2]
		input := cut
		for n := 0; n < b; n++ {
			_, consumed, derr := Decode(idxRawDebug, input)
			if derr != nil {
				t.Fatalf("cut in frame %d: frame %d: %v", b, n, derr)
			}
			input = input[consumed:]
		}
		assertDecodeError(t, idxRawDebug, input, UnexpectedEOF)
	}
}

// ---------------------------------------------------------------------------------------------
// Decode: errors
// ---------------------------------------------------------------------------------------------

func TestDecodeErrorCodes(t *testing.T) {
	frame := traceFrame

	t.Run("EmptyInput", func(t *testing.T) {
		assertDecodeError(t, idxGood, nil, EmptyInput)
		assertDecodeError(t, idxGood, []byte{}, EmptyInput)
		assertDecodeError(t, idxGood, frame[:0], EmptyInput)
		// checked before the index
		assertDecodeError(t, numPaths, nil, EmptyInput)
	})

	t.Run("IndexOutOfBounds", func(t *testing.T) {
		for _, idx := range []int{numPaths, numPaths + 1, 1 << 20, int(^uint(0) >> 1), -1, -numPaths} {
			assertDecodeError(t, idx, frame, IndexOutOfBounds)
		}
	})

	t.Run("EntryFailedToInitialize", func(t *testing.T) {
		for idx, code := range expectedInitCodes {
			if code != NoError {
				assertDecodeError(t, idx, frame, EntryFailedToInitialize)
			}
		}
	})

	t.Run("UnexpectedEOF", func(t *testing.T) {
		assertDecodeError(t, idxGood, []byte{0x01}, UnexpectedEOF)
		assertDecodeError(t, idxGood, []byte{0x00}, UnexpectedEOF)
	})

	t.Run("Malformed", func(t *testing.T) {
		// indices with no entry in the table
		for _, input := range [][]byte{{0x00, 0x00}, {0xff, 0xff}, {0xfe, 0x7f, 1, 2, 3}} {
			assertDecodeError(t, idxGood, input, Malformed)
		}
	})
}

func TestDecodeErrorMessage(t *testing.T) {
	cases := map[DecodeErrorCode]string{
		EntryFailedToInitialize: "The entry at this index failed to initialize",
		CalledBeforeInit:        "Decode cannot be called before Init",
		UnexpectedEOF:           "Unexpected EOF: input ends in the middle of a frame",
		Malformed:               "Malformed frame",
		IndexOutOfBounds:        "Index out of bounds",
		EmptyInput:              "Input cannot be empty",
		-100:                    "Unknown error",
	}
	for code, want := range cases {
		if got := (&DecodeError{code}).Error(); got != want {
			t.Errorf("DecodeError{%d}.Error() = %q, want %q", code, got, want)
		}
	}
}

// Random input must never crash the process (a Rust panic across the FFI aborts it). Run with
// `go test -fuzz=FuzzDecode` to search beyond the seed corpus.
func FuzzDecode(f *testing.F) {
	for _, fr := range loadFrames(f) {
		f.Add(fr.raw)
	}
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})
	f.Fuzz(func(t *testing.T, input []byte) {
		out, consumed, err := Decode(idxGood, input)
		if err != nil {
			if out != "" || consumed != 0 {
				t.Fatalf("error %v with output %q, consumed %d", err, out, consumed)
			}
			return
		}
		if consumed < 1 || consumed > len(input) {
			t.Fatalf("consumed %d of %d bytes", consumed, len(input))
		}
		if !utf8.ValidString(out) {
			t.Fatalf("output is not valid UTF-8: %q", out)
		}
		// decoding is deterministic and only depends on the consumed bytes
		out2, consumed2, err2 := Decode(idxGood, input[:consumed])
		if err2 != nil || out2 != out || consumed2 != consumed {
			t.Fatalf("decoding the consumed prefix gave %q %d %v, want %q %d", out2, consumed2, err2, out, consumed)
		}
	})
}

// ---------------------------------------------------------------------------------------------
// Scenarios that need a process where Init has not been called yet
// ---------------------------------------------------------------------------------------------

// runScenario runs a scenario in a fresh test process and fails t if it does
func runScenario(t *testing.T, name string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), scenarioEnv+"="+name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("scenario %s failed: %v\n%s", name, err, out)
	}
}

type scenarioChecker struct{ failed bool }

func (c *scenarioChecker) check(ok bool, format string, args ...any) {
	if !ok {
		c.failed = true
		fmt.Printf("FAIL: "+format+"\n", args...)
	}
}

func (c *scenarioChecker) checkDecodeError(index int, input []byte, want DecodeErrorCode) {
	out, consumed, err := Decode(index, input)
	c.check(err != nil && err.code == want && out == "" && consumed == 0,
		"Decode(%d, % x) = %q, %d, %v, want error %d", index, input, out, consumed, err, want)
}

func (c *scenarioChecker) checkDecode(index int, input []byte, want string) {
	out, consumed, err := Decode(index, input)
	c.check(err == nil && out == want && consumed == len(input),
		"Decode(%d, % x) = %q, %d, %v, want %q", index, input, out, consumed, err, want)
}

var scenarios = map[string]func(c *scenarioChecker){
	// Decode before Init fails, but does not break a later Init
	"decode_before_init": func(c *scenarioChecker) {
		c.checkDecodeError(0, traceFrame, CalledBeforeInit)
		c.checkDecodeError(5, traceFrame, CalledBeforeInit)
		c.checkDecodeError(0, nil, EmptyInput)
		c.check(Init([]string{elfPath}) == nil, "Init after Decode failed")
		c.checkDecode(0, traceFrame, "TRACE trace level")
	},
	// NoPaths loads nothing, so Init can still be called afterwards
	"no_paths_then_init": func(c *scenarioChecker) {
		err := Init(nil)
		c.check(err != nil && err.code == NoPaths, "Init(nil) = %v, want NoPaths", err)
		err = Init([]string{})
		c.check(err != nil && err.code == NoPaths, "Init([]) = %v, want NoPaths", err)
		c.checkDecodeError(0, traceFrame, CalledBeforeInit)
		c.check(Init([]string{elfPath}) == nil, "Init after NoPaths failed")
		c.checkDecode(0, traceFrame, "TRACE trace level")
	},
	// Init twice: InitTwice takes precedence over entry errors, and the first tables are kept
	"init_twice": func(c *scenarioChecker) {
		c.check(Init([]string{elfPath}) == nil, "first Init failed")
		err := Init([]string{"testdata/does_not_exist.elf", noDefmtSectionElf})
		c.check(err != nil && err.code == InitTwice, "second Init = %v, want InitTwice", err)
		err = Init([]string{elfPath})
		c.check(err != nil && err.code == InitTwice, "third Init = %v, want InitTwice", err)
		c.checkDecode(0, traceFrame, "TRACE trace level")
		c.checkDecodeError(1, traceFrame, IndexOutOfBounds)
	},
	// An Init where every path fails still counts as the one Init
	"all_entries_fail": func(c *scenarioChecker) {
		err := Init([]string{"testdata/does_not_exist.elf", noDefmtSectionElf})
		c.check(err != nil && err.code == ErrorInAtLeastOneEntry, "Init = %v, want ErrorInAtLeastOneEntry", err)
		if err != nil {
			c.check(len(err.err_codes) == 2 && err.err_codes[0] == ReadFileError && err.err_codes[1] == NoDefmtSectionFound,
				"entry codes = %v", err.err_codes)
		}
		c.checkDecodeError(0, traceFrame, EntryFailedToInitialize)
		c.checkDecodeError(1, traceFrame, EntryFailedToInitialize)
		c.checkDecodeError(2, traceFrame, IndexOutOfBounds)
		err = Init([]string{elfPath})
		c.check(err != nil && err.code == InitTwice, "Init after failed Init = %v, want InitTwice", err)
	},
	// Go strings may contain NUL, C strings end at the first one. C.CString does not reject
	// them, so the path is silently cut off at the NUL and the truncated path is loaded
	"nul_in_path": func(c *scenarioChecker) {
		err := Init([]string{elfPath + "\x00garbage", "testdata/does_not_exist.elf\x00" + elfPath})
		c.check(err != nil && err.code == ErrorInAtLeastOneEntry, "Init = %v, want ErrorInAtLeastOneEntry", err)
		if err != nil {
			c.check(len(err.err_codes) == 2 && err.err_codes[0] == NoError && err.err_codes[1] == ReadFileError,
				"entry codes = %v, want [NoError ReadFileError]", err.err_codes)
		}
		c.checkDecode(0, traceFrame, "TRACE trace level")
	},
	// Found by FuzzDecode: the iso8601 frame with an out of range timestamp makes defmt-decoder
	// unwrap an Err (frame.rs:548). A panic cannot unwind out of the extern "C" function, so
	// Rust aborts the whole Go process instead of Decode returning an error
	"rust_panic_on_bad_timestamp": func(c *scenarioChecker) {
		c.check(Init([]string{elfPath}) == nil, "Init failed")
		input := []byte("L\x00\x12\x00\xfe\xff\x11\x00\x00\x00`@\x17\x00\x02\xff\xff\xfastr")
		_, _, err := Decode(0, input)
		c.check(err != nil, "Decode of an out of range timestamp succeeded")
	},
	// Many paths, including the same file many times
	"many_paths": func(c *scenarioChecker) {
		paths := make([]string, 1000)
		for i := range paths {
			paths[i] = elfPath
		}
		paths[500] = "testdata/does_not_exist.elf"
		err := Init(paths)
		c.check(err != nil && err.code == ErrorInAtLeastOneEntry, "Init = %v, want ErrorInAtLeastOneEntry", err)
		if err != nil {
			for i, code := range err.err_codes {
				want := NoError
				if i == 500 {
					want = ReadFileError
				}
				c.check(code == want, "entry %d: code %d, want %d", i, code, want)
			}
		}
		c.checkDecode(0, traceFrame, "TRACE trace level")
		c.checkDecode(999, traceFrame, "TRACE trace level")
		c.checkDecodeError(500, traceFrame, EntryFailedToInitialize)
		c.checkDecodeError(1000, traceFrame, IndexOutOfBounds)
	},
}

func runScenarioInProcess(name string) int {
	scenario, ok := scenarios[name]
	if !ok {
		fmt.Printf("unknown scenario %q\n", name)
		return 2
	}
	c := &scenarioChecker{}
	scenario(c)
	if c.failed {
		return 1
	}
	return 0
}

func TestScenarios(t *testing.T) {
	for name := range scenarios {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			runScenario(t, name)
		})
	}
}

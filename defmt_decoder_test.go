package defmt_decoder

import (
	"bufio"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

var decoders []*Decoder

func TestMain(m *testing.M) {
	paths := make([]string, 1)
	paths[0] = "./testdata/renode_test.elf"

	var err error
	decoders, err = Init(paths)

	if err != nil {
		println(err.Error())
	}

	code := m.Run()
	os.Exit(code)
}

func TestIncompleteStreamInput(t *testing.T) {
	file, err := os.Open("./testdata/defmt_stream.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()

	dec := decoders[0]

	go func() {
		buffer := make([]byte, 4)

		for {
			num_read, err := file.Read(buffer)

			if errors.Is(err, io.EOF) {
				break
			}

			_, _ = dec.Write(buffer[0:num_read])
		}

		_ = dec.Close()
	}()

	expected, err := os.ReadFile("./testdata/expected.txt")
	if err != nil {
		t.Fatal(err)
	}
	expectedLines := strings.Split(strings.TrimRight(string(expected), "\n"), "\n")

	var got []string
	scanner := bufio.NewScanner(dec)
	for scanner.Scan() {
		// expected.txt has no location info
		if !strings.HasPrefix(scanner.Text(), "└─") {
			got = append(got, scanner.Text())
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}

	if len(got) != len(expectedLines) {
		t.Fatalf("got %d frames, expected %d", len(got), len(expectedLines))
	}
	for i := range got {
		got_str := strings.Trim(got[i], "\n\t ")
		expected_str := strings.Trim(expectedLines[i], "\n\r\t ")
		if got_str != expected_str {
			t.Errorf("frame %d: got %q, expected %q", i, got[i], expectedLines[i])
		}
	}

}

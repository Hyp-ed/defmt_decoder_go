// Decodes the test stream through the published module and checks the output against
// testdata/expected.txt, like TestSanityCheck but from outside the module.
//
// Usage: go run . <directory of github.com/Hyp-ed/defmt_decoder_go in the module cache>
package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	defmt "github.com/Hyp-ed/defmt_decoder_go"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: consumer <module dir>")
	}
	testdata := filepath.Join(os.Args[1], "testdata")

	decoders, err := defmt.Init([]string{filepath.Join(testdata, "renode_test.elf")})
	if err != nil {
		log.Fatal(err)
	}
	dec := decoders[0]

	stream, err := os.Open(filepath.Join(testdata, "defmt_stream.bin"))
	if err != nil {
		log.Fatal(err)
	}
	defer stream.Close()

	go func() {
		if _, err := io.Copy(dec, stream); err != nil {
			log.Fatal(err)
		}
		dec.Close()
	}()

	expected, err := os.ReadFile(filepath.Join(testdata, "expected.txt"))
	if err != nil {
		log.Fatal(err)
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
		log.Fatal(err)
	}

	if len(got) != len(expectedLines) {
		log.Fatalf("got %d frames, expected %d", len(got), len(expectedLines))
	}
	failed := false
	for i := range got {
		if strings.TrimSpace(got[i]) != strings.TrimSpace(expectedLines[i]) {
			fmt.Printf("frame %d: got %q, expected %q\n", i, got[i], expectedLines[i])
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
	fmt.Printf("decoded all %d frames as expected\n", len(got))
}

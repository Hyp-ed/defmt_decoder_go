package defmt_decoder

import (
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	paths := make([]string, 1)
	paths[0] = "./testdata/renode_test.elf"
	mainInitErr := Init(paths)

	if mainInitErr != nil {
		println(mainInitErr.Error())
	}

	code := m.Run()
	os.Exit(code)
}

func TestSanityCheck(t *testing.T) {
	file, err := os.Open("./testdata/defmt_stream.bin")

	if err != nil {
		t.Fatal(err)
	}

	bytes := make([]byte, 0x4)
	_, error := file.Read(bytes)

	if error != nil {
		t.Fatal(err)
	}

	fmt.Printf("%08x\n", bytes)

	str, _, err := Decode(0, bytes)

	if err != nil {
		t.Fatal(err)
	}

	fmt.Printf("%s", str)
}

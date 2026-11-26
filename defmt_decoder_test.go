package defmt_decoder

import (
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

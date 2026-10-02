//go:build unix

package luvus_test

import (
	"context"
	"os"
	"testing"

	"github.com/atqamz/hand/internal/luvus"
)

func TestKeepMakesTheCopyReadOnly(t *testing.T) {
	pin, err := luvus.Keep(context.Background(), t.TempDir(), fakeLuvus(t, "0.14.3", ""), nil, pinnedAt)
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(pin.Path); err != nil || fi.Mode().Perm() != 0o555 {
		t.Fatalf("copy mode = %v, %v", fi.Mode(), err)
	}
}

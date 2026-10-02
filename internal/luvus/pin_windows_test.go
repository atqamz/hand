package luvus_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/atqamz/hand/internal/luvus"
)

func TestKeepWantsAnExecutableExtension(t *testing.T) {
	bin := fakeLuvus(t, "0.14.3", "")
	bare := filepath.Join(t.TempDir(), "luvus")
	if err := os.Rename(bin, bare); err != nil {
		t.Fatal(err)
	}
	if _, err := luvus.Keep(context.Background(), t.TempDir(), bare, nil, pinnedAt); !errors.Is(err, luvus.ErrNotLuvus) {
		t.Fatalf("without .exe = %v", err)
	}
}

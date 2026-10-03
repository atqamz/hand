//go:build unix

package board_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTheInboxIsPrivate(t *testing.T) {
	fx := newFixture(t)
	saved(t, upload(fx.handler(), "shot.png", pngBytes, token))
	if fi, err := os.Stat(filepath.Join(fx.options.Home, "inbox")); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("inbox mode %v, %v", fi.Mode(), err)
	}
}

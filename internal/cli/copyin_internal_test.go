package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCopyInNeverReadsAFileSwappedForALinkToTheFleetDatabase(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("swapping a symlink in needs privileges on Windows")
	}
	home, ext := t.TempDir(), t.TempDir()
	db, other := filepath.Join(home, "hand.db"), filepath.Join(ext, "other-secret")
	for path, data := range map[string]string{db: "SECRET-DB", other: "SECRET-OTHER"} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	race, tmp := filepath.Join(ext, "race"), filepath.Join(ext, "tmp")
	swap := func(i int) {
		os.Remove(tmp)
		switch i % 3 {
		case 0:
			os.Symlink(db, tmp)
		case 1:
			os.Symlink(other, tmp)
		default:
			os.WriteFile(tmp, []byte("plain"), 0o600)
		}
		os.Rename(tmp, race)
	}
	swap(2)
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
				swap(i)
			}
		}
	}()
	r := &runner{env: Env{Now: func() time.Time { return time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC) }}, home: home}
	copied := 0
	for i := 0; i < 1500; i++ {
		name, err := r.copyIn(race)
		if err != nil {
			continue
		}
		copied++
		path := filepath.Join(home, "inbox", name)
		if b, _ := os.ReadFile(path); strings.Contains(string(b), "SECRET") {
			t.Fatalf("copyIn copied the fleet database: %q", name)
		}
		os.Remove(path)
	}
	close(stop)
	<-done
	if copied == 0 {
		t.Fatal("no copy ever succeeded, so the test proved nothing")
	}
}

package desktop_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
)

func TestDraftsWriteRestoreAndClear(t *testing.T) {
	dir := t.TempDir()
	d := desktop.Drafts{Dir: dir}
	if got := d.Load(fleetID); got != "" {
		t.Fatalf("a missing draft loaded %q", got)
	}
	if err := d.Save(fleetID, "line one\nline two"); err != nil {
		t.Fatal(err)
	}
	if got := d.Load(fleetID); got != "line one\nline two" {
		t.Fatalf("restored %q", got)
	}
	info, err := os.Stat(filepath.Join(dir, fleetID+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("draft mode = %v, want 0600", info.Mode().Perm())
	}
	if err := d.ClearIf(fleetID, "line one\nline two"); err != nil {
		t.Fatal(err)
	}
	if got := d.Load(fleetID); got != "" {
		t.Fatalf("a cleared draft loaded %q", got)
	}
	if err := d.ClearIf(fleetID, "line one\nline two"); err != nil {
		t.Fatalf("clearing again: %v", err)
	}
}

func TestSavingAnEmptyDraftRemovesTheFile(t *testing.T) {
	dir := t.TempDir()
	d := desktop.Drafts{Dir: dir}
	if err := d.Save(fleetID, "typed"); err != nil {
		t.Fatal(err)
	}
	if err := d.Save(fleetID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, fleetID+".txt")); !os.IsNotExist(err) {
		t.Fatalf("the empty draft is still on disk: %v", err)
	}
	if got := d.Load(fleetID); got != "" {
		t.Fatalf("loaded %q, want no draft", got)
	}
	if err := d.Save(fleetID, ""); err != nil {
		t.Fatalf("saving an empty draft twice: %v", err)
	}
}

func TestDraftsRefuseAnUnknownFleetAndAnOversizedDraft(t *testing.T) {
	d := desktop.Drafts{Dir: t.TempDir()}
	if err := d.Save("../elsewhere", "x"); err == nil {
		t.Fatal("saved a draft for an id that is not a fleet")
	}
	if err := d.Save(fleetID, strings.Repeat("x", 65<<10)); err == nil {
		t.Fatal("saved an oversized draft")
	}
	if got := d.Load("../elsewhere"); got != "" {
		t.Fatalf("loaded %q for an id that is not a fleet", got)
	}
}

func TestDraftsClearOnlyTheTextThatWasSent(t *testing.T) {
	d := desktop.Drafts{Dir: t.TempDir()}
	if err := d.Save(fleetID, "a newer draft"); err != nil {
		t.Fatal(err)
	}
	if err := d.ClearIf(fleetID, "the sent text"); err != nil {
		t.Fatal(err)
	}
	if got := d.Load(fleetID); got != "a newer draft" {
		t.Fatalf("ClearIf removed a newer draft: %q", got)
	}
	if err := d.ClearIf(fleetID, "a newer draft"); err != nil {
		t.Fatal(err)
	}
	if got := d.Load(fleetID); got != "" {
		t.Fatalf("ClearIf left the sent draft: %q", got)
	}
	if err := d.ClearIf(fleetID, "not there"); err != nil {
		t.Fatalf("ClearIf on a missing draft: %v", err)
	}
}

func TestDraftDirFollowsXDGState(t *testing.T) {
	if got, want := desktop.DraftDir(func(k string) string {
		switch k {
		case "XDG_STATE_HOME":
			return "/state"
		case "HOME":
			return "/home/op"
		}
		return ""
	}), filepath.Join("/state", "hand-desktop", "drafts"); got != want {
		t.Fatalf("DraftDir = %q, want %q", got, want)
	}
	if got, want := desktop.DraftDir(func(k string) string {
		if k == "HOME" {
			return "/home/op"
		}
		return ""
	}), filepath.Join("/home/op", ".local", "state", "hand-desktop", "drafts"); got != want {
		t.Fatalf("DraftDir without XDG = %q, want %q", got, want)
	}
}

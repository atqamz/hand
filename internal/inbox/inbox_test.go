package inbox

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

const day = 24 * time.Hour

func put(t *testing.T, path string, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, now.Add(-age), now.Add(-age)); err != nil {
		t.Fatal(err)
	}
}

func tree(t *testing.T, root string) map[string]time.Time {
	t.Helper()
	out := map[string]time.Time{}
	err := filepath.WalkDir(root, func(path string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		out[filepath.ToSlash(rel)] = info.ModTime()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func names(files []File) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Name
	}
	return out
}

func TestNameMatchesBothForms(t *testing.T) {
	for _, name := range []string{"20261019-101010-1.png", "20261019-101010-12.webp", "20261019-101010-3-report.pdf", "20261019-101010-1-my notes.tar.gz", "20261019-101010-2-shot.png.bin", "20261019-101010-1-Makefile", "20261019-101010-4-bashrc", "20261019-101010-5-.bashrc"} {
		if !Name.MatchString(name) {
			t.Errorf("%q does not match", name)
		}
	}
	for _, name := range []string{".upload-123", ".upload-20261019-101010-1.png", "notes.txt", "20261019-101010.png", "20261019-101010-1", "20261019-101010-1-", "20261019-101010-1.", "2026101-101010-1.png", "20261019-101010-1.png~", "x20261019-101010-1.png", ".trash", ".pruned", ".pruned.tmp-123"} {
		if Name.MatchString(name) {
			t.Errorf("%q matches", name)
		}
	}
}

func TestParseAgeTakesDaysOnly(t *testing.T) {
	for in, want := range map[string]time.Duration{"1d": day, "30d": 30 * day, "9999d": 9999 * day} {
		if got, ok := ParseAge(in); !ok || got != want {
			t.Errorf("ParseAge(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "0d", "30", "24h", "-1d", "01d", "10000d", "30D", "1.5d", " 30d"} {
		if _, ok := ParseAge(in); ok {
			t.Errorf("ParseAge(%q) accepted", in)
		}
	}
}

func TestMiBRoundsToOneDecimal(t *testing.T) {
	for n, want := range map[int64]string{0: "0 MiB", 5: "0 MiB", 400 << 10: "0.4 MiB", 27 << 20: "27 MiB", 27<<20 + 300<<10: "27.3 MiB"} {
		if got := MiB(n); got != want {
			t.Errorf("MiB(%d) = %q, want %q", n, got, want)
		}
	}
	if got := Count(1, 5); got != "1 file, 0 MiB" {
		t.Errorf("Count(1) = %q", got)
	}
	if got := Count(49, 27<<20); got != "49 files, 27 MiB" {
		t.Errorf("Count(49) = %q", got)
	}
}

func TestSizeOfAMissingAndAFullInbox(t *testing.T) {
	home := t.TempDir()
	if u, err := Size(home); err != nil || u != (Usage{}) {
		t.Fatalf("missing inbox = %+v, %v", u, err)
	}
	dir := filepath.Join(home, Folder)
	put(t, filepath.Join(dir, "20260901-000000-1.png"), 48*day)
	put(t, filepath.Join(dir, "20261019-110000-1-notes.txt"), time.Hour)
	put(t, filepath.Join(dir, "20261010-000000-1.png"), 9*day)
	put(t, filepath.Join(dir, "notes.txt"), 90*day)
	put(t, filepath.Join(dir, ".upload-123"), 90*day)
	put(t, filepath.Join(dir, "20250101-000000-1.png", "20250101-000000-1.png"), 300*day)
	put(t, filepath.Join(dir, Trash, "20260801-000000-1.png"), 2*day)
	u, err := Size(home)
	if err != nil || u.Files != 3 || u.Bytes != 15 || !u.Oldest.Equal(now.Add(-48*day)) || !u.Newest.Equal(now.Add(-time.Hour)) || u.TrashFiles != 1 || u.TrashBytes != 5 {
		t.Fatalf("full inbox = %+v, %v", u, err)
	}
}

func TestDryRunTouchesNothing(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, Folder)
	put(t, filepath.Join(dir, "20260901-000000-1.png"), 48*day)
	put(t, filepath.Join(dir, "20261019-110000-1.png"), time.Hour)
	put(t, filepath.Join(dir, Trash, "20260801-000000-1.png"), 8*day)
	put(t, filepath.Join(dir, Trash, "20260802-000000-1.png"), day)
	before := tree(t, home)
	p, err := Prune(home, now, 30*day, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names(p.Moved), []string{"20260901-000000-1.png"}) || !slices.Equal(names(p.Deleted), []string{"20260801-000000-1.png"}) || p.Failed != nil {
		t.Fatalf("dry run = %+v", p)
	}
	if after := tree(t, home); !mapsEqual(before, after) {
		t.Fatalf("dry run changed %v into %v", before, after)
	}
	empty := t.TempDir()
	if p, err := Prune(empty, now, day, true); err != nil || p.Moved != nil || p.Deleted != nil {
		t.Fatalf("dry run of no inbox = %+v, %v", p, err)
	}
	if des, _ := os.ReadDir(empty); len(des) != 0 {
		t.Fatalf("dry run of no inbox made %v", des)
	}
}

func mapsEqual(a, b map[string]time.Time) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || !w.Equal(v) {
			return false
		}
	}
	return true
}

func TestPruneMovesOnlyOldHandFilesDirectlyInside(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, Folder)
	trash := filepath.Join(dir, Trash)
	put(t, filepath.Join(dir, "20260901-000000-1.png"), 48*day)
	put(t, filepath.Join(dir, "20260902-000000-1-report.pdf"), 47*day)
	put(t, filepath.Join(dir, "20260903-000000-1-Makefile"), 46*day)
	put(t, filepath.Join(dir, "20261019-110000-1.png"), time.Hour)
	put(t, filepath.Join(dir, "20261019-110000-2-bashrc"), time.Hour)
	put(t, filepath.Join(dir, "20260920-000000-1.png"), 30*day)
	put(t, filepath.Join(dir, "notes.txt"), 90*day)
	put(t, filepath.Join(dir, ".upload-123"), 90*day)
	put(t, filepath.Join(dir, "20250101-000000-1.png", "20250101-000000-2.png"), 300*day)
	put(t, filepath.Join(home, "hand.db"), 90*day)
	if err := os.Symlink(filepath.Join("..", "hand.db"), filepath.Join(dir, "20250101-000000-3.png")); err != nil {
		t.Fatal(err)
	}
	p, err := Prune(home, now, 30*day, false)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"20260901-000000-1.png", "20260902-000000-1-report.pdf", "20260903-000000-1-Makefile"}; !slices.Equal(names(p.Moved), want) || p.Deleted != nil || p.Failed != nil {
		t.Fatalf("prune = %+v, want moved %v", p, want)
	}
	for _, name := range []string{"20261019-110000-1.png", "20261019-110000-2-bashrc", "20260920-000000-1.png", "notes.txt", ".upload-123", filepath.Join("20250101-000000-1.png", "20250101-000000-2.png"), "20250101-000000-3.png"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s left the inbox: %v", name, err)
		}
	}
	for _, name := range p.Moved {
		info, err := os.Lstat(filepath.Join(trash, name.Name))
		if err != nil || !info.Mode().IsRegular() || !info.ModTime().Equal(now) {
			t.Errorf("trash %s = %v, %v; want a regular file stamped %v", name.Name, info, err, now)
		}
	}
	if info, err := os.Lstat(filepath.Join(home, "hand.db")); err != nil || !info.ModTime().Equal(now.Add(-90*day)) {
		t.Fatalf("symlink target touched: %v, %v", info, err)
	}
	if info, err := os.Stat(trash); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o700) {
		t.Fatalf("trash = %v, %v", info, err)
	}
}

func TestPruneExpiresTrashAfterSevenDays(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, Folder)
	put(t, filepath.Join(dir, "20260901-000000-1.png"), 48*day)
	put(t, filepath.Join(dir, Trash, "notes.txt"), 90*day)
	put(t, filepath.Join(dir, Trash, "20250101-000000-1.png", "20250101-000000-2.png"), 300*day)
	if _, err := Prune(home, now, 30*day, false); err != nil {
		t.Fatal(err)
	}
	if p, err := Prune(home, now.Add(TrashKeep), 30*day, false); err != nil || p.Deleted != nil || p.Moved != nil {
		t.Fatalf("at 7 days = %+v, %v", p, err)
	}
	p, err := Prune(home, now.Add(TrashKeep+time.Hour), 30*day, false)
	if err != nil || !slices.Equal(names(p.Deleted), []string{"20260901-000000-1.png"}) || p.Moved != nil || p.Failed != nil {
		t.Fatalf("after 7 days = %+v, %v", p, err)
	}
	if got := Bytes(p.Deleted); got != 5 {
		t.Fatalf("freed %d bytes", got)
	}
	if _, err := os.Lstat(filepath.Join(dir, Trash, "20260901-000000-1.png")); !os.IsNotExist(err) {
		t.Fatalf("expired file still in the trash: %v", err)
	}
	for _, name := range []string{"notes.txt", filepath.Join("20250101-000000-1.png", "20250101-000000-2.png")} {
		if _, err := os.Lstat(filepath.Join(dir, Trash, name)); err != nil {
			t.Errorf("trash %s removed: %v", name, err)
		}
	}
}

func TestPruneTwiceChangesNothingMore(t *testing.T) {
	home := t.TempDir()
	put(t, filepath.Join(home, Folder, "20260901-000000-1.png"), 48*day)
	if p, err := Prune(home, now, 30*day, false); err != nil || len(p.Moved) != 1 {
		t.Fatalf("first = %+v, %v", p, err)
	}
	before := tree(t, home)
	if p, err := Prune(home, now, 30*day, false); err != nil || p.Moved != nil || p.Deleted != nil || p.Failed != nil {
		t.Fatalf("second = %+v, %v", p, err)
	}
	if after := tree(t, home); !mapsEqual(before, after) {
		t.Fatalf("second run changed %v into %v", before, after)
	}
	young := t.TempDir()
	put(t, filepath.Join(young, Folder, "20261019-110000-1.png"), time.Hour)
	if p, err := Prune(young, now, day, false); err != nil || p.Moved != nil {
		t.Fatalf("young = %+v, %v", p, err)
	}
	if _, err := os.Lstat(filepath.Join(young, Folder, Trash)); !os.IsNotExist(err) {
		t.Fatalf("nothing to move still made the trash: %v", err)
	}
}

func TestAFailedMoveDoesNotStopTheRest(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, Folder)
	put(t, filepath.Join(dir, "20260901-000000-1.png"), 48*day)
	put(t, filepath.Join(dir, "20260902-000000-1.png"), 47*day)
	put(t, filepath.Join(dir, "20260903-000000-1.png"), 46*day)
	put(t, filepath.Join(dir, "20260904-000000-1.png"), 45*day)
	put(t, filepath.Join(dir, Trash, "20260902-000000-1.png", "child"), time.Hour)
	put(t, filepath.Join(dir, Trash, "20260904-000000-1.png"), time.Hour)
	p, err := Prune(home, now, 30*day, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names(p.Moved), []string{"20260901-000000-1.png", "20260903-000000-1.png"}) || len(p.Failed) != 2 || p.Failed[0].Name != "20260902-000000-1.png" || p.Failed[1].Name != "20260904-000000-1.png" || p.Failed[1].Err.Error() != "a file of that name is already in the trash" {
		t.Fatalf("prune = %+v", p)
	}
	for name, age := range map[string]time.Duration{"20260902-000000-1.png": 47 * day, "20260904-000000-1.png": 45 * day, filepath.Join(Trash, "20260904-000000-1.png"): time.Hour} {
		info, err := os.Lstat(filepath.Join(dir, name))
		if err != nil || !info.ModTime().Equal(now.Add(-age)) {
			t.Fatalf("%s = %v, %v; want it in place with its own time", name, info, err)
		}
	}
}

func TestATrashThatIsNotAFolderIsRefused(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, Folder)
	put(t, filepath.Join(dir, "20260901-000000-1.png"), 48*day)
	elsewhere := t.TempDir()
	put(t, filepath.Join(elsewhere, "20250101-000000-1.png"), 300*day)
	if err := os.Symlink(elsewhere, filepath.Join(dir, Trash)); err != nil {
		t.Fatal(err)
	}
	for _, dry := range []bool{true, false} {
		if p, err := Prune(home, now, 30*day, dry); err == nil {
			t.Fatalf("dry=%v symlinked trash = %+v", dry, p)
		}
	}
	if _, err := Size(home); err == nil {
		t.Fatal("size took a symlinked trash")
	}
	if err := Mark(home, "20261019"); err == nil {
		t.Fatal("mark took a symlinked trash")
	}
	if _, err := os.Lstat(filepath.Join(elsewhere, "20250101-000000-1.png")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "20260901-000000-1.png")); err != nil {
		t.Fatal(err)
	}
}

func TestMarkRemembersTheDay(t *testing.T) {
	home := t.TempDir()
	if err := Mark(home, "20261019"); err != nil {
		t.Fatal(err)
	}
	if des, _ := os.ReadDir(home); len(des) != 0 || Marked(home, "20261019") {
		t.Fatalf("mark without an inbox made %v", des)
	}
	if err := os.Mkdir(filepath.Join(home, Folder), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Mark(home, "20261019"); err != nil {
		t.Fatal(err)
	}
	if !Marked(home, "20261019") || Marked(home, "20261020") {
		t.Fatal("marker does not hold the day")
	}
	if u, err := Size(home); err != nil || u != (Usage{}) {
		t.Fatalf("marker counted: %+v, %v", u, err)
	}
}

func TestAFailedRenameGivesTheFileItsTimeBack(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a trash folder this user cannot write to")
	}
	home := t.TempDir()
	dir := filepath.Join(home, Folder)
	trash := filepath.Join(dir, Trash)
	put(t, filepath.Join(dir, "20260901-000000-1.png"), 48*day)
	if err := os.MkdirAll(trash, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(trash, 0o700) })
	p, err := Prune(home, now, 30*day, false)
	if err != nil || p.Moved != nil || len(p.Failed) != 1 || !errors.Is(p.Failed[0].Err, fs.ErrPermission) {
		t.Fatalf("prune = %+v, %v", p, err)
	}
	info, err := os.Lstat(filepath.Join(dir, "20260901-000000-1.png"))
	if err != nil || !info.ModTime().Equal(now.Add(-48*day)) {
		t.Fatalf("file = %v, %v; want it in place with its own time", info, err)
	}
}

func TestASymlinkedInboxIsRefused(t *testing.T) {
	home := t.TempDir()
	elsewhere := t.TempDir()
	put(t, filepath.Join(elsewhere, "20250101-000000-1.png"), 300*day)
	if err := os.Symlink(elsewhere, filepath.Join(home, Folder)); err != nil {
		t.Fatal(err)
	}
	for _, dry := range []bool{true, false} {
		if p, err := Prune(home, now, day, dry); err == nil {
			t.Fatalf("dry=%v symlinked inbox = %+v", dry, p)
		}
	}
	if _, err := Size(home); err == nil {
		t.Fatal("size took a symlinked inbox")
	}
	if err := Mark(home, "20261019"); err == nil {
		t.Fatal("mark took a symlinked inbox")
	}
	if des, _ := os.ReadDir(elsewhere); len(des) != 1 {
		t.Fatalf("symlink target changed: %v", des)
	}
}

func TestMarkReplacesASymlinkedMarker(t *testing.T) {
	home := t.TempDir()
	trash := filepath.Join(home, Folder, Trash)
	if err := os.MkdirAll(trash, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "keep.txt")
	if err := os.WriteFile(target, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(trash, mark)); err != nil {
		t.Fatal(err)
	}
	if err := Mark(home, "20261019"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(target); err != nil || string(b) != "keep\n" {
		t.Fatalf("symlink target = %q, %v", b, err)
	}
	if info, err := os.Lstat(filepath.Join(trash, mark)); err != nil || !info.Mode().IsRegular() || !Marked(home, "20261019") {
		t.Fatalf("marker = %v, %v", info, err)
	}
	if des, _ := os.ReadDir(trash); len(des) != 1 {
		t.Fatalf("trash holds %v", des)
	}
}

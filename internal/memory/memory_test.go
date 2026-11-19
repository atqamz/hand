package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestInitCreatesLayoutAndKeepsExistingOperatorMemory(t *testing.T) {
	home := t.TempDir()
	if err := Init(home); err != nil {
		t.Fatal(err)
	}
	op := filepath.Join(home, "memory", OperatorFile)
	if err := os.WriteFile(op, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Init(home); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(op)
	if err != nil || string(got) != "mine\n" {
		t.Fatalf("operator memory = %q, %v; want untouched", got, err)
	}
	if fi, err := os.Stat(filepath.Join(home, "memory", "projects")); err != nil || !fi.IsDir() {
		t.Fatalf("projects dir: %v", err)
	}
}

func writeOperator(t *testing.T, home, body string) {
	t.Helper()
	if err := Init(home); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "memory", OperatorFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadCutsAtALineBoundary(t *testing.T) {
	home := t.TempDir()
	body := "# T\n\n- aaaa\n- bbbbbbbb\n- cc\n"
	writeOperator(t, home, body)
	c, err := Read(home, OperatorFile, 16)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(c.Lines, "|"); got != "# T|- aaaa" || c.Total != 4 || c.Bytes != len(body) {
		t.Fatalf("Read = %q total %d bytes %d", got, c.Total, c.Bytes)
	}
	if len(c.Largest) != 3 || c.Largest[0] != (Line{4, 10}) || c.Largest[1] != (Line{3, 6}) || c.Largest[2] != (Line{5, 4}) {
		t.Fatalf("largest = %+v", c.Largest)
	}
	whole, err := Read(home, OperatorFile, len(body))
	if err != nil || len(whole.Lines) != 4 {
		t.Fatalf("exact budget shows %d lines, %v", len(whole.Lines), err)
	}
}

func TestReadBudgetBoundary(t *testing.T) {
	for _, tc := range []struct {
		size, shown int
	}{{4000, 2}, {4001, 1}} {
		home := t.TempDir()
		body := strings.Repeat("a", 3990) + "\n" + strings.Repeat("b", tc.size-3991)
		writeOperator(t, home, body)
		c, err := Read(home, OperatorFile, 4000)
		if err != nil {
			t.Fatal(err)
		}
		if len(c.Lines) != tc.shown || c.Total != 2 || c.Bytes != tc.size {
			t.Fatalf("%d bytes: shown %d of %d, bytes %d; want shown %d of 2", tc.size, len(c.Lines), c.Total, c.Bytes, tc.shown)
		}
	}
}

func TestReadCountsAnOversizedLineAsHidden(t *testing.T) {
	home := t.TempDir()
	writeOperator(t, home, "- "+strings.Repeat("x", 50)+"\n- short\n")
	c, err := Read(home, OperatorFile, 20)
	if err != nil || len(c.Lines) != 0 || c.Total != 2 {
		t.Fatalf("Read = %+v, %v; want nothing shown of 2", c, err)
	}
}

func TestReadKeepsMultibyteLinesWholeAndValidUTF8(t *testing.T) {
	home := t.TempDir()
	line := "- " + strings.Repeat("漢", 10)
	writeOperator(t, home, line+"\n"+line+"\n- h\xff\xfex\n")
	c, err := Read(home, OperatorFile, len(line)+len(line)/2)
	if err != nil || len(c.Lines) != 1 || c.Lines[0] != line || c.Total != 3 {
		t.Fatalf("Read = %+v, %v", c, err)
	}
	all, err := Read(home, OperatorFile, 1<<20)
	if err != nil || all.Lines[2] != "- hx" {
		t.Fatalf("Read = %+v, %v", all, err)
	}
	for _, l := range all.Lines {
		if !utf8.ValidString(l) {
			t.Fatalf("invalid UTF-8 line %q", l)
		}
	}
}

func TestReadTrimsCRLF(t *testing.T) {
	home := t.TempDir()
	writeOperator(t, home, "# T\r\n- a\r\n")
	c, err := Read(home, OperatorFile, 100)
	if err != nil || strings.Join(c.Lines, "|") != "# T|- a" || c.Largest[0].Bytes != 3 {
		t.Fatalf("Read = %+v, %v", c, err)
	}
}

func TestReadMissingFileIsEmpty(t *testing.T) {
	c, err := Read(t.TempDir(), OperatorFile, 100)
	if err != nil || c.Total != 0 || len(c.Lines) != 0 || c.Bytes != 0 {
		t.Fatalf("Read = %+v, %v", c, err)
	}
}

func TestTopicsListsRegularMarkdownFilesWithTheirHeading(t *testing.T) {
	home := filepath.Join(t.TempDir(), "fleet [a]{b}")
	if err := Init(home); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "memory", "operator")
	if err := os.MkdirAll(filepath.Join(dir, "folder.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"b.md":     "intro\r\n# B: read it when asked\r\n- x\n",
		"a.md":     "## A: first\n",
		"none.md":  "no heading\n",
		"skip.txt": "# not md\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(dir, "a.md"), filepath.Join(dir, "link.md")); err != nil {
		t.Log("symlinks unavailable:", err)
	}
	got, total := Topics(home, 12)
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	want := []Topic{{"a.md", int64(len(files["a.md"])), "A: first"}, {"b.md", int64(len(files["b.md"])), "B: read it when asked"}, {"none.md", int64(len(files["none.md"])), ""}}
	if len(got) != len(want) {
		t.Fatalf("Topics = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Topics[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestTopicsMissingFolderIsEmpty(t *testing.T) {
	got, total := Topics(t.TempDir(), 12)
	if len(got) != 0 || total != 0 {
		t.Fatalf("Topics = %+v, %d", got, total)
	}
}

func TestTopicsFolderThatIsAFileIsEmpty(t *testing.T) {
	home := t.TempDir()
	if err := Init(home); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "memory", "operator"), []byte("not a folder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, total := Topics(home, 12)
	if len(got) != 0 || total != 0 {
		t.Fatalf("Topics = %+v, %d", got, total)
	}
}

func TestTopicsUnreadableFolderIsEmpty(t *testing.T) {
	home := t.TempDir()
	if err := Init(home); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "memory", "operator")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("# A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0); err != nil {
		t.Skip("chmod unavailable:", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if _, err := os.ReadDir(dir); err == nil {
		t.Skip("folder stays readable with mode 000 here")
	}
	got, total := Topics(home, 12)
	if len(got) != 0 || total != 0 {
		t.Fatalf("Topics = %+v, %d", got, total)
	}
}

func TestTopicsReadsTheHeadingOfTheShownFilesOnly(t *testing.T) {
	home := t.TempDir()
	if err := Init(home); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "memory", "operator")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a", "b", "c"} {
		if err := os.WriteFile(filepath.Join(dir, n+".md"), []byte("# "+n+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, total := Topics(home, 2)
	if total != 3 || len(got) != 2 || got[0].Title != "a" || got[1].Title != "b" {
		t.Fatalf("Topics = %+v, %d; want a and b of 3", got, total)
	}
}

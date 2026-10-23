package harness

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

func TestLookPathExtPrefersAnExeOverEveryBatchFile(t *testing.T) {
	root := t.TempDir()
	dirs := map[string]string{}
	for _, d := range []string{"a", "b", "c"} {
		dirs[d] = filepath.Join(root, d)
		if err := os.Mkdir(dirs[d], 0o755); err != nil {
			t.Fatal(err)
		}
	}
	put := func(dir, file string) string {
		p := filepath.Join(dirs[dir], file)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	exe, cmd, bat := put("b", "codex.exe"), put("a", "codex.cmd"), put("a", "claude.bat")
	put("c", "claude.cmd")
	put("c", "opencode.cmd")
	other := put("a", "agy.exe")
	path := strings.Join([]string{dirs["a"], "relative", dirs["b"], dirs["c"]}, string(os.PathListSeparator))
	const pathext = ".COM;.EXE;.BAT;.CMD"
	for _, tc := range []struct {
		name, ext string
		want      string
		batch     string
	}{
		{name: "codex", ext: pathext, want: exe},
		{name: "codex.exe", ext: pathext, want: exe},
		{name: "codex", ext: "EXE;.CMD", want: exe},
		{name: "agy", ext: pathext, want: other},
		{name: "codex.cmd", ext: pathext, batch: cmd},
		{name: "claude", ext: pathext, batch: bat},
		{name: "opencode", ext: pathext, batch: filepath.Join(dirs["c"], "opencode.cmd")},
		{name: "codex", ext: ".COM;.BAT;.CMD", batch: cmd},
		{name: "gemini", ext: pathext},
	} {
		got, err := lookPathExt(tc.name, path, tc.ext)
		var be *BatchFileError
		switch {
		case tc.want != "":
			if err != nil || got != tc.want {
				t.Errorf("%s %q = %q, %v; want %q", tc.name, tc.ext, got, err, tc.want)
			}
		case tc.batch != "":
			stem := strings.TrimSuffix(tc.name, filepath.Ext(tc.name))
			if !errors.As(err, &be) || !errors.Is(err, state.ErrInvalid) || got != "" || be.Path != tc.batch || be.Name != stem {
				t.Errorf("%s %q = %q, %v; want a batch file error for %s", tc.name, tc.ext, got, err, tc.batch)
			}
			for _, part := range []string{stem + " is installed only as " + tc.batch, "install the standalone " + stem, "add the folder of " + stem + ".exe to PATH"} {
				if !strings.Contains(err.Error(), part) {
					t.Errorf("%s error %q lacks %q", tc.name, err, part)
				}
			}
		default:
			if err == nil || errors.As(err, &be) || !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), tc.name+" not found on PATH") {
				t.Errorf("%s = %q, %v; want not found", tc.name, got, err)
			}
		}
	}
}

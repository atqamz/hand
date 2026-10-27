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
			_, npm := npmNative[stem]
			if strings.Contains(err.Error(), "an npm install leaves only the shim on PATH") != npm {
				t.Errorf("%s error %q: npm note present is not %v", tc.name, err, npm)
			}
		default:
			if err == nil || errors.As(err, &be) || !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), tc.name+" not found on PATH") {
				t.Errorf("%s = %q, %v; want not found", tc.name, got, err)
			}
		}
	}
}

func TestLookPathExtResolvesTheExeNpmInstallsBesideAShim(t *testing.T) {
	const pathext = ".COM;.EXE;.BAT;.CMD"
	for _, tc := range []struct {
		name, shim, exe string
		want, stub      bool
	}{
		{name: "codex", shim: "codex.cmd", exe: "node_modules/@openai/codex/node_modules/@openai/codex-win32-x64/vendor/x86_64-pc-windows-msvc/bin/codex.exe", want: true},
		{name: "codex.cmd", shim: "codex.cmd", exe: "node_modules/@openai/codex/node_modules/@openai/codex-win32-x64/vendor/x86_64-pc-windows-msvc/bin/codex.exe", want: true},
		{name: "codex", shim: "codex.cmd", exe: "node_modules/@openai/codex/node_modules/@openai/codex-win32-x64/vendor/x86_64-pc-windows-msvc/bin/codex-code-mode-host.exe"},
		{name: "codex", shim: "codex.cmd", exe: "node_modules/@openai/codex/node_modules/@openai/codex-linux-x64/vendor/x86_64-unknown-linux-musl/bin/codex.exe"},
		{name: "codex", shim: "codex.cmd"},
		{name: "claude", shim: "claude.cmd", exe: "node_modules/@anthropic-ai/claude-code/bin/claude.exe", want: true},
		{name: "claude", shim: "claude.cmd", exe: "node_modules/@anthropic-ai/claude-code-win32-x64/claude.exe"},
		{name: "claude", shim: "claude.cmd", exe: "node_modules/@anthropic-ai/claude-code/bin/claude.exe", stub: true},
		{name: "opencode", shim: "opencode.cmd", exe: "node_modules/opencode-ai/bin/opencode.exe", want: true},
		{name: "agy", shim: "agy.cmd", exe: "node_modules/@openai/codex/node_modules/@openai/codex-win32-x64/vendor/x86_64-pc-windows-msvc/bin/codex.exe"},
		{name: "claude", shim: "claude.cmd", exe: "node_modules/opencode-ai/bin/opencode.exe"},
	} {
		dir := t.TempDir()
		shim := filepath.Join(dir, tc.shim)
		if err := os.WriteFile(shim, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		var exe string
		if tc.exe != "" {
			exe = filepath.Join(dir, filepath.FromSlash(tc.exe))
			if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(exe, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			if !tc.stub {
				if err := os.Truncate(exe, minNativeSize); err != nil {
					t.Fatal(err)
				}
			}
		}
		got, err := lookPathExt(tc.name, dir, pathext)
		var be *BatchFileError
		switch {
		case tc.want:
			if err != nil || got != exe {
				t.Errorf("%s with %s = %q, %v; want %q", tc.name, tc.exe, got, err, exe)
			}
		case !errors.As(err, &be) || !errors.Is(err, state.ErrInvalid) || got != "" || be.Path != shim:
			t.Errorf("%s with %q = %q, %v; want a batch file error for %s", tc.name, tc.exe, got, err, shim)
		}
	}
}

func TestLookPathExtRefusesAnNpmFolderThatIsNotARegularFile(t *testing.T) {
	dir := t.TempDir()
	shim := filepath.Join(dir, "codex.cmd")
	if err := os.WriteFile(shim, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "node_modules", "@openai", "codex", "node_modules", "@openai", "codex-win32-x64", "vendor", "x86_64-pc-windows-msvc", "bin", "codex.exe")
	if err := os.MkdirAll(exe, 0o755); err != nil {
		t.Fatal(err)
	}
	var be *BatchFileError
	if got, err := lookPathExt("codex", dir, ".EXE;.CMD"); !errors.As(err, &be) || got != "" {
		t.Fatalf("lookPathExt = %q, %v; want a batch file error", got, err)
	}
}

func TestNpmExePicksTheCodexPackageThatRunsOnTheMachine(t *testing.T) {
	const (
		x64   = "node_modules/@openai/codex/node_modules/@openai/codex-win32-x64/vendor/x86_64-pc-windows-msvc/bin/codex.exe"
		arm64 = "node_modules/@openai/codex/node_modules/@openai/codex-win32-arm64/vendor/aarch64-pc-windows-msvc/bin/codex.exe"
	)
	for _, tc := range []struct {
		arch      string
		have      []string
		want      string
		wantFound bool
	}{
		{arch: "amd64", have: []string{x64, arm64}, want: x64, wantFound: true},
		{arch: "arm64", have: []string{x64, arm64}, want: arm64, wantFound: true},
		{arch: "arm64", have: []string{x64}, want: x64, wantFound: true},
		{arch: "arm64", have: []string{arm64}, want: arm64, wantFound: true},
		{arch: "amd64", have: []string{x64}, want: x64, wantFound: true},
		{arch: "amd64", have: []string{arm64}},
	} {
		dir := t.TempDir()
		for _, f := range tc.have {
			p := filepath.Join(dir, filepath.FromSlash(f))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Truncate(p, minNativeSize); err != nil {
				t.Fatal(err)
			}
		}
		want := ""
		if tc.want != "" {
			want = filepath.Join(dir, filepath.FromSlash(tc.want))
		}
		got, ok := npmExe("codex", filepath.Join(dir, "codex.cmd"), tc.arch)
		if got != want || ok != tc.wantFound {
			t.Errorf("%s with %v = %q, %v; want %q, %v", tc.arch, tc.have, got, ok, want, tc.wantFound)
		}
	}
}

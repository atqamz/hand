package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/fakebin"
)

func echoArgsMain(args []string) int {
	for _, a := range args {
		fakebin.Append(fakebin.Params()["out"], a)
	}
	return 0
}

func TestCodexDirSurvivesLaunchingTheExe(t *testing.T) {
	base := t.TempDir()
	out := filepath.Join(base, "args.txt")
	bin := fakebin.Install(t, base, "codex", "echoargs", map[string]string{"out": out})
	dir := filepath.Join(base, "my fleet")
	spec := Spec{"codex", "gpt-6-luna", "low"}
	for name, build := range map[string]func() ([]string, error){
		"supervisor": func() ([]string, error) { return SupervisorArgv(bin, spec, "", "go", dir, false) },
		"resume":     func() ([]string, error) { return SupervisorArgv(bin, spec, "abc", "", dir, true) },
		"worker":     func() ([]string, error) { return Argv(bin, spec, "go", dir) },
	} {
		_ = os.Remove(out)
		argv, err := build()
		if err != nil {
			t.Fatal(err)
		}
		if b, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", name, err, b)
		}
		got, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimRight(strings.ReplaceAll(string(got), "\r\n", "\n"), "\n"), "\n")
		if i := slices.Index(lines, "--cd"); i < 0 || i+1 == len(lines) || lines[i+1] != dir {
			t.Fatalf("%s: the exe saw %q, want --cd followed by %q", name, lines, dir)
		}
	}
}

func TestOpencodeSessionIdSurvivesLaunchingTheExe(t *testing.T) {
	base := t.TempDir()
	out := filepath.Join(base, "args.txt")
	bin := fakebin.Install(t, base, "opencode", "echoargs", map[string]string{"out": out})
	spec := Spec{Harness: "opencode"}
	id := NewOpencodeSessionID()
	for name, build := range map[string]func() ([]string, error){
		"supervisor": func() ([]string, error) { return SupervisorArgv(bin, spec, id, "go", base, false) },
		"resume":     func() ([]string, error) { return SupervisorArgv(bin, spec, id, "", base, true) },
		"worker":     func() ([]string, error) { return Argv(bin, spec, "go", base) },
	} {
		_ = os.Remove(out)
		argv, err := build()
		if err != nil {
			t.Fatal(err)
		}
		if b, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", name, err, b)
		}
		got, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimRight(strings.ReplaceAll(string(got), "\r\n", "\n"), "\n"), "\n")
		i := slices.Index(lines, "--session")
		if i < 0 || i+1 == len(lines) || !strings.HasPrefix(lines[i+1], "ses_hand") || !sessionPattern.MatchString(lines[i+1]) {
			t.Fatalf("%s: the exe saw %q, want --session followed by a ses_hand id", name, lines)
		}
		if name != "worker" && lines[i+1] != id {
			t.Fatalf("%s: the exe saw id %q, want %q", name, lines[i+1], id)
		}
	}
}

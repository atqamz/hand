package update

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func fakeSystemctl(t *testing.T, list, show string, fail map[string]bool) (string, string) {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	for name, body := range map[string]string{"list": list, "show": show} {
		if err := os.WriteFile(filepath.Join(dir, name+".txt"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var fails []string
	for verb := range fail {
		fails = append(fails, verb)
	}
	script := "#!/bin/sh\necho \"systemctl $*\" >> " + calls + "\n" +
		"for f in " + strings.Join(fails, " ") + "; do if [ \"$2\" = \"$f\" ]; then echo \"Failed to $2 $3: boom\" >&2; exit 1; fi; done\n" +
		"case \"$2\" in list-units) cat " + filepath.Join(dir, "list.txt") + " ;; show) cat " + filepath.Join(dir, "show.txt") + " ;; esac\n"
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, calls
}

func execStart(path, argv string) string {
	return "ExecStart={ path=" + path + " ; argv[]=" + argv + " ; ignore_errors=no ; start_time=[n/a] ; stop_time=[n/a] ; pid=0 ; code=(null) ; status=0/0 }\n"
}

func unitBlock(name, state, path, argv, env string) string {
	return "Id=" + name + "\nActiveState=" + state + "\n" + execStart(path, argv) + "Environment=" + env + "\n"
}

func listing(names ...string) string {
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n + " loaded active running Hand\n")
	}
	return b.String()
}

func pathEnv(dir string) []string {
	return []string{"PATH=" + dir + ":" + os.Getenv("PATH")}
}

func TestUnitsFindThisBinarysUnits(t *testing.T) {
	show := strings.Join([]string{
		unitBlock("secondhand-board.service", "active", "/t/hand", "/t/hand board --addr 127.0.0.1:7777", ""),
		unitBlock("secondhand-luvus-f1.service", "active", "/r/luvus/0.14.3-89507302/luvus", "/r/luvus/0.14.3-89507302/luvus server", ""),
		unitBlock("secondhand-watch-alpha.service", "active", "/t/hand", "/t/hand watch", "HAND_HOME=/f/alpha"),
		unitBlock("secondhand-watch-other.service", "active", "/other/hand", "/other/hand watch", "HAND_HOME=/f/other"),
	}, "\n")
	dir, calls := fakeSystemctl(t, listing("secondhand-board.service", "secondhand-luvus-f1.service", "secondhand-watch-alpha.service", "secondhand-watch-other.service"), show, nil)
	got, err := Units(context.Background(), pathEnv(dir), "/t/hand")
	if err != nil {
		t.Fatal(err)
	}
	want := []Unit{{"secondhand-board.service", "board", "", true}, {"secondhand-watch-alpha.service", "watch", "/f/alpha", true}}
	if !slices.Equal(got, want) {
		t.Fatalf("units = %+v", got)
	}
	log, _ := os.ReadFile(calls)
	if !strings.Contains(string(log), "systemctl --user list-units --all --plain --no-legend secondhand-*\n") || !strings.Contains(string(log), "-p Id,ActiveState,ExecStart,Environment") {
		t.Fatalf("calls:\n%s", log)
	}
}

func TestUnitsReadAQuotedHome(t *testing.T) {
	dir, _ := fakeSystemctl(t, listing("secondhand-watch-mine.service"), unitBlock("secondhand-watch-mine.service", "inactive", "/t/hand", "/t/hand watch", `"HAND_HOME=/f/my fleet" SECONDHAND_HOME=/r`), nil)
	got, err := Units(context.Background(), pathEnv(dir), "/t/hand")
	if err != nil || !slices.Equal(got, []Unit{{"secondhand-watch-mine.service", "watch", "/f/my fleet", false}}) {
		t.Fatalf("units = %+v, %v", got, err)
	}
}

func TestUnitsWithoutSystemctl(t *testing.T) {
	got, err := Units(context.Background(), []string{"PATH=" + t.TempDir()}, "/t/hand")
	if err != nil || got != nil {
		t.Fatalf("units = %+v, %v", got, err)
	}
}

func TestSystemctlReportsItsOutput(t *testing.T) {
	dir, calls := fakeSystemctl(t, "", "", map[string]bool{"stop": true})
	err := Systemctl(context.Background(), pathEnv(dir), "stop", "secondhand-watch-alpha.service")
	if err == nil || !strings.Contains(err.Error(), "Failed to stop secondhand-watch-alpha.service: boom") {
		t.Fatalf("err = %v", err)
	}
	if log, _ := os.ReadFile(calls); string(log) != "systemctl --user stop secondhand-watch-alpha.service\n" {
		t.Fatalf("calls = %q", log)
	}
}

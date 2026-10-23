package cli_test

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/fakebin"
)

func markerCommand(t *testing.T) (command, log string) {
	t.Helper()
	log = filepath.Join(t.TempDir(), "ran.log")
	return filepath.ToSlash(fakebin.Install(t, t.TempDir(), "mark", "fake", map[string]string{"log": log})), log
}

func nothingRan(t *testing.T, log string) {
	t.Helper()
	if b, err := os.ReadFile(log); err == nil {
		t.Fatalf("a configured program ran: %q", b)
	} else if !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
}

func plantConfig(t *testing.T, repo string, kvs ...[2]string) (restore func()) {
	t.Helper()
	file := filepath.Join(repo, ".git", "config")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, kv := range kvs {
		parts := strings.Split(kv[0], ".")
		if sub := strings.Join(parts[1:len(parts)-1], "."); sub != "" {
			fmt.Fprintf(&b, "[%s %q]\n", parts[0], sub)
		} else {
			fmt.Fprintf(&b, "[%s]\n", parts[0])
		}
		fmt.Fprintf(&b, "\t%s = %q\n", parts[len(parts)-1], kv[1])
	}
	if err := os.WriteFile(file, append(slices.Clone(before), b.String()...), 0o644); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := os.WriteFile(file, before, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func plant(pattern, command string) (key, value string) {
	key = strings.Replace(pattern, "*", "x", 1)
	if strings.HasPrefix(key, "include") {
		return key, "missing.cfg"
	}
	return key, command
}

func TestDoctorNamesAProjectWhoseSettingsChanged(t *testing.T) {
	fx := newDoctorFixture(t)
	command, _ := markerCommand(t)
	repo := gitRepo(t)
	fx.h.ok("project", "add", "app", repo)
	if out, _, _ := fx.h.run("doctor"); !strings.Contains(out, "project app,ok,") {
		t.Fatalf("doctor = %q", out)
	}
	gitIn(t, repo, "config", "core.sshCommand", command)
	out, errOut, code := fx.h.run("doctor")
	if code != 1 || !strings.Contains(out, "project app,fail,") || !strings.Contains(out, `core.sshcommand`) || strings.Contains(out, command) || !strings.Contains(errOut, "check(s) failed") {
		t.Fatalf("doctor: code=%d out=%q stderr=%q", code, out, errOut)
	}
	fx.h.ok("project", "trust", "app")
	if out, _, _ := fx.h.run("doctor"); !strings.Contains(out, "project app,ok,") {
		t.Fatalf("doctor after trust = %q", out)
	}
}

func TestTrustShowsValuesWithControlCharactersEscaped(t *testing.T) {
	fx := newAttemptFixture(t)
	gitIn(t, fx.repo, "config", "core.editor", "vi\x1b[2Jx\ty")
	out := fx.h.ok("project", "trust", "app")
	if strings.ContainsRune(out, 0x1b) || !strings.Contains(out, `vi\x1b[2Jx\ty`) {
		t.Fatalf("trust = %q", out)
	}
}

func TestTrustRefusesWhatItCannotReadOrFind(t *testing.T) {
	fx := newAttemptFixture(t)
	if _, errOut, code := fx.h.run("project", "trust", "nope"); code != 3 || !strings.Contains(errOut, "project nope") {
		t.Fatalf("unknown: code=%d stderr=%q", code, errOut)
	}
	if _, _, code := fx.h.run("project", "trust"); code != 2 {
		t.Fatalf("no name: code=%d", code)
	}
	gone := t.TempDir()
	fx.h.ok("project", "add", "plain", gone)
	if _, _, code := fx.h.run("project", "trust", "plain"); code != 1 {
		t.Fatalf("not a repository: code=%d", code)
	}
}

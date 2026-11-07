//go:build linux

package update

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/fleet"
)

func fakeSystemd(t *testing.T, f *fixture, unit, name, home string, transient ...string) (string, string) {
	t.Helper()
	base, bin, runtime := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(runtime, "systemd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtime, "systemd", "private"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "systemd-run"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, unit)
	if err := os.WriteFile(path, []byte("[Unit]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	files := filepath.Join(base, "files")
	units := filepath.Join(base, "units")
	show := filepath.Join(base, "show")
	log := filepath.Join(base, "log")
	if err := os.WriteFile(files, []byte(unit+" enabled\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(units, []byte(strings.Join(transient, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf("Id=%s\nLoadState=loaded\nTransient=no\nFragmentPath=%s\nExecStart={ path=%s ; argv[]=%s watch ; ignore_errors=no }\nEnvironment=HAND_HOME=%s\nDescription=Hand watcher for %s (%s)\n\n",
		unit, path, f.target, f.target, home, name, home)
	if err := os.WriteFile(show, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" >> %s\ncase \"$*\" in\n*list-unit-files*) cat %s;;\n*list-units*) cat %s;;\n*show*) cat %s;;\nesac\nexit 0\n", log, files, units, show)
	if err := os.WriteFile(filepath.Join(bin, "systemctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	env := append(slices.Clone(f.o.Env), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "XDG_RUNTIME_DIR="+runtime)
	f.o.Env, f.o.Getenv = env, lookup(env)
	return log, path
}

func calls(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return string(b)
}

func TestUpdateRemovesAWatchUnitForAMovedHome(t *testing.T) {
	f := newCycle(t)
	log, path := fakeSystemd(t, f, "secondhand-watch-gone.service", "gone", f.gone)
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Failed {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if got := calls(t, log); !strings.Contains(got, "--user disable --now secondhand-watch-gone.service") {
		t.Fatalf("systemctl calls = %q, want the moved home's unit disabled", got)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the moved home's unit file still exists: %v", err)
	}
	if !slices.Contains(rep.Units, UnitResult{"secondhand-watch-gone.service", "remove", "ok"}) {
		t.Fatalf("units = %+v", rep.Units)
	}
}

func TestUpdateRemovesAWatchUnitForAnUnregisteredHome(t *testing.T) {
	f := newCycle(t)
	log, path := fakeSystemd(t, f, "secondhand-watch-archive.service", "archive", t.TempDir())
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Failed {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if got := calls(t, log); !strings.Contains(got, "--user disable --now secondhand-watch-archive.service") {
		t.Fatalf("systemctl calls = %q, want the unregistered home's unit disabled", got)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the unregistered home's unit file still exists: %v", err)
	}
	if !slices.Contains(rep.Units, UnitResult{"secondhand-watch-archive.service", "remove", "ok"}) {
		t.Fatalf("units = %+v", rep.Units)
	}
}

func TestUpdateKeepsALiveFleetsWatchUnit(t *testing.T) {
	f := newCycle(t)
	log, path := fakeSystemd(t, f, "secondhand-watch-alpha.service", "alpha", f.alpha)
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Failed {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if got := calls(t, log); strings.Contains(got, "disable") {
		t.Fatalf("systemctl calls = %q, want the live fleet's unit kept", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the live fleet's unit file is gone: %v", err)
	}
}

func TestUpdateRemovesAWatchUnitTheTransientUnitReplaced(t *testing.T) {
	f := newCycle(t)
	log, path := fakeSystemd(t, f, "secondhand-watch-alpha.service", "alpha", f.alpha, fleet.WatchUnit(f.alphaID)+".service loaded active running")
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Failed {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if got := calls(t, log); !strings.Contains(got, "--user disable --now secondhand-watch-alpha.service") {
		t.Fatalf("systemctl calls = %q, want the replaced unit disabled", got)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the replaced unit file still exists: %v", err)
	}
	if got := calls(t, log); strings.Contains(got, "disable --now "+fleet.WatchUnit(f.alphaID)) {
		t.Fatalf("systemctl calls = %q, want the transient unit kept", got)
	}
}

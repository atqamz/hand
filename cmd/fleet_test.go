package cmd

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/registry"
	"github.com/atqamz/hand/internal/store"
)

func TestFleetListsKnownHomesOutsideFleetContext(t *testing.T) {
	userHome := t.TempDir()
	setTestUserHome(t, userHome)
	t.Setenv("HAND_HOME", "")
	t.Chdir(t.TempDir())
	firstHome := filepath.Join(t.TempDir(), "first")
	secondHome := filepath.Join(t.TempDir(), "second")
	registryDB, err := registry.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = registryDB.Close() }()
	for _, home := range []string{firstHome, secondHome} {
		db, err := store.Open(home)
		if err != nil {
			t.Fatal(err)
		}
		fleetID, err := db.FleetID()
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		if err := registryDB.Register(home, fleetID, testNow()); err != nil {
			t.Fatal(err)
		}
	}

	root := newRootCmd(devBuild("test"))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"fleet"})
	if _, err := root.ExecuteC(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"fleets[2]{id,home,state,current,locations}:", "ready,false", "current_home: none"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("fleet output = %q, want %q", out.String(), want)
		}
	}
}

func TestFleetListsHomesWhenExplicitContextIsInvalid(t *testing.T) {
	userHome := t.TempDir()
	setTestUserHome(t, userHome)
	badHome := filepath.Join(t.TempDir(), "not-a-fleet")
	t.Setenv("HAND_HOME", badHome)
	t.Chdir(t.TempDir())
	registryDB, err := registry.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = registryDB.Close() }()
	home := filepath.Join(t.TempDir(), "fleet")
	db, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	fleetID, err := db.FleetID()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := registryDB.Register(home, fleetID, testNow()); err != nil {
		t.Fatal(err)
	}

	root := newRootCmd(devBuild("test"))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"fleet"})
	if _, err := root.ExecuteC(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "current_error:") || !strings.Contains(out.String(), "HAND_HOME") || !strings.Contains(out.String(), "fleets[1]") {
		t.Fatalf("fleet output = %q, want context error and listing", out.String())
	}
}

func testNow() (now time.Time) {
	return time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)
}

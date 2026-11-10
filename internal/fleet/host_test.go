package fleet_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fleet"
)

var hostNow = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func marker(host, boot string, age time.Duration) fleet.HostMarker {
	return fleet.HostMarker{Host: host, Boot: boot, PID: 4242, At: hostNow.Add(-age)}
}

func held(v bool) func() (bool, error) { return func() (bool, error) { return v, nil } }

func TestJudgeHost(t *testing.T) {
	self := marker("desk", "boot-b", 0)
	for _, c := range []struct {
		name  string
		m     fleet.HostMarker
		found bool
		held  func() (bool, error)
		want  fleet.HostVerdict
	}{
		{"no marker", fleet.HostMarker{}, false, held(false), fleet.HostOurs},
		{"this host and boot, fresh", marker("desk", "boot-b", time.Second), true, held(false), fleet.HostOurs},
		{"this host and boot, a day old", marker("desk", "boot-b", 24*time.Hour), true, held(false), fleet.HostOurs},
		{"this host and boot, written in the future", marker("desk", "boot-b", -3*time.Hour), true, held(false), fleet.HostOurs},
		{"this boot under another host name", marker("renamed", "boot-b", 24*time.Hour), true, held(false), fleet.HostOurs},
		{"this host name in other case", marker("DESK", "boot-b", time.Hour), true, held(false), fleet.HostOurs},
		{"this host, earlier boot, nothing holds the lock", marker("desk", "boot-a", time.Second), true, held(false), fleet.HostTakeOver},
		{"this host, earlier boot, a watcher holds the lock", marker("desk", "boot-a", time.Second), true, held(true), fleet.HostOurs},
		{"this host, earlier boot, the lock cannot be read", marker("desk", "boot-a", time.Hour), true, func() (bool, error) { return false, errors.New("denied") }, fleet.HostOurs},
		{"other host, fresh", marker("laptop", "boot-x", 5*time.Second), true, held(false), fleet.HostRefuse},
		{"other host, at the limit", marker("laptop", "boot-x", fleet.HostStale), true, held(false), fleet.HostRefuse},
		{"other host, past the limit", marker("laptop", "boot-x", fleet.HostStale+time.Second), true, held(false), fleet.HostTakeOver},
		{"other host, clock ahead of ours", marker("laptop", "boot-x", -10*time.Minute), true, held(false), fleet.HostRefuse},
		{"other host, lock held here", marker("laptop", "boot-x", time.Hour), true, held(true), fleet.HostTakeOver},
	} {
		if got := fleet.JudgeHost(c.m, c.found, self, hostNow, c.held); got != c.want {
			t.Errorf("%s: verdict %v, want %v", c.name, got, c.want)
		}
	}
}

func TestJudgeHostReadsTheWindowsBootForm(t *testing.T) {
	self := marker("pc", "ticks:1799999000", 0)
	for _, c := range []struct {
		name string
		boot string
		held bool
		want fleet.HostVerdict
	}{
		{"same second", "ticks:1799999000", false, fleet.HostOurs},
		{"jitter", "ticks:1799999003", false, fleet.HostOurs},
		{"clock jumped an hour, watcher alive", "ticks:1799995400", true, fleet.HostOurs},
		{"rebooted, nothing holds the lock", "ticks:1799995400", false, fleet.HostTakeOver},
	} {
		got := fleet.JudgeHost(marker("pc", c.boot, time.Second), true, self, hostNow, held(c.held))
		if got != c.want {
			t.Errorf("%s: verdict %v, want %v", c.name, got, c.want)
		}
	}
}

func TestHostMarkerRoundTripsAndLeavesNoTemp(t *testing.T) {
	home := t.TempDir()
	if _, found, err := fleet.ReadHost(home); err != nil || found {
		t.Fatalf("empty home: found=%v err=%v", found, err)
	}
	want := marker("desk", "boot-b", 0)
	if err := fleet.WriteHost(home, want); err != nil {
		t.Fatal(err)
	}
	got, found, err := fleet.ReadHost(home)
	if err != nil || !found || got != want {
		t.Fatalf("read %+v found=%v err=%v, want %+v", got, found, err, want)
	}
	if err := fleet.WriteHost(home, marker("desk", "boot-b", -time.Minute)); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(home)
	for _, e := range entries {
		if e.Name() != fleet.HostFile {
			t.Fatalf("a write left %s behind", e.Name())
		}
	}
	if err := fleet.RemoveHost(home); err != nil {
		t.Fatal(err)
	}
	if err := fleet.RemoveHost(home); err != nil {
		t.Fatalf("removing twice: %v", err)
	}
	if _, found, _ := fleet.ReadHost(home); found {
		t.Fatal("marker still there")
	}
}

func TestHostMarkerThatCannotBeReadCountsAsAbsent(t *testing.T) {
	home := t.TempDir()
	for _, text := range []string{"", "not json", `{"host":""}`, strings.Repeat("{", 3)} {
		if err := os.WriteFile(filepath.Join(home, fleet.HostFile), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, found, err := fleet.ReadHost(home); err != nil || found {
			t.Fatalf("%q: found=%v err=%v", text, found, err)
		}
	}
}

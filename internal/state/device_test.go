package state

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var hashA, hashB = strings.Repeat("a", 64), strings.Repeat("b", 64)

func deviceEvents(t *testing.T, s *Store) []string {
	t.Helper()
	events, err := s.EventsAfter(context.Background(), 0, []string{"board.paired", "board.revoked"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Kind + " " + e.Detail
	}
	return out
}

func TestAPairedDeviceIsStoredWithItsEventAndNoSecret(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	d, err := s.AddDevice(ctx, "  Pixel \n 9\t", "operator@example.com", hashA)
	if err != nil || d.ID != 1 || d.Name != "Pixel 9" || !d.PairedAt.Equal(fixed) {
		t.Fatalf("AddDevice = %+v, %v", d, err)
	}
	list, err := s.Devices(ctx)
	if err != nil || len(list) != 1 || list[0] != d {
		t.Fatalf("Devices = %+v, %v", list, err)
	}
	if got := deviceEvents(t, s); len(got) != 1 || got[0] != "board.paired 1 Pixel 9" {
		t.Fatalf("events = %q", got)
	}
	events, err := s.RecentEvents(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if strings.Contains(e.Detail, hashA) || strings.Contains(e.Detail, "operator@example.com") {
			t.Fatalf("event %s %q carries the hash or the login", e.Kind, e.Detail)
		}
	}
}

func TestADeviceNameIsOneShortLineAndHasADefault(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	long, err := s.AddDevice(ctx, strings.Repeat("é", 60)+"\x00", "", hashA)
	if err != nil || len([]rune(long.Name)) != MaxDeviceName {
		t.Fatalf("long name = %q, %v", long.Name, err)
	}
	blank, err := s.AddDevice(ctx, " \n\t\x07 ", "", hashB)
	if err != nil || blank.Name != "Phone" {
		t.Fatalf("blank name = %q, %v", blank.Name, err)
	}
	broken, err := s.AddDevice(ctx, "ok\xff\xfe", "a\x1b[31m@example.com", hashB)
	if err != nil || broken.Name != "ok" || broken.Login != "a [31m@example.com" {
		t.Fatalf("invalid UTF-8 and control characters = %+v, %v", broken, err)
	}
	if _, err := s.AddDevice(ctx, "x", "", "short"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("short hash = %v", err)
	}
}

func TestADeviceOpensOnlyWithItsOwnHash(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	d, _ := s.AddDevice(ctx, "Pixel", "", hashA)
	other, _ := s.AddDevice(ctx, "iPhone", "", hashB)
	if got, ok, err := s.DeviceWithSecret(ctx, d.ID, hashA); err != nil || !ok || got != d {
		t.Fatalf("right hash = %+v, %v, %v", got, ok, err)
	}
	for name, c := range map[string]struct {
		id   int64
		hash string
	}{
		"another device's hash": {d.ID, hashB},
		"no hash":               {d.ID, ""},
		"a longer hash":         {d.ID, hashA + "a"},
		"a missing device":      {99, hashA},
		"the swapped pair":      {other.ID, hashA},
	} {
		if got, ok, err := s.DeviceWithSecret(ctx, c.id, c.hash); err != nil || ok || got != (Device{}) {
			t.Fatalf("%s = %+v, %v, %v", name, got, ok, err)
		}
	}
}

func TestRevokingADeviceRemovesItAndRecordsIt(t *testing.T) {
	s, move := movingStore(t)
	ctx := context.Background()
	d, _ := s.AddDevice(ctx, "Pixel", "operator@example.com", hashA)
	keep, _ := s.AddDevice(ctx, "iPhone", "", hashB)
	move(time.Minute)
	gone, err := s.RevokeDevice(ctx, d.ID)
	if err != nil || gone.ID != d.ID || gone.Name != "Pixel" {
		t.Fatalf("RevokeDevice = %+v, %v", gone, err)
	}
	if _, ok, _ := s.DeviceWithSecret(ctx, d.ID, hashA); ok {
		t.Fatal("a revoked device still opens")
	}
	if _, ok, _ := s.DeviceWithSecret(ctx, keep.ID, hashB); !ok {
		t.Fatal("revoking one device closed another")
	}
	if _, err := s.RevokeDevice(ctx, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second revoke = %v", err)
	}
	want := []string{"board.paired 1 Pixel", "board.paired 2 iPhone", "board.revoked 1 Pixel"}
	if got := deviceEvents(t, s); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("events = %q, want %q", got, want)
	}
}

func TestADeviceAndItsEventShareOneTransaction(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	if _, err := s.db.Exec(`CREATE TRIGGER no_paired BEFORE INSERT ON event WHEN NEW.kind = 'board.paired' BEGIN SELECT RAISE(ABORT, 'no event'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddDevice(ctx, "Pixel", "", hashA); err == nil {
		t.Fatal("AddDevice survived a failing event")
	}
	if list, err := s.Devices(ctx); err != nil || len(list) != 0 {
		t.Fatalf("a device outlived its failed event: %v, %v", list, err)
	}
}

func TestADeviceSurvivesABackup(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	d, _ := s.AddDevice(ctx, "Pixel", "operator@example.com", hashA)
	dst := t.TempDir() + "/hand.db.bak"
	if err := s.Backup(ctx, dst); err != nil {
		t.Fatal(err)
	}
	b, err := Open(dst, clock)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if got, ok, err := b.DeviceWithSecret(ctx, d.ID, hashA); err != nil || !ok || got != d {
		t.Fatalf("restored device = %+v, %v, %v", got, ok, err)
	}
}

func TestARevokedDevicesNumberIsNeverGivenToAnotherPhone(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	first, _ := s.AddDevice(ctx, "Pixel", "", hashA)
	last, _ := s.AddDevice(ctx, "iPhone", "", hashB)
	if _, err := s.RevokeDevice(ctx, last.ID); err != nil {
		t.Fatal(err)
	}
	next, err := s.AddDevice(ctx, "Tablet", "", hashA)
	if err != nil || next.ID == last.ID || next.ID == first.ID {
		t.Fatalf("a new phone got number %d after %d and %d, %v", next.ID, first.ID, last.ID, err)
	}
	want := []string{"board.paired 1 Pixel", "board.paired 2 iPhone", "board.revoked 2 iPhone", "board.paired 3 Tablet"}
	if got := deviceEvents(t, s); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("events = %q, want %q", got, want)
	}
}

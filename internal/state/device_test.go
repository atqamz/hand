package state

import (
	"context"
	"errors"
	"slices"
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
	d, err := s.AddDevice(ctx, "  Pixel \n 9\t", "operator@example.com", hashA, "")
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
	long, err := s.AddDevice(ctx, strings.Repeat("é", 60)+"\x00", "", hashA, "")
	if err != nil || len([]rune(long.Name)) != MaxDeviceName {
		t.Fatalf("long name = %q, %v", long.Name, err)
	}
	blank, err := s.AddDevice(ctx, " \n\t\x07 ", "", hashB, "")
	if err != nil || blank.Name != "Phone" {
		t.Fatalf("blank name = %q, %v", blank.Name, err)
	}
	broken, err := s.AddDevice(ctx, "ok\xff\xfe", "a\x1b[31m@example.com", hashB, "")
	if err != nil || broken.Name != "ok" || broken.Login != "a [31m@example.com" {
		t.Fatalf("invalid UTF-8 and control characters = %+v, %v", broken, err)
	}
	if _, err := s.AddDevice(ctx, "x", "", "short", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("short hash = %v", err)
	}
}

func TestADeviceOpensOnlyWithItsOwnHash(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	d, _ := s.AddDevice(ctx, "Pixel", "", hashA, "")
	other, _ := s.AddDevice(ctx, "iPhone", "", hashB, "")
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
	d, _ := s.AddDevice(ctx, "Pixel", "operator@example.com", hashA, "")
	keep, _ := s.AddDevice(ctx, "iPhone", "", hashB, "")
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
	if _, err := s.AddDevice(ctx, "Pixel", "", hashA, ""); err == nil {
		t.Fatal("AddDevice survived a failing event")
	}
	if list, err := s.Devices(ctx); err != nil || len(list) != 0 {
		t.Fatalf("a device outlived its failed event: %v, %v", list, err)
	}
}

func TestADeviceSurvivesABackup(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	d, _ := s.AddDevice(ctx, "Pixel", "operator@example.com", hashA, "")
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
	first, _ := s.AddDevice(ctx, "Pixel", "", hashA, "")
	last, _ := s.AddDevice(ctx, "iPhone", "", hashB, "")
	if _, err := s.RevokeDevice(ctx, last.ID); err != nil {
		t.Fatal(err)
	}
	next, err := s.AddDevice(ctx, "Tablet", "", hashA, "")
	if err != nil || next.ID == last.ID || next.ID == first.ID {
		t.Fatalf("a new phone got number %d after %d and %d, %v", next.ID, first.ID, last.ID, err)
	}
	want := []string{"board.paired 1 Pixel", "board.paired 2 iPhone", "board.revoked 2 iPhone", "board.paired 3 Tablet"}
	if got := deviceEvents(t, s); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("events = %q, want %q", got, want)
	}
}

func TestAPhoneLabelTiesOnePairingsRecordsAndIsNotACredential(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	label := "0123456789abcdef"
	d, err := s.AddDevice(ctx, "Pixel", "operator@example.com", hashA, label)
	if err != nil || d.Phone != label {
		t.Fatalf("AddDevice = %+v, %v", d, err)
	}
	other, _ := s.AddDevice(ctx, "iPhone", "", hashB, "")
	if other.Phone != "" {
		t.Fatalf("a phone paired with no label = %+v", other)
	}
	list, err := s.Devices(ctx)
	if err != nil || len(list) != 2 || list[0].Phone != label || list[1].Phone != "" {
		t.Fatalf("Devices = %+v, %v", list, err)
	}
	if got, ok, err := s.DeviceWithSecret(ctx, d.ID, hashA); err != nil || !ok || got != d {
		t.Fatalf("DeviceWithSecret = %+v, %v, %v", got, ok, err)
	}
	if _, ok, _ := s.DeviceWithSecret(ctx, d.ID, label+label+label+label); ok {
		t.Fatal("the label opened a device")
	}
	gone, err := s.RevokeDevice(ctx, d.ID)
	if err != nil || gone.Phone != label {
		t.Fatalf("RevokeDevice = %+v, %v", gone, err)
	}
	events, err := s.RecentEvents(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if strings.Contains(e.Detail, label) {
			t.Fatalf("event %s %q carries the label", e.Kind, e.Detail)
		}
	}
}

func TestOnlyAnEmptyOrSixteenHexLabelIsAPhone(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	for name, label := range map[string]string{
		"short":     "0123456789abcde",
		"long":      "0123456789abcdef0",
		"upper":     "0123456789ABCDEF",
		"not hex":   "0123456789abcdeg",
		"a secret":  strings.Repeat("a", 64),
		"a newline": "0123456789abcde\n",
	} {
		if _, err := s.AddDevice(ctx, "Pixel", "", hashA, label); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s label %q = %v", name, label, err)
		}
	}
	if list, err := s.Devices(ctx); err != nil || len(list) != 0 {
		t.Fatalf("a refused label left %v, %v", list, err)
	}
}

func TestEveryRecordOfOnePairingKeepsItsOwnSecretHashAndNumber(t *testing.T) {
	first, _ := openTest(t)
	second, _ := openTest(t)
	ctx := context.Background()
	label := "fedcba9876543210"
	a, err := first.AddDevice(ctx, "Pixel", "", hashA, label)
	if err != nil {
		t.Fatal(err)
	}
	b, err := second.AddDevice(ctx, "Pixel", "", hashB, label)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := first.DeviceWithSecret(ctx, a.ID, hashB); ok {
		t.Fatal("one fleet's record opened with another fleet's hash")
	}
	if _, ok, _ := second.DeviceWithSecret(ctx, b.ID, hashA); ok {
		t.Fatal("one fleet's record opened with another fleet's hash")
	}
	if _, err := first.RevokeDevice(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := second.DeviceWithSecret(ctx, b.ID, hashB); !ok {
		t.Fatal("revoking in one store closed the record in another")
	}
}

func TestRevokingAPhoneRemovesEveryRecordOfItsLabelAndOnlyThose(t *testing.T) {
	label, other := "0123456789abcdef", "fedcba9876543210"
	hashC := strings.Repeat("c", 64)
	cases := []struct {
		what   string
		revoke string
		gone   []string
		kept   []string
		events []string
	}{
		{"one record of the label", label, []string{"Pixel"}, []string{"Old", "Tablet", "Twin"}, []string{"board.revoked 1 Pixel"}},
		{"another phone's label", other, []string{"Tablet", "Twin"}, []string{"Old", "Pixel"}, []string{"board.revoked 3 Tablet", "board.revoked 4 Twin"}},
		{"a label nobody holds", "aaaaaaaaaaaaaaaa", nil, []string{"Old", "Pixel", "Tablet", "Twin"}, nil},
	}
	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			s, _ := openTest(t)
			ctx := context.Background()
			for _, d := range []struct{ name, hash, label string }{{"Pixel", hashA, label}, {"Old", hashB, ""}, {"Tablet", hashC, other}, {"Twin", hashA, other}} {
				if _, err := s.AddDevice(ctx, d.name, "operator@example.com", d.hash, d.label); err != nil {
					t.Fatal(err)
				}
			}
			gone, err := s.RevokePhone(ctx, c.revoke)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, d := range gone {
				if d.Phone != c.revoke {
					t.Fatalf("revoked a record of another phone: %+v", d)
				}
				names = append(names, d.Name)
			}
			if strings.Join(names, ",") != strings.Join(c.gone, ",") {
				t.Fatalf("revoked %q, want %q", names, c.gone)
			}
			list, err := s.Devices(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var kept []string
			for _, d := range list {
				kept = append(kept, d.Name)
			}
			if slices.Compare(slices.Sorted(slices.Values(kept)), c.kept) != 0 {
				t.Fatalf("kept %q, want %q", kept, c.kept)
			}
			events := deviceEvents(t, s)[4:]
			if slices.Compare(events, c.events) != 0 {
				t.Fatalf("events = %q, want %q", events, c.events)
			}
			again, err := s.RevokePhone(ctx, c.revoke)
			if err != nil || len(again) != 0 {
				t.Fatalf("a repeated revoke = %+v, %v", again, err)
			}
			if got := deviceEvents(t, s)[4:]; slices.Compare(got, c.events) != 0 {
				t.Fatalf("a repeated revoke wrote events: %q", got)
			}
		})
	}
}

func TestRevokingAPhoneByAnEmptyOrBadLabelRevokesNothing(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	for _, d := range []struct{ hash, label string }{{hashA, ""}, {hashB, "0123456789abcdef"}} {
		if _, err := s.AddDevice(ctx, "Phone", "", d.hash, d.label); err != nil {
			t.Fatal(err)
		}
	}
	for name, label := range map[string]string{
		"empty":      "",
		"short":      "0123456789abcde",
		"upper":      "0123456789ABCDEF",
		"a wildcard": "%",
		"a clause":   "0123456789abcdef' OR '1'='1",
		"two":        "0123456789abcdef0123456789abcdef",
	} {
		if got, err := s.RevokePhone(ctx, label); !errors.Is(err, ErrInvalid) || len(got) != 0 {
			t.Errorf("%s label = %+v, %v", name, got, err)
		}
	}
	if list, err := s.Devices(ctx); err != nil || len(list) != 2 {
		t.Fatalf("a refused label left %v, %v", list, err)
	}
	if got := deviceEvents(t, s); len(got) != 2 {
		t.Fatalf("a refused label wrote events: %q", got)
	}
}

func TestRevokingAPhoneAndItsEventsShareOneTransaction(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	label := "0123456789abcdef"
	for _, h := range []string{hashA, hashB} {
		if _, err := s.AddDevice(ctx, "Pixel", "", h, label); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`CREATE TRIGGER no_revoked BEFORE INSERT ON event WHEN NEW.kind = 'board.revoked' AND NEW.detail LIKE '2 %' BEGIN SELECT RAISE(ABORT, 'no event'); END`); err != nil {
		t.Fatal(err)
	}
	if got, err := s.RevokePhone(ctx, label); err == nil || len(got) != 0 {
		t.Fatalf("RevokePhone survived a failing event: %+v, %v", got, err)
	}
	if list, err := s.Devices(ctx); err != nil || len(list) != 2 {
		t.Fatalf("a revoke outlived its failed event: %v, %v", list, err)
	}
}

func TestEveryRecordRevokedByLabelKeepsTheNumbersItHeld(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	label := "0123456789abcdef"
	first, _ := s.AddDevice(ctx, "Pixel", "", hashA, label)
	if _, err := s.RevokePhone(ctx, label); err != nil {
		t.Fatal(err)
	}
	next, err := s.AddDevice(ctx, "Pixel", "", hashA, label)
	if err != nil || next.ID == first.ID {
		t.Fatalf("a new record got number %d after %d, %v", next.ID, first.ID, err)
	}
}

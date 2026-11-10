package boot

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestFromFileReadsTheLinuxForm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "boot_id")
	if err := os.WriteFile(path, []byte("6f1c2d3e-aaaa-4bbb-8ccc-0123456789ab\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := FromFile(path); got != "6f1c2d3e-aaaa-4bbb-8ccc-0123456789ab" {
		t.Fatalf("FromFile = %q", got)
	}
	if got := FromFile(filepath.Join(t.TempDir(), "absent")); got != "" {
		t.Fatalf("an unreadable file gave %q, want no id", got)
	}
}

func TestFromTicksNamesTheBootSecond(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	if got := FromTicks(now, 90_000); got != "ticks:1799999910" {
		t.Fatalf("FromTicks = %q", got)
	}
	if got := FromTicks(now, 1<<41); got != "" {
		t.Fatalf("an absurd uptime gave %q", got)
	}
}

func TestSameAcrossTheThreeForms(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	for _, c := range []struct {
		name, a, b string
		want       bool
	}{
		{"linux equal", "6f1c2d3e-aaaa-4bbb-8ccc-0123456789ab", "6f1c2d3e-aaaa-4bbb-8ccc-0123456789ab", true},
		{"linux differs", "6f1c2d3e-aaaa-4bbb-8ccc-0123456789ab", "7f1c2d3e-aaaa-4bbb-8ccc-0123456789ab", false},
		{"macos case", "6F1C2D3E-AAAA-4BBB-8CCC-0123456789AB", "6f1c2d3e-aaaa-4bbb-8ccc-0123456789ab", true},
		{"macos differs", "6F1C2D3E-AAAA-4BBB-8CCC-0123456789AB", "7F1C2D3E-AAAA-4BBB-8CCC-0123456789AB", false},
		{"windows jitter", FromTicks(now, 600_000), FromTicks(now.Add(3*time.Second), 603_000+2_000), true},
		{"windows other boot", FromTicks(now, 600_000), FromTicks(now.Add(30*time.Minute), 600_000), false},
		{"windows beside linux", "ticks:1799999400", "6f1c2d3e-aaaa-4bbb-8ccc-0123456789ab", false},
		{"unknown matches anything", "", "6f1c2d3e-aaaa-4bbb-8ccc-0123456789ab", true},
		{"both unknown", "", "", true},
		{"windows garbage", "ticks:x", "ticks:y", false},
	} {
		if got := Same(c.a, c.b); got != c.want {
			t.Errorf("%s: Same(%q, %q) = %v, want %v", c.name, c.a, c.b, got, c.want)
		}
	}
}

func TestIDIsStableWithinABoot(t *testing.T) {
	a := ID()
	switch runtime.GOOS {
	case "linux", "darwin", "windows":
		if a == "" {
			t.Fatal("no boot id on a system that has one")
		}
	default:
		t.Skip("no boot id here")
	}
	if b := ID(); !Same(a, b) {
		t.Fatalf("two reads differ: %q then %q", a, b)
	}
}

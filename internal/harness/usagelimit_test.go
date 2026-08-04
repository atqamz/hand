package harness

import (
	"testing"
	"time"
)

func TestSupportsUsageLimitOnlyWhereASignatureIsCatalogued(t *testing.T) {
	if !SupportsUsageLimit(Claude) {
		t.Fatal("SupportsUsageLimit(claude) = false, want true")
	}
	for _, name := range []string{Codex, Grok, Pi, OpenCode, "", "nonesuch"} {
		if SupportsUsageLimit(name) {
			t.Errorf("SupportsUsageLimit(%q) = true, want false: no signature is catalogued for it", name)
		}
	}
}

func TestDetectUsageLimitRecognizesEveryCatalogedWording(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	for _, text := range []string{
		"Claude usage limit reached. Your limit will reset at 3pm (UTC).",
		"Claude AI usage limit reached|1785063600",
		"5-hour limit reached - your limit will reset at 4pm",
		"You've reached your weekly limit for Opus.",
	} {
		if _, limited := DetectUsageLimit(Claude, text, now); !limited {
			t.Errorf("DetectUsageLimit(%q) = false, want true", text)
		}
	}
}

// The warning claude prints as quota runs low does not stop the turn, so reading it as
// a stop would strand a working worker under a limit hold and steer it mid-task.
func TestDetectUsageLimitIgnoresTextThatIsNotAStop(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	for _, text := range []string{
		"Approaching your usage limit - 10% of your 5-hour window remains",
		"Set a spend limit for this account",
		"go test -race ./... passed; rate limit handling still to do",
		"",
	} {
		if _, limited := DetectUsageLimit(Claude, text, now); limited {
			t.Errorf("DetectUsageLimit(%q) = true, want false", text)
		}
	}
}

func TestDetectUsageLimitDeclinesForAnUncataloguedHarness(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	text := "Claude usage limit reached. Your limit will reset at 3pm (UTC)."
	if _, limited := DetectUsageLimit(Codex, text, now); limited {
		t.Fatal("DetectUsageLimit(codex) = true: claude's wording must not be matched against another harness")
	}
}

func TestDetectUsageLimitReadsTheResetInstant(t *testing.T) {
	utcNow := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		text string
		now  time.Time
		want time.Time
	}{
		{
			name: "epoch form is exact",
			text: "Claude AI usage limit reached|1785063600",
			now:  utcNow,
			want: time.Unix(1785063600, 0).UTC(),
		},
		{
			name: "clock form with a named zone",
			text: "Claude usage limit reached. Your limit will reset at 3pm (UTC).",
			now:  utcNow,
			want: time.Date(2026, 8, 4, 15, 0, 0, 0, time.UTC),
		},
		{
			name: "clock form with minutes",
			text: "Claude usage limit reached. Your limit will reset at 11:30am (UTC).",
			now:  utcNow,
			want: time.Date(2026, 8, 4, 11, 30, 0, 0, time.UTC),
		},
		{
			// A reset is always ahead of the message announcing it, so a clock time
			// already past today names tomorrow's occurrence.
			name: "clock form already past today rolls over",
			text: "Claude usage limit reached. Your limit will reset at 9am (UTC).",
			now:  utcNow,
			want: time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC),
		},
		{
			name: "midnight is not noon",
			text: "Claude usage limit reached. Your limit will reset at 12am (UTC).",
			now:  utcNow,
			want: time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, limited := DetectUsageLimit(Claude, tt.text, tt.now)
			if !limited {
				t.Fatalf("DetectUsageLimit(%q) = false, want true", tt.text)
			}
			if !got.Equal(tt.want) {
				t.Fatalf("reset = %s, want %s", got, tt.want)
			}
		})
	}
}

// A limit whose message names no reset instant is still a limit. The caller must be
// able to tell the two apart, since the instant is what it schedules the first attempt
// from and its absence means fall back to the floor.
func TestDetectUsageLimitReportsALimitWithNoNamedReset(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	reset, limited := DetectUsageLimit(Claude, "Claude usage limit reached.", now)
	if !limited {
		t.Fatal("DetectUsageLimit = false, want true")
	}
	if !reset.IsZero() {
		t.Fatalf("reset = %s, want the zero instant: the message named none", reset)
	}
}

// An unloadable zone name must not cost the detection: a wait computed in the host's
// zone is still a bounded wait, and the attempt is what decides the limit is over.
func TestDetectUsageLimitFallsBackWhenTheZoneIsUnloadable(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	reset, limited := DetectUsageLimit(Claude, "Claude usage limit reached. Your limit will reset at 3pm (Nowhere/Nothing).", now)
	if !limited {
		t.Fatal("DetectUsageLimit = false, want true")
	}
	if want := time.Date(2026, 8, 4, 15, 0, 0, 0, time.UTC); !reset.Equal(want) {
		t.Fatalf("reset = %s, want %s resolved in the host zone", reset, want)
	}
}

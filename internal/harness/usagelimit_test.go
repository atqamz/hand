package harness

import (
	"testing"
	"time"
)

func TestSupportsUsageLimitOnlyWhereASignatureIsCatalogued(t *testing.T) {
	for _, name := range []string{Claude, Codex} {
		if !SupportsUsageLimit(name) {
			t.Errorf("SupportsUsageLimit(%q) = false, want true", name)
		}
	}
	for _, name := range []string{Grok, Pi, OpenCode, Antigravity, "", "nonesuch"} {
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
	if _, limited := DetectUsageLimit(Grok, text, now); limited {
		t.Fatal("DetectUsageLimit(grok) = true: claude's wording must not be matched against another harness")
	}
}

func TestDetectUsageLimitDoesNotCrossMatchClaudeAndCodex(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	claudeText := "Claude usage limit reached. Your limit will reset at 3pm (UTC)."
	if _, limited := DetectUsageLimit(Codex, claudeText, now); limited {
		t.Fatal("DetectUsageLimit(codex, claude's wording) = true, want false")
	}
	codexText := "You've hit your usage limit. Upgrade to Pro (https://chatgpt.com/explore/pro), " +
		"visit https://chatgpt.com/codex/settings/usage to purchase more credits or try again at 7:27 PM."
	if _, limited := DetectUsageLimit(Claude, codexText, now); limited {
		t.Fatal("DetectUsageLimit(claude, codex's wording) = true, want false")
	}
}

// The verbatim refusal from atqamz/hand#435: two codex workers hit the limit simultaneously and
// the claude-anchored pattern could not match "hit", only "reached".
func TestDetectUsageLimitRecognizesTheObservedCodexRefusal(t *testing.T) {
	now := time.Date(2026, 8, 27, 18, 21, 0, 0, time.FixedZone("WIB", 7*3600))
	text := "■ You've hit your usage limit. Upgrade to Pro (https://chatgpt.com/explore/pro), " +
		"visit https://chatgpt.com/codex/settings/usage to purchase more credits or try again at 7:27 PM."
	reset, limited := DetectUsageLimit(Codex, text, now)
	if !limited {
		t.Fatal("DetectUsageLimit(codex, observed refusal) = false, want true")
	}
	want := time.Date(2026, 8, 27, 19, 27, 0, 0, now.Location())
	if !reset.Equal(want) {
		t.Fatalf("reset = %s, want %s", reset, want)
	}
}

// The warning codex prints as quota runs low - if it ever does - must not read as a stop: the
// signature is anchored on "hit", the word the real refusal uses, not on bare "limit".
func TestDetectUsageLimitIgnoresCodexTextThatIsNotAStop(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	for _, text := range []string{
		"You're approaching your usage limit.",
		"10% of your usage limit remains.",
		"Set a spend limit for this account",
		"",
	} {
		if _, limited := DetectUsageLimit(Codex, text, now); limited {
			t.Errorf("DetectUsageLimit(codex, %q) = true, want false", text)
		}
	}
}

func TestDetectCodexUsageLimitResetInstant(t *testing.T) {
	tests := []struct {
		name string
		text string
		now  time.Time
		want time.Time
		ok   bool
	}{
		{
			// The exact observed shape.
			name: "observed shape",
			text: "You've hit your usage limit. ... or try again at 7:27 PM.",
			now:  time.Date(2026, 8, 27, 18, 21, 0, 0, time.UTC),
			want: time.Date(2026, 8, 27, 19, 27, 0, 0, time.UTC),
			ok:   true,
		},
		{
			// A shape the parser must not guess at: no reset is better than a wrong one.
			name: "unparseable shape does not parse",
			text: "You've hit your usage limit. Try again later.",
			now:  time.Date(2026, 8, 27, 18, 21, 0, 0, time.UTC),
			ok:   false,
		},
		{
			// Dateless and zoneless: a clock time earlier than now rolls to tomorrow.
			name: "rollover across midnight",
			text: "You've hit your usage limit. ... or try again at 12:15 AM.",
			now:  time.Date(2026, 8, 27, 23, 50, 0, 0, time.UTC),
			want: time.Date(2026, 8, 28, 0, 15, 0, 0, time.UTC),
			ok:   true,
		},
		{
			// A clock time still ahead of now resolves to today, no rollover.
			name: "clock time later today",
			text: "You've hit your usage limit. ... or try again at 11:05 AM.",
			now:  time.Date(2026, 8, 27, 9, 0, 0, 0, time.UTC),
			want: time.Date(2026, 8, 27, 11, 5, 0, 0, time.UTC),
			ok:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reset, limited := DetectUsageLimit(Codex, tt.text, tt.now)
			if !limited {
				t.Fatalf("DetectUsageLimit(codex, %q) limited = false, want true: text still names a stop even when the reset does not parse", tt.text)
			}
			if reset.IsZero() != !tt.ok {
				t.Fatalf("reset zero = %v, want ok=%v", reset.IsZero(), tt.ok)
			}
			if tt.ok && !reset.Equal(tt.want) {
				t.Fatalf("reset = %s, want %s", reset, tt.want)
			}
		})
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

// Scrollback accumulates refusals: the one a resume attempt just provoked sits under
// the one that stranded the worker in the first place. Reading the older prediction is
// how a worker whose quota returns at 8pm gets left until tomorrow's 3pm instead.
func TestDetectUsageLimitReadsTheFreshestRefusal(t *testing.T) {
	now := time.Date(2026, 8, 4, 16, 0, 0, 0, time.UTC)
	text := "Claude usage limit reached. Your limit will reset at 3pm (UTC).\n" +
		"> Your previous turn stopped on a usage limit.\n" +
		"Claude usage limit reached. Your limit will reset at 8pm (UTC).\n"
	reset, limited := DetectUsageLimit(Claude, text, now)
	if !limited {
		t.Fatal("DetectUsageLimit = false, want true")
	}
	if want := time.Date(2026, 8, 4, 20, 0, 0, 0, time.UTC); !reset.Equal(want) {
		t.Fatalf("reset = %s, want %s from the refusal the last attempt provoked", reset, want)
	}
}

// The same, the other way around: the freshest refusal owns the answer even when it is
// the one that names no instant at all, since the stale one's is a prediction the
// harness has already superseded.
func TestDetectUsageLimitDoesNotFallBackToAStaleReset(t *testing.T) {
	now := time.Date(2026, 8, 4, 16, 0, 0, 0, time.UTC)
	text := "Claude usage limit reached. Your limit will reset at 3pm (UTC).\n" +
		"> Your previous turn stopped on a usage limit.\n" +
		"Claude usage limit reached.\n"
	reset, limited := DetectUsageLimit(Claude, text, now)
	if !limited {
		t.Fatal("DetectUsageLimit = false, want true")
	}
	if !reset.IsZero() {
		t.Fatalf("reset = %s, want the zero instant: the freshest refusal named none", reset)
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

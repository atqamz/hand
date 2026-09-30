package board

import (
	"testing"

	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/transcript"
)

func TestGaugeFormat(t *testing.T) {
	for _, c := range []struct {
		in          transcript.Status
		text, level string
	}{
		{transcript.Status{}, "", ""},
		{transcript.Status{Context: 508311}, "CTX 508K", ""},
		{transcript.Status{Context: 1234567}, "CTX 1.23M", ""},
		{transcript.Status{Context: 72479, Window: 258400}, "CTX 72.5K / 258K · 28%", ""},
		{transcript.Status{Context: 210000, Window: 258400}, "CTX 210K / 258K · 81%", "warn"},
		{transcript.Status{Context: 240000, Window: 258400}, "CTX 240K / 258K · 93% COMPACT SOON", "fail"},
		{transcript.Status{Context: 950}, "CTX 950", ""},
	} {
		if text, level := gauge(c.in); text != c.text || level != c.level {
			t.Errorf("gauge(%+v) = %q %q, want %q %q", c.in, text, level, c.text, c.level)
		}
	}
}

func TestDispatchesNameTheSupervisorOfTheirTime(t *testing.T) {
	sups := []state.Supervisor{{ID: 1, CreatedAt: "2026-09-29T10:00:00Z"}, {ID: 2, CreatedAt: "2026-09-29T11:00:00.5Z"}}
	for at, want := range map[string]string{
		"2026-09-29T09:59:00Z":     "s1",
		"2026-09-29T10:30:00Z":     "s1",
		"2026-09-29T11:00:00.5Z":   "s2",
		"2026-09-29T12:00:00.000Z": "s2",
		"not a time":               "s1",
	} {
		if got := refAt(sups, at); got != want {
			t.Errorf("refAt(%q) = %q, want %q", at, got, want)
		}
	}
	if got := refAt(nil, "2026-09-29T10:00:00Z"); got != "" {
		t.Errorf("refAt with no supervisors = %q", got)
	}
}

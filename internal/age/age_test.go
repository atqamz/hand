package age

import (
	"testing"
	"time"
)

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "just now"},
		{30 * time.Second, "just now"},
		{time.Minute, "1m"},
		{5 * time.Minute, "5m"},
		{59*time.Minute + 59*time.Second, "59m"},
		{time.Hour, "1h"},
		{2 * time.Hour, "2h"},
		{23*time.Hour + 59*time.Minute, "23h"},
		{24 * time.Hour, "1d"},
		{3 * 24 * time.Hour, "3d"},
	}
	for _, c := range cases {
		if got := FormatDuration(c.d); got != c.want {
			t.Errorf("FormatDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestFormatAge(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		delta time.Duration
		want  string
	}{
		{-30 * time.Second, "just now"},
		{-5 * time.Minute, "5m"},
		{-2 * time.Hour, "2h"},
		{-3 * 24 * time.Hour, "3d"},
	}
	for _, c := range cases {
		got := FormatAge(now.Add(c.delta).Format(time.RFC3339))
		if got != c.want {
			t.Errorf("FormatAge(%v) = %q, want %q", c.delta, got, c.want)
		}
	}
	if got := FormatAge("not-a-time"); got != "unknown" {
		t.Errorf("FormatAge(invalid) = %q, want unknown", got)
	}
	if got := FormatAge(""); got != "unknown" {
		t.Errorf("FormatAge(empty) = %q, want unknown", got)
	}
}

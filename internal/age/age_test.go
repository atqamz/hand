package age

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"pgregory.net/rapid"
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

// Orders FormatDuration's output by the vocabulary its own doc comment names
// - "just now" < Nm < Nh < Nd - letting INV-PURE-3 compare renderings
// without recomputing FormatDuration's own arithmetic as the oracle.
func ageRank(s string) (category, magnitude int) {
	if s == "just now" {
		return 0, 0
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil {
		panic(fmt.Sprintf("age output %q does not match the documented vocabulary: %v", s, err))
	}
	switch s[len(s)-1] {
	case 'm':
		return 1, n
	case 'h':
		return 2, n
	case 'd':
		return 3, n
	default:
		panic(fmt.Sprintf("age output %q does not match the documented vocabulary", s))
	}
}

// Draws from each of FormatDuration's four buckets - just now, minutes,
// hours, days - with comparable weight, since a uniform full-range draw
// would make the hour and minute buckets a vanishing sliver next to a decade of days.
func durationGen() *rapid.Generator[time.Duration] {
	return rapid.Map(rapid.OneOf(
		rapid.Int64Range(int64(-24*time.Hour), int64(time.Minute)-1),
		rapid.Int64Range(int64(time.Minute), int64(time.Hour)-1),
		rapid.Int64Range(int64(time.Hour), int64(24*time.Hour)-1),
		rapid.Int64Range(int64(24*time.Hour), int64(3650*24*time.Hour)),
	), func(n int64) time.Duration { return time.Duration(n) })
}

// INV-PURE-3: a larger duration never renders as a smaller age.
func TestFormatDurationIsMonotonic(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := durationGen().Draw(t, "a")
		b := durationGen().Draw(t, "b")
		if a > b {
			a, b = b, a
		}

		aCat, aMag := ageRank(FormatDuration(a))
		bCat, bMag := ageRank(FormatDuration(b))
		if aCat > bCat || (aCat == bCat && aMag > bMag) {
			t.Fatalf("FormatDuration(%s)=%q ranks above FormatDuration(%s)=%q despite %s <= %s",
				a, FormatDuration(a), b, FormatDuration(b), a, b)
		}
	})
}

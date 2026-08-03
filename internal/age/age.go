// Package age renders elapsed time as the compact strings hand status and the
// watcher both surface: "just now", "45m", "2h", "3d".
package age

import (
	"fmt"
	"time"
)

// FormatAge renders the elapsed time since createdAt (RFC3339). Returns
// "unknown" if createdAt doesn't parse.
func FormatAge(createdAt string) string {
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return "unknown"
	}
	return FormatDuration(time.Since(t))
}

func FormatDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

package transcript

import (
	"strings"
	"time"
)

type Sent struct {
	ID        int64
	Body      string
	CreatedAt string
}

func Answers(entries []Entry, sent []Sent) map[int64]string {
	out := map[int64]string{}
	taken := make([]bool, len(entries))
	for _, in := range sent {
		created, err := time.Parse(time.RFC3339Nano, in.CreatedAt)
		if err != nil {
			continue
		}
		for i, e := range entries {
			at, err := time.Parse(time.RFC3339Nano, e.At)
			if taken[i] || e.Role != "operator" || err != nil || at.Before(created) || strings.TrimSpace(e.Text) != strings.TrimSpace(in.Body) {
				continue
			}
			taken[i] = true
			for _, r := range entries[i+1:] {
				if _, err := time.Parse(time.RFC3339Nano, r.At); r.Role == "supervisor" && err == nil {
					out[in.ID] = r.At
					break
				}
			}
			break
		}
	}
	return out
}

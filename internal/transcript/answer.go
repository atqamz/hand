package transcript

import (
	"slices"
	"strings"
	"time"
)

type Sent struct {
	ID        int64
	Body      string
	CreatedAt string
}

func Pair(entries []Entry, sent []Sent) map[int64]int {
	out := map[int64]int{}
	taken := make([]bool, len(entries))
	for _, in := range slices.SortedFunc(slices.Values(sent), func(a, b Sent) int { return int(a.ID - b.ID) }) {
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
			out[in.ID] = i
			break
		}
	}
	return out
}

func Answers(entries []Entry, sent []Sent) map[int64]string {
	out := map[int64]string{}
	for id, i := range Pair(entries, sent) {
		for _, r := range entries[i+1:] {
			if _, err := time.Parse(time.RFC3339Nano, r.At); r.Role == "supervisor" && err == nil {
				out[id] = r.At
				break
			}
		}
	}
	return out
}

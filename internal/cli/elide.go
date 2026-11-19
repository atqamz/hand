package cli

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	reportShowBytes = 8 << 10
	goalShowBytes   = 4 << 10
	planShowBytes   = 8 << 10
	maxReadLines    = 500
)

func elide(s string, limit int, command string) string {
	if len(s) <= limit {
		return s
	}
	tail := limit / 8
	headEnd := lineCut(s, limit-tail)
	tailStart := len(s) - tail
	if i := strings.IndexByte(s[tailStart-1:], '\n'); i >= 0 && tail-i >= tail/2 {
		tailStart += i
	} else {
		for tailStart < len(s) && !utf8.RuneStart(s[tailStart]) {
			tailStart++
		}
	}
	head := strings.TrimRight(s[:headEnd], "\r\n")
	end := strings.TrimLeft(s[tailStart:], "\r\n")
	var kept []string
	keptBytes := 0
	for _, l := range strings.Split(s[headEnd:tailStart], "\n") {
		l = strings.TrimRight(l, "\r")
		if strings.HasPrefix(strings.TrimLeft(l, " \t-*"), "Delivered:") && keptBytes+len(l)+1 <= tail {
			kept = append(kept, l)
			keptBytes += len(l) + 1
		}
	}
	omitted := len(s) - len(head) - keptBytes - len(end)
	out := fmt.Sprintf("%s\n… %d bytes omitted; `%s` prints all\n", head, omitted, command)
	if len(kept) > 0 {
		out += strings.Join(kept, "\n") + "\n"
	}
	out += end
	if len(out) >= len(s) {
		return s
	}
	return out
}

func lineCut(s string, n int) int {
	if cut := strings.LastIndexByte(s[:n+1], '\n'); cut >= n/2 {
		return cut
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return n
}

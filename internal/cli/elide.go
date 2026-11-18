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

func elide(s string, limit int, flag string) string {
	if len(s) <= limit {
		return s
	}
	cut := strings.LastIndexByte(s[:limit], '\n')
	if cut <= 0 {
		cut = limit
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
	}
	var kept []string
	rest := s[cut:]
	for _, l := range strings.Split(rest, "\n") {
		if strings.HasPrefix(strings.TrimLeft(l, " \t-*"), "Delivered:") {
			kept = append(kept, l)
		}
	}
	omitted := len(rest) - len(strings.Join(kept, "\n"))
	out := fmt.Sprintf("%s\n… %d bytes omitted; `%s` prints all", strings.TrimRight(s[:cut], "\n"), omitted, flag)
	if len(kept) > 0 {
		out += "\n" + strings.Join(kept, "\n")
	}
	return out
}

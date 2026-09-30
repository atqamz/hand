package luvus

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

const ScreenLines = 20

var countdown = regexp.MustCompile(`(deny this request in )\d+:\d{2}`)

func ScreenDigest(text string) string {
	lines := strings.Split(countdown.ReplaceAllString(text, "${1}_"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t ")
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:16])
}

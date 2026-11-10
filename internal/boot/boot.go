package boot

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	ticksPrefix    = "ticks:"
	ticksTolerance = 5

	maxUptimeMillis = 1 << 40
)

func FromFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func FromTicks(now time.Time, uptimeMillis uint64) string {
	if uptimeMillis > maxUptimeMillis {
		return ""
	}
	return ticksPrefix + strconv.FormatInt(now.Add(-time.Duration(uptimeMillis)*time.Millisecond).Unix(), 10)
}

func Same(a, b string) bool {
	if a == "" || b == "" {
		return true
	}
	ta, aok := strings.CutPrefix(a, ticksPrefix)
	tb, bok := strings.CutPrefix(b, ticksPrefix)
	if aok != bok {
		return false
	}
	if !aok {
		return strings.EqualFold(a, b)
	}
	x, errA := strconv.ParseInt(ta, 10, 64)
	y, errB := strconv.ParseInt(tb, 10, 64)
	if errA != nil || errB != nil {
		return a == b
	}
	return max(x-y, y-x) <= ticksTolerance
}

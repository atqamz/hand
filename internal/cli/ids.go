package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/atqamz/hand/internal/state"
)

func parseID(prefix, s string) (int64, error) {
	n, err := strconv.ParseInt(strings.TrimPrefix(s, prefix), 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%w: %q is not a %s id", state.ErrInvalid, s, prefix)
	}
	return n, nil
}

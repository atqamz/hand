//go:build !linux

package update

import (
	"context"

	"github.com/atqamz/hand/internal/fleet"
)

func (r *Report) retireWatchUnits(context.Context, Options, []fleet.Entry) {}

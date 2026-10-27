//go:build linux

package cli

var (
	AttemptDir = attemptDir
	Sweep      = sweepWorktree
	Ancestry   = ancestry
)

func EndLeftover(pid int, marker string) { endLeftovers(nil, []leftover{{pid, marker}}) }

//go:build !linux && !darwin && !windows

package boot

func ID() string { return "" }

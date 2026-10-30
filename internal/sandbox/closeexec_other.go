//go:build !linux

package sandbox

func CloseOnExec() error { return nil }

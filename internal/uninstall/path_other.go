//go:build !windows

package uninstall

func userPath(string, bool) (bool, error) { return false, nil }

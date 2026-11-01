//go:build !unix

package harness

import "io/fs"

func ownedBy(fs.FileInfo, int) bool { return true }

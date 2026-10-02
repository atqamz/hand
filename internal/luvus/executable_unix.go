//go:build unix

package luvus

import "os"

func executable(fi os.FileInfo) bool { return fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 }

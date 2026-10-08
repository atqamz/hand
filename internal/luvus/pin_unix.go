//go:build unix

package luvus

import "os"

func executable(fi os.FileInfo) bool { return fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 }

func exeExt(string) string { return "" }

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

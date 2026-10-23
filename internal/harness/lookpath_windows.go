package harness

import "os"

func LookPath(name, path string) (string, error) {
	return lookPathExt(name, path, os.Getenv("PATHEXT"))
}

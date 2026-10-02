package cli

import "os"

func HostGetenv(key string) string {
	if v := os.Getenv(key); v != "" || key != "HOME" {
		return v
	}
	return os.Getenv("USERPROFILE")
}

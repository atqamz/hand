package luvus

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func ProcStartMarker(pid int) (string, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", err
	}
	s := string(b)
	i := strings.LastIndex(s, ") ")
	if i < 0 {
		return "", fmt.Errorf("malformed /proc/%d/stat", pid)
	}
	f := strings.Fields(s[i+2:])
	if len(f) < 20 {
		return "", fmt.Errorf("short /proc/%d/stat", pid)
	}
	return f[19], nil
}

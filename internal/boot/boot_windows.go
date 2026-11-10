package boot

import (
	"time"

	"golang.org/x/sys/windows"
)

var tickCount64 = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetTickCount64")

func ID() string {
	if tickCount64.Find() != nil {
		return ""
	}
	ms, _, _ := tickCount64.Call()
	return FromTicks(time.Now(), uint64(ms))
}

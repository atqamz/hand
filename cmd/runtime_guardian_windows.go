//go:build windows

package cmd

import (
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	runtimeGuardianJob     windows.Handle
	runtimeGuardianJobErr  error
	runtimeGuardianJobOnce sync.Once
)

func protectRuntimeGuardian() error {
	runtimeGuardianJobOnce.Do(func() {
		job, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			runtimeGuardianJobErr = err
			return
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		if _, err := windows.SetInformationJobObject(
			job,
			windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)),
			uint32(unsafe.Sizeof(info)),
		); err != nil {
			_ = windows.CloseHandle(job)
			runtimeGuardianJobErr = err
			return
		}
		if err := windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
			_ = windows.CloseHandle(job)
			runtimeGuardianJobErr = err
			return
		}
		// The guardian intentionally retains the sole job handle until process
		// exit. Windows then terminates every managed descendant atomically.
		runtimeGuardianJob = job
	})
	return runtimeGuardianJobErr
}

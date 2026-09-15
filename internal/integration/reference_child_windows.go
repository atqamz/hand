//go:build windows

package integration

import (
	"os"
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	payloadReferenceJob     windows.Handle
	payloadReferenceJobErr  error
	payloadReferenceJobOnce sync.Once
)

func startChildWithPayloadReference(cmd *exec.Cmd, _ *os.File) error {
	payloadReferenceJobOnce.Do(func() {
		job, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			payloadReferenceJobErr = err
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
			payloadReferenceJobErr = err
			return
		}
		if err := windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
			_ = windows.CloseHandle(job)
			payloadReferenceJobErr = err
			return
		}
		payloadReferenceJob = job
	})
	if payloadReferenceJobErr != nil {
		return payloadReferenceJobErr
	}
	return cmd.Start()
}

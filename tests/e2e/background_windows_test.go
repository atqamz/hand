//go:build e2e && windows

package e2e

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestBackgroundJobActiveProcesses(t *testing.T) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.CloseHandle(job) }()
	active, err := backgroundJobActiveProcesses(job)
	if err != nil || active != 0 {
		t.Fatalf("new job active processes = %d, %v; want zero", active, err)
	}
}

func TestBackgroundJobJoinsDescendantAfterTimeout(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/C", "ping -n 30 127.0.0.1 >NUL")
	process, err := startBackgroundProcess(cmd)
	if err != nil {
		t.Fatal(err)
	}
	p := process.(*windowsBackgroundProcess)
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			p.stop()
			_ = cmd.Wait()
		}
		if err := p.close(); err != nil {
			t.Errorf("close background job: %v", err)
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		active, err := backgroundJobActiveProcesses(p.job)
		if err != nil {
			t.Fatal(err)
		}
		if active >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background job active processes = %d, want parent and ping descendant", active)
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, timedOut := p.wait(20 * time.Millisecond)
	if err := p.close(); err != nil {
		t.Fatal(err)
	}
	if !timedOut {
		t.Fatal("background job exited before timeout")
	}
}

func TestBackgroundJobCloseZeroHandles(t *testing.T) {
	if err := (&windowsBackgroundProcess{}).close(); err != nil {
		t.Fatal(err)
	}
}

func TestBackgroundJobCloseReportsHandleErrors(t *testing.T) {
	p := &windowsBackgroundProcess{process: windows.InvalidHandle, job: windows.InvalidHandle}
	err := p.close()
	if err == nil || !strings.Contains(err.Error(), "close background hand process handle") || !strings.Contains(err.Error(), "inspect background hand job") {
		t.Fatalf("close invalid handles: %v", err)
	}
	if err := p.close(); err != nil {
		t.Fatalf("close cleared handles: %v", err)
	}
}

type windowsBackgroundProcess struct {
	cmd     *exec.Cmd
	job     windows.Handle
	process windows.Handle
}

type backgroundJobAccounting struct {
	totalUserTime            int64
	totalKernelTime          int64
	periodUserTime           int64
	periodKernelTime         int64
	totalPageFaultCount      uint32
	totalProcesses           uint32
	activeProcesses          uint32
	totalTerminatedProcesses uint32
}

func backgroundJobActiveProcesses(job windows.Handle) (uint32, error) {
	var accounting backgroundJobAccounting
	err := windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)), nil)
	return accounting.activeProcesses, err
}

func startBackgroundProcess(cmd *exec.Cmd) (backgroundProcess, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = new(syscall.SysProcAttr)
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	if err := cmd.Start(); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	p := &windowsBackgroundProcess{cmd: cmd, job: job}
	p.process, err = windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(cmd.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, p.process)
	}
	if err == nil {
		err = resumeBackgroundThread(uint32(cmd.Process.Pid))
	}
	if err != nil {
		p.stop()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("contain background hand process: %w", errors.Join(err, p.close()))
	}
	return p, nil
}

func (p *windowsBackgroundProcess) stop() {
	_ = windows.TerminateJobObject(p.job, 1)
}

func (p *windowsBackgroundProcess) wait(timeout time.Duration) (error, bool) {
	ms := uint32(max(1, timeout.Milliseconds()))
	status, err := windows.WaitForSingleObject(p.process, ms)
	if err != nil {
		p.stop()
		waitErr := p.cmd.Wait()
		return errors.Join(err, waitErr), false
	}
	if status != windows.WAIT_OBJECT_0 {
		p.stop()
		_, waitErr := windows.WaitForSingleObject(p.process, windows.INFINITE)
		cmdErr := p.cmd.Wait()
		if status == uint32(windows.WAIT_TIMEOUT) {
			return errors.Join(waitErr, cmdErr), true
		}
		return errors.Join(fmt.Errorf("wait for background hand process: %d", status), waitErr, cmdErr), false
	}
	p.stop()
	cmdErr := p.cmd.Wait()
	return cmdErr, false
}

func (p *windowsBackgroundProcess) close() error {
	var closeErr error
	if p.process != 0 {
		if err := windows.CloseHandle(p.process); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("close background hand process handle: %w", err))
		}
		p.process = 0
	}
	if p.job != 0 {
		if err := windows.TerminateJobObject(p.job, 1); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("terminate background hand job: %w", err))
		}
		deadline := time.Now().Add(10 * time.Second)
		for {
			active, err := backgroundJobActiveProcesses(p.job)
			if err != nil {
				closeErr = errors.Join(closeErr, fmt.Errorf("inspect background hand job: %w", err))
				break
			}
			if active == 0 {
				break
			}
			if time.Now().After(deadline) {
				closeErr = errors.Join(closeErr, fmt.Errorf("background hand job still has %d active processes", active))
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		if err := windows.CloseHandle(p.job); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("close background hand job: %w", err))
		}
		p.job = 0
	}
	return closeErr
}

func resumeBackgroundThread(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		return err
	}
	for {
		if entry.OwnerProcessID == pid {
			thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if err != nil {
				return err
			}
			_, resumeErr := windows.ResumeThread(thread)
			closeErr := windows.CloseHandle(thread)
			return errors.Join(resumeErr, closeErr)
		}
		if err := windows.Thread32Next(snapshot, &entry); err != nil {
			return fmt.Errorf("find suspended background hand thread: %w", err)
		}
	}
}

//go:build windows

package selfupdate

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWindowsReplacementChildProcess(t *testing.T) {
	if os.Getenv("HAND_SELFUPDATE_CHILD") != "1" {
		return
	}
	if err := os.WriteFile(os.Getenv("HAND_SELFUPDATE_READY"), []byte("ready"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {}
}

func TestReplaceExecutableWhileTargetIsRunning(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "hand.exe")
	testExe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	oldBytes, err := os.ReadFile(testExe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(execPath, oldBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	readyPath := filepath.Join(dir, "ready")
	child := exec.Command(execPath, "-test.run=^TestWindowsReplacementChildProcess$")
	child.Env = append(os.Environ(), "HAND_SELFUPDATE_CHILD=1", "HAND_SELFUPDATE_READY="+readyPath)
	child.Stdout = io.Discard
	child.Stderr = io.Discard
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if waited {
			return
		}
		if child.ProcessState == nil {
			_ = child.Process.Kill()
		}
		_ = child.Wait()
	})

	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(readyPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not signal readiness")
		}
		time.Sleep(10 * time.Millisecond)
	}

	stagedPath := filepath.Join(dir, ".hand-update-staged.exe")
	if err := os.WriteFile(stagedPath, []byte("new executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceExecutable(execPath, stagedPath); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new executable" {
		t.Fatalf("got %q, want new executable", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	backupCount := 0
	for _, entry := range entries {
		if isUpdaterBackup(entry.Name()) {
			backupCount++
		}
	}
	if backupCount != 1 {
		t.Fatalf("got %d updater backups, want one while child is running", backupCount)
	}

	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	waitErr := child.Wait()
	waited = true
	if waitErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(waitErr, &exitErr) {
			t.Fatal(waitErr)
		}
	}
	cleanupStaleBackups(dir)
	assertNoUpdaterBackups(t, dir)
}

func TestReplaceExecutableRollsBackWhenStagedRenameFails(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "hand.exe")
	stagedPath := filepath.Join(dir, ".hand-update-staged.exe")
	if err := os.WriteFile(execPath, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stagedPath, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}

	originalRename := renameFile
	call := 0
	renameFile = func(oldPath, newPath string) error {
		call++
		if call == 2 {
			return errors.New("install blocked")
		}
		return os.Rename(oldPath, newPath)
	}
	t.Cleanup(func() { renameFile = originalRename })

	err := replaceExecutable(execPath, stagedPath)
	if err == nil || !strings.Contains(err.Error(), "install blocked") {
		t.Fatalf("replaceExecutable error = %v, want install failure", err)
	}
	got, readErr := os.ReadFile(execPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "old" {
		t.Fatalf("got %q, want old after rollback", got)
	}
	if _, err := os.Stat(stagedPath); err != nil {
		t.Fatalf("staged path error = %v, want staged file left for cleanup", err)
	}
	assertNoUpdaterBackups(t, dir)
}

func TestReplaceExecutableLeavesCanonicalWhenFirstRenameFails(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "hand.exe")
	stagedPath := filepath.Join(dir, ".hand-update-staged.exe")
	if err := os.WriteFile(execPath, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stagedPath, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}

	originalRename := renameFile
	renameFile = func(oldPath, newPath string) error {
		return errors.New("backup blocked")
	}
	t.Cleanup(func() { renameFile = originalRename })

	err := replaceExecutable(execPath, stagedPath)
	if err == nil || !strings.Contains(err.Error(), "backup blocked") {
		t.Fatalf("replaceExecutable error = %v, want backup failure", err)
	}
	got, readErr := os.ReadFile(execPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "old" {
		t.Fatalf("got %q, want old after first rename failure", got)
	}
	if _, err := os.Stat(stagedPath); err != nil {
		t.Fatalf("staged path error = %v, want staged file left for cleanup", err)
	}
	assertNoUpdaterBackups(t, dir)
}

func TestReplaceExecutableReportsRollbackFailure(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "hand.exe")
	stagedPath := filepath.Join(dir, ".hand-update-staged.exe")
	if err := os.WriteFile(execPath, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stagedPath, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}

	originalRename := renameFile
	call := 0
	renameFile = func(oldPath, newPath string) error {
		call++
		switch call {
		case 2:
			return errors.New("install blocked")
		case 3:
			return errors.New("rollback blocked")
		default:
			return os.Rename(oldPath, newPath)
		}
	}
	t.Cleanup(func() { renameFile = originalRename })

	err := replaceExecutable(execPath, stagedPath)
	if err == nil {
		t.Fatal("want rollback error")
	}
	for _, want := range []string{
		"install blocked",
		"rollback blocked",
		execPath,
		stagedPath,
		"manual recovery",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestCleanupStaleBackupsOnlyRemovesUpdaterFiles(t *testing.T) {
	dir := t.TempDir()
	owned := filepath.Join(dir, ".hand-update-stale.old.exe")
	unrelated := filepath.Join(dir, "unrelated.old.exe")
	if err := os.WriteFile(owned, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unrelated, []byte("keep"), 0o755); err != nil {
		t.Fatal(err)
	}

	cleanupStaleBackups(dir)
	if _, err := os.Stat(owned); !os.IsNotExist(err) {
		t.Fatalf("owned backup error = %v, want removed", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("unrelated backup error = %v, want retained", err)
	}
}

func assertNoUpdaterBackups(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if isUpdaterBackup(entry.Name()) {
			t.Fatalf("found updater backup %q", entry.Name())
		}
	}
}

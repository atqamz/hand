//go:build windows

package launcher

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/desktopasset"
)

const (
	Marker      = "its description is not Hand's"
	description = "Hand, the supervisor window for your agent fleets"
	script      = `$l=(New-Object -ComObject WScript.Shell).CreateShortcut($env:HAND_LNK);` +
		`$l.TargetPath=$env:HAND_TARGET;$l.WorkingDirectory=$env:HAND_DIR;` +
		`$l.IconLocation=$env:HAND_ICON+',0';$l.Description=$env:HAND_DESC;$l.Save()`
)

var putIcon = put

var writeLnk = func(env []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ps := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	cmd := exec.CommandContext(ctx, ps, "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func entry(getenv func(string) string) (string, error) {
	roaming := getenv("APPDATA")
	if !filepath.IsAbs(roaming) {
		return "", errors.New("launcher: APPDATA is not an absolute path")
	}
	return filepath.Join(roaming, "Microsoft", "Windows", "Start Menu", "Programs", "Hand.lnk"), nil
}

func icoPath(bin string) string { return filepath.Join(filepath.Dir(bin), "hand.ico") }

func managed(lnk string) (s lnkStrings, owned, exists bool) {
	b, err := os.ReadFile(lnk)
	if err != nil {
		return s, false, !errors.Is(err, fs.ErrNotExist)
	}
	s, ok := readLnk(b)
	return s, ok && s.Name == description, true
}

func Sync(getenv func(string) string, bin string) (skipped string, err error) {
	if !filepath.IsAbs(bin) || strings.ContainsAny(bin, "\n\r") {
		return "", fmt.Errorf("launcher: %q is not a usable absolute path", bin)
	}
	lnk, err := entry(getenv)
	if err != nil {
		return "", err
	}
	if _, owned, exists := managed(lnk); exists && !owned {
		return lnk, nil
	}
	ico := icoPath(bin)
	if _, err := putIcon(ico, iconFile()); err != nil {
		return "", fmt.Errorf("launcher: cannot write %s: %w", ico, err)
	}
	switch state, _ := Check(getenv, bin); state {
	case OK:
		return "", nil
	case Foreign:
		return lnk, nil
	}
	if err := os.MkdirAll(filepath.Dir(lnk), 0o755); err != nil {
		return "", fmt.Errorf("launcher: cannot write %s: %w", lnk, err)
	}
	err = writeLnk([]string{"HAND_LNK=" + lnk, "HAND_TARGET=" + bin, "HAND_DIR=" + filepath.Dir(bin), "HAND_ICON=" + ico, "HAND_DESC=" + description})
	if err != nil {
		return "", fmt.Errorf("launcher: cannot write %s: %w", lnk, err)
	}
	return "", nil
}

func Owned(getenv func(string) string) (found []string, foreign string, err error) {
	lnk, err := entry(getenv)
	if err != nil {
		return nil, "", err
	}
	s, owned, exists := managed(lnk)
	switch {
	case !exists:
		return nil, "", nil
	case !owned:
		return nil, lnk, nil
	}
	found = []string{lnk}
	if ico := strings.TrimSuffix(s.Icon, ",0"); filepath.Base(ico) == "hand.ico" && filepath.IsAbs(ico) {
		if _, err := os.Lstat(ico); err == nil {
			found = append(found, ico)
		}
	}
	return found, "", nil
}

func points(s lnkStrings, bin string) bool {
	return strings.EqualFold(s.WorkDir, filepath.Dir(bin)) && strings.EqualFold(strings.TrimSuffix(s.Icon, ",0"), icoPath(bin))
}

func Mine(getenv func(string) string, hand string) bool {
	lnk, err := entry(getenv)
	if err != nil {
		return false
	}
	s, owned, exists := managed(lnk)
	return exists && owned && points(s, desktopasset.Path(hand, "windows", ""))
}

func Remove(getenv func(string) string) (skipped string, err error) {
	found, foreign, err := Owned(getenv)
	if err != nil || foreign != "" {
		return foreign, err
	}
	for _, p := range found {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("launcher: cannot remove %s: %w", p, err)
		}
	}
	return "", nil
}

func Check(getenv func(string) string, bin string) (State, string) {
	lnk, err := entry(getenv)
	if err != nil {
		return Missing, lnk
	}
	s, owned, exists := managed(lnk)
	switch {
	case !exists:
		return Missing, lnk
	case !owned:
		return Foreign, lnk
	}
	ico := icoPath(bin)
	if !points(s, bin) {
		return Elsewhere, lnk
	}
	if _, err := os.Stat(ico); err != nil {
		return Missing, ico
	}
	return OK, lnk
}

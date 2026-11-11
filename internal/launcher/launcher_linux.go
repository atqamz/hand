//go:build linux

package launcher

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/brand"
	"github.com/atqamz/hand/internal/desktopasset"
)

const marker = "X-Hand-Managed=true\n"

func dataHome(getenv func(string) string) (string, error) {
	if d := getenv("XDG_DATA_HOME"); filepath.IsAbs(d) {
		return d, nil
	}
	h := getenv("HOME")
	if !filepath.IsAbs(h) {
		return "", errors.New("launcher: neither XDG_DATA_HOME nor HOME is an absolute path")
	}
	return filepath.Join(h, ".local", "share"), nil
}

func paths(getenv func(string) string) (apps, entry, pic string, err error) {
	data, err := dataHome(getenv)
	if err != nil {
		return "", "", "", err
	}
	apps = filepath.Join(data, "applications")
	return apps, filepath.Join(apps, desktopasset.Name+".desktop"), filepath.Join(data, "icons", "hicolor", "512x512", "apps", desktopasset.Name+".png"), nil
}

func quote(path string) string {
	if !strings.ContainsAny(path, " \t\n\"'\\><~|&;$*?#()`%") {
		return path
	}
	r := strings.NewReplacer(`"`, `\"`, "`", "\\`", `$`, `\$`, `\`, `\\`)
	return `"` + strings.ReplaceAll(r.Replace(path), "%", "%%") + `"`
}

func escape(s string) string { return strings.ReplaceAll(s, `\`, `\\`) }

func content(bin string) []byte {
	return []byte("[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=Hand\n" +
		"Comment=Supervisor window for your agent fleets\n" +
		"Exec=" + escape(quote(bin)) + "\n" +
		"TryExec=" + escape(bin) + "\n" +
		"Icon=" + desktopasset.Name + "\n" +
		marker +
		"Terminal=false\n" +
		"Categories=Development;\n" +
		"StartupWMClass=" + desktopasset.Name + "\n")
}

func put(path string, want []byte) (bool, error) {
	if have, err := os.ReadFile(path); err == nil && bytes.Equal(have, want) {
		return false, nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	f, err := os.CreateTemp(dir, ".hand-desktop.*")
	if err != nil {
		return false, err
	}
	_, err = f.Write(want)
	if err == nil {
		err = f.Chmod(0o644)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return false, err
	}
	return true, nil
}

func refresh(apps string) {
	if tool, err := exec.LookPath("update-desktop-database"); err == nil {
		cmd := exec.Command(tool, apps)
		_ = cmd.Start()
		done := time.AfterFunc(10*time.Second, func() { _ = cmd.Process.Kill() })
		_ = cmd.Wait()
		done.Stop()
	}
}

func managed(entry string) (owned, exists bool) {
	b, err := os.ReadFile(entry)
	return err == nil && bytes.Contains(b, []byte("\n"+marker)), err == nil
}

func Sync(getenv func(string) string, bin string) (skipped string, err error) {
	if !filepath.IsAbs(bin) || strings.ContainsAny(bin, "\n\r") {
		return "", fmt.Errorf("launcher: %q is not a usable absolute path", bin)
	}
	apps, entry, pic, err := paths(getenv)
	if err != nil {
		return "", err
	}
	if owned, exists := managed(entry); exists && !owned {
		return entry, nil
	}
	if _, err := put(pic, brand.Icon512); err != nil {
		return "", fmt.Errorf("launcher: cannot write %s: %w", pic, err)
	}
	changed, err := put(entry, content(bin))
	if err != nil {
		return "", fmt.Errorf("launcher: cannot write %s: %w", entry, err)
	}
	if changed {
		refresh(apps)
	}
	return "", nil
}

func Remove(getenv func(string) string) (skipped string, err error) {
	apps, entry, pic, err := paths(getenv)
	if err != nil {
		return "", err
	}
	owned, exists := managed(entry)
	if !exists {
		return "", nil
	}
	if !owned {
		return entry, nil
	}
	for _, p := range []string{entry, pic} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("launcher: cannot remove %s: %w", p, err)
		}
	}
	refresh(apps)
	return "", nil
}

func Check(getenv func(string) string, bin string) (State, string) {
	_, entry, pic, err := paths(getenv)
	if err != nil {
		return Missing, entry
	}
	owned, exists := managed(entry)
	switch {
	case !exists:
		return Missing, entry
	case !owned:
		return Foreign, entry
	}
	have, _ := os.ReadFile(entry)
	if !bytes.Equal(have, content(bin)) {
		return Elsewhere, entry
	}
	if _, err := os.Stat(pic); err != nil {
		return Missing, pic
	}
	return OK, entry
}

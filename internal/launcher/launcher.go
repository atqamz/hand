package launcher

import (
	"bytes"
	_ "embed"
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

//go:embed hand.png
var icon []byte

type State string

const (
	OK        State = "ok"
	Missing   State = "missing"
	Elsewhere State = "elsewhere"
)

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
	return apps, filepath.Join(apps, desktopasset.Name+".desktop"), filepath.Join(data, "icons", "hicolor", "512x512", "apps", "hand.png"), nil
}

func quote(path string) string {
	if !strings.ContainsAny(path, " \t\n\"'\\><~|&;$*?#()`%") {
		return path
	}
	r := strings.NewReplacer(`"`, `\"`, "`", "\\`", `$`, `\$`, `\`, `\\`)
	return `"` + strings.ReplaceAll(r.Replace(path), "%", "%%") + `"`
}

func content(bin string) []byte {
	return []byte("[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=Hand\n" +
		"Comment=Supervisor window for your agent fleets\n" +
		"Exec=" + strings.ReplaceAll(quote(bin), `\`, `\\`) + "\n" +
		"TryExec=" + bin + "\n" +
		"Icon=hand\n" +
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

func Sync(getenv func(string) string, bin string) error {
	if !filepath.IsAbs(bin) {
		return fmt.Errorf("launcher: %q is not an absolute path", bin)
	}
	apps, entry, pic, err := paths(getenv)
	if err != nil {
		return err
	}
	if _, err := put(pic, icon); err != nil {
		return fmt.Errorf("launcher: cannot write %s: %w", pic, err)
	}
	changed, err := put(entry, content(bin))
	if err != nil {
		return fmt.Errorf("launcher: cannot write %s: %w", entry, err)
	}
	if changed {
		refresh(apps)
	}
	return nil
}

func Remove(getenv func(string) string) error {
	apps, entry, pic, err := paths(getenv)
	if err != nil {
		return err
	}
	removed := false
	for _, p := range []string{entry, pic} {
		switch err := os.Remove(p); {
		case err == nil:
			removed = true
		case !errors.Is(err, fs.ErrNotExist):
			return fmt.Errorf("launcher: cannot remove %s: %w", p, err)
		}
	}
	if removed {
		refresh(apps)
	}
	return nil
}

func Check(getenv func(string) string, bin string) (State, string) {
	_, entry, pic, err := paths(getenv)
	if err != nil {
		return Missing, entry
	}
	have, err := os.ReadFile(entry)
	if err != nil {
		return Missing, entry
	}
	if !bytes.Equal(have, content(bin)) {
		return Elsewhere, entry
	}
	if _, err := os.Stat(pic); err != nil {
		return Missing, pic
	}
	return OK, entry
}

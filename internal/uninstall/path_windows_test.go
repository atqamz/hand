//go:build windows

package uninstall

import (
	"crypto/rand"
	"errors"
	"testing"

	"golang.org/x/sys/windows/registry"
)

func TestUserPathEditsOnlyTheListedFolder(t *testing.T) {
	old := envKey
	envKey = `Software\HandUninstallTest-` + rand.Text()
	t.Cleanup(func() {
		envKey = old
		_ = registry.DeleteKey(registry.CURRENT_USER, `Software\`+envKey[len(`Software\`):])
	})
	k, _, err := registry.CreateKey(registry.CURRENT_USER, envKey, registry.ALL_ACCESS)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	dir := `C:\hand-test\bin`
	if found, err := userPath(dir, true); found || err != nil {
		t.Fatalf("userPath with no Path value = %v, %v", found, err)
	}
	if err := k.SetExpandStringValue("Path", `C:\a;`+dir+`;%SystemRoot%\b`); err != nil {
		t.Fatal(err)
	}
	if found, err := userPath(dir, false); !found || err != nil {
		t.Fatalf("userPath probe = %v, %v", found, err)
	}
	if got, _, _ := k.GetStringValue("Path"); got != `C:\a;`+dir+`;%SystemRoot%\b` {
		t.Fatalf("the probe changed Path to %q", got)
	}
	if found, err := userPath(dir, true); !found || err != nil {
		t.Fatalf("userPath = %v, %v", found, err)
	}
	got, kind, _ := k.GetStringValue("Path")
	if got != `C:\a;%SystemRoot%\b` || kind != registry.EXPAND_SZ {
		t.Fatalf("Path = %q (type %d)", got, kind)
	}
	if err := k.SetStringValue("Path", dir); err != nil {
		t.Fatal(err)
	}
	if found, err := userPath(dir, true); !found || err != nil {
		t.Fatalf("userPath = %v, %v", found, err)
	}
	if _, _, err := k.GetStringValue("Path"); !errors.Is(err, registry.ErrNotExist) {
		t.Fatalf("an emptied Path was kept: %v", err)
	}
}

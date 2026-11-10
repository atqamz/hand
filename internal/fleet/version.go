package fleet

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

const VersionFile = "hand.version"

type VersionPin struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Schema  int    `json:"schema"`
}

func ReadVersion(home string) (VersionPin, bool, error) {
	b, err := os.ReadFile(filepath.Join(home, VersionFile))
	if errors.Is(err, fs.ErrNotExist) {
		return VersionPin{}, false, nil
	}
	if err != nil {
		return VersionPin{}, false, err
	}
	var p VersionPin
	if json.Unmarshal(b, &p) != nil || p.Version == "" {
		return VersionPin{}, false, nil
	}
	return p, true, nil
}

func WriteVersion(home string, p VersionPin) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	tmp := filepath.Join(home, "."+VersionFile+"."+rand.Text())
	if err := writeTemp(tmp, append(b, '\n')); err != nil {
		return err
	}
	return renameSynced(tmp, filepath.Join(home, VersionFile))
}

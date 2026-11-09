package fleet

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/atqamz/hand/internal/state"
)

const HomeFile = "fleet.home"

type HomesRecord struct {
	Homes   []string `json:"homes"`
	Current string   `json:"current"`
}

func ReadHomes(home string) (HomesRecord, error) {
	path := filepath.Join(home, HomeFile)
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return HomesRecord{}, nil
	}
	if err != nil {
		return HomesRecord{}, err
	}
	var rec HomesRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return HomesRecord{}, fmt.Errorf("%w: %s is not a fleet home record: %v", state.ErrInvalid, path, err)
	}
	return rec, nil
}

func WriteHomes(home, current string) error {
	if current == "" {
		current = home
	}
	rec, err := ReadHomes(home)
	if err != nil {
		return err
	}
	if rec.Current == current && slices.Contains(rec.Homes, current) {
		return nil
	}
	homes := make([]string, 0, len(rec.Homes)+2)
	add := func(dir string) {
		if dir != "" && !slices.Contains(homes, dir) {
			homes = append(homes, dir)
		}
	}
	for _, dir := range rec.Homes {
		add(dir)
	}
	add(rec.Current)
	add(current)
	rec.Homes, rec.Current = homes, current
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	tmp := filepath.Join(home, "."+HomeFile+"."+rand.Text())
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, filepath.Join(home, HomeFile)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

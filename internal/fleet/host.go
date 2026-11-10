package fleet

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/boot"
)

const (
	HostFile  = "fleet.host"
	HostStale = 90 * time.Second
)

type HostMarker struct {
	Host string    `json:"host"`
	Boot string    `json:"boot"`
	PID  int       `json:"pid"`
	At   time.Time `json:"at"`
}

type HostVerdict int

const (
	HostOurs HostVerdict = iota
	HostRefuse
	HostTakeOver
)

func ReadHost(home string) (HostMarker, bool, error) {
	b, err := os.ReadFile(filepath.Join(home, HostFile))
	if errors.Is(err, fs.ErrNotExist) {
		return HostMarker{}, false, nil
	}
	if err != nil {
		return HostMarker{}, false, err
	}
	var m HostMarker
	if json.Unmarshal(b, &m) != nil || m.Host == "" {
		return HostMarker{}, false, nil
	}
	return m, true, nil
}

func WriteHost(home string, m HostMarker) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := filepath.Join(home, "."+HostFile+"."+rand.Text())
	if err := writeTemp(tmp, append(b, '\n')); err != nil {
		return err
	}
	return renameSynced(tmp, filepath.Join(home, HostFile))
}

func RemoveHost(home string) error {
	if err := os.Remove(filepath.Join(home, HostFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (m HostMarker) SameMachine(self HostMarker) bool {
	if m.Boot != "" && m.Boot == self.Boot {
		return true
	}
	return strings.EqualFold(m.Host, self.Host) && boot.Same(m.Boot, self.Boot)
}

func (m HostMarker) Age(now time.Time) time.Duration { return now.Sub(m.At) }

func JudgeHost(m HostMarker, found bool, self HostMarker, now time.Time, held func() (bool, error)) HostVerdict {
	switch {
	case !found, m.SameMachine(self):
		return HostOurs
	case strings.EqualFold(m.Host, self.Host):
		if up, err := held(); up || err != nil {
			return HostOurs
		}
		return HostTakeOver
	case m.Age(now) <= HostStale:
		return HostRefuse
	}
	return HostTakeOver
}

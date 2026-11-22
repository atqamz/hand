package remote

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sync"
	"time"
)

const (
	maxFile = 1 << 20
	keyFile = "hand-iroh.key"
)

var (
	ErrFleetHeld   = errors.New("a fleet with this ID is already held")
	ErrPermissions = errors.New("a remote state file can be read by others")
	ErrUnknown     = errors.New("no such remote host or fleet")
	ErrBad         = errors.New("a remote host, fleet or cookie is malformed")

	cookieRe = regexp.MustCompile(`^[1-9][0-9]*\.[0-9a-f]{64}$`)
)

type Fleet struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Host struct {
	ID       string    `json:"id"`
	Relay    string    `json:"relay,omitempty"`
	Label    string    `json:"label,omitempty"`
	PairedAt time.Time `json:"paired_at"`
	Fleets   []Fleet   `json:"fleets"`
}

type Store struct {
	dir string
	mu  sync.Mutex
}

func Dir(drafts string) string {
	if drafts == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(filepath.Clean(drafts)), "remote")
}

func OpenStore(dir string) *Store { return &Store{dir: dir} }

func (s *Store) hosts() ([]Host, error) {
	var hs []Host
	return hs, s.read(filepath.Join(s.dir, "hosts.json"), &hs)
}

func (s *Store) cookies(strict bool) (map[string]map[string]string, error) {
	path := filepath.Join(s.dir, "cookies.json")
	if strict && runtime.GOOS != "windows" {
		if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("%w: %s is %#o, it must be 0600", ErrPermissions, path, fi.Mode().Perm())
		}
	}
	var c map[string]map[string]string
	return c, s.read(path, &c)
}

func (s *Store) read(path string, into any) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxFile+1))
	if err != nil {
		return err
	}
	if len(raw) > maxFile {
		return fmt.Errorf("%s is over %d bytes", path, maxFile)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("%s is not valid: %w", path, err)
	}
	return nil
}

func (s *Store) write(name string, v any, mode os.FileMode) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".remote-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	_, err = tmp.Write(raw)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(s.dir, name))
}

func (s *Store) Hosts() ([]Host, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hosts()
}

func (s *Store) Cookie(host, fleet string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.cookies(true)
	if err != nil {
		return "", err
	}
	v := c[host][fleet]
	if v != "" && !cookieRe.MatchString(v) {
		return "", ErrBad
	}
	return v, nil
}

func (s *Store) Add(h Host, f Fleet, cookie string, local func(id string) bool) error {
	if !endpointRe.MatchString(h.ID) || !fleetRe.MatchString(f.ID) || !cookieRe.MatchString(cookie) {
		return ErrBad
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	hosts, err := s.hosts()
	if err != nil {
		return err
	}
	at := -1
	for i, o := range hosts {
		if o.ID == h.ID {
			at = i
		}
		if slices.ContainsFunc(o.Fleets, func(x Fleet) bool { return x.ID == f.ID }) {
			return ErrFleetHeld
		}
	}
	if local != nil && local(f.ID) {
		return ErrFleetHeld
	}
	if at < 0 {
		h.Fleets = nil
		hosts, at = append(hosts, h), len(hosts)
	}
	hosts[at].Fleets = append(hosts[at].Fleets, f)
	jar, err := s.cookies(true)
	if err != nil {
		return err
	}
	if jar == nil {
		jar = map[string]map[string]string{}
	}
	if jar[h.ID] == nil {
		jar[h.ID] = map[string]string{}
	}
	jar[h.ID][f.ID] = cookie
	if err := s.write("cookies.json", jar, 0o600); err != nil {
		return err
	}
	return s.write("hosts.json", hosts, 0o600)
}

func (s *Store) Remove(host, fleet string) error {
	if !endpointRe.MatchString(host) {
		return ErrBad
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	hosts, err := s.hosts()
	if err != nil {
		return err
	}
	at := slices.IndexFunc(hosts, func(h Host) bool { return h.ID == host })
	if at < 0 || !slices.ContainsFunc(hosts[at].Fleets, func(f Fleet) bool { return f.ID == fleet }) {
		return ErrUnknown
	}
	hosts[at].Fleets = slices.DeleteFunc(hosts[at].Fleets, func(f Fleet) bool { return f.ID == fleet })
	if len(hosts[at].Fleets) == 0 {
		return s.forget(hosts, host)
	}
	jar, err := s.cookies(false)
	if err != nil {
		return err
	}
	delete(jar[host], fleet)
	if err := s.write("cookies.json", jar, 0o600); err != nil {
		return err
	}
	return s.write("hosts.json", hosts, 0o600)
}

func (s *Store) Forget(host string) error {
	if !endpointRe.MatchString(host) {
		return ErrBad
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	hosts, err := s.hosts()
	if err != nil {
		return err
	}
	return s.forget(hosts, host)
}

func (s *Store) forget(hosts []Host, id string) error {
	jar, err := s.cookies(false)
	if err != nil {
		return err
	}
	delete(jar, id)
	if err := s.write("cookies.json", jar, 0o600); err != nil {
		return err
	}
	hosts = slices.DeleteFunc(hosts, func(h Host) bool { return h.ID == id })
	if err := s.write("hosts.json", hosts, 0o600); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(s.dir, id))
}

func (s *Store) KeyDir(host string) (string, error) {
	if !endpointRe.MatchString(host) {
		return "", ErrBad
	}
	dir := filepath.Join(s.dir, host)
	return dir, os.MkdirAll(dir, 0o700)
}

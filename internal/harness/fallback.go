package harness

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

var (
	npmTimeout  = 3 * time.Second
	npmPrefixes sync.Map
)

func Find(name string, env Env) (string, error) {
	if env.Finds == nil {
		return search(name, env)
	}
	return env.Finds.find(name, env)
}

func search(name string, env Env) (string, error) {
	bin, err := LookPath(name, env.Path)
	if err == nil {
		return bin, nil
	}
	for _, dir := range fallbackDirs(name, env) {
		if found, lookErr := LookPath(name, dir); lookErr == nil {
			return found, nil
		}
	}
	return "", err
}

func fallbackDirs(name string, env Env) []string {
	var dirs []string
	if dir := miseShimsDir(env.get, runtime.GOOS); dir != "" {
		dirs = append(dirs, dir)
	}
	if name != "copilot" {
		return dirs
	}
	prefix := env.get("NPM_CONFIG_PREFIX")
	if prefix == "" {
		prefix = env.get("npm_config_prefix")
	}
	if prefix == "" {
		prefix = npmPrefix(env)
	}
	if dir := npmBinDir(prefix, runtime.GOOS); dir != "" {
		dirs = append(dirs, dir)
	}
	if dir := ghCopilotDir(env.get, runtime.GOOS); dir != "" {
		dirs = append(dirs, dir)
	}
	return dirs
}

func npmBinDir(prefix, goos string) string {
	switch {
	case !filepath.IsAbs(prefix):
		return ""
	case goos == "windows":
		return prefix
	}
	return filepath.Join(prefix, "bin")
}

func miseShimsDir(get func(string) string, goos string) string {
	data := get("MISE_DATA_DIR")
	if !filepath.IsAbs(data) {
		switch {
		case get("XDG_DATA_HOME") != "":
			data = filepath.Join(get("XDG_DATA_HOME"), "mise")
		case goos == "windows":
			local := get("LOCALAPPDATA")
			if local == "" {
				return ""
			}
			data = filepath.Join(local, "mise")
		default:
			home := get("HOME")
			if home == "" {
				return ""
			}
			data = filepath.Join(home, ".local", "share", "mise")
		}
	}
	return filepath.Join(data, "shims")
}

func ghCopilotDir(get func(string) string, goos string) string {
	home := get("HOME")
	if goos == "windows" {
		home = get("USERPROFILE")
	}
	var data string
	switch local := get("LocalAppData"); {
	case get("XDG_DATA_HOME") != "":
		data = filepath.Join(get("XDG_DATA_HOME"), "gh")
	case goos == "windows" && local != "":
		data = filepath.Join(local, "GitHub CLI")
	case home != "":
		data = filepath.Join(home, ".local", "share", "gh")
	default:
		return ""
	}
	return filepath.Join(data, "copilot")
}

func npmPrefix(env Env) string {
	bin, err := LookPath("npm", env.Path)
	var batch *BatchFileError
	if errors.As(err, &batch) {
		bin = batch.Path
	} else if err != nil {
		return ""
	}
	key := bin + "\x00" + env.get("HOME")
	if v, ok := npmPrefixes.Load(key); ok {
		return v.(string)
	}
	var environ []string
	for _, k := range baseEnv {
		v := env.get(k)
		if k == "PATH" {
			v = env.Path
		}
		if v != "" {
			environ = append(environ, k+"="+v)
		}
	}
	out, _, err := runner{bin: bin, environ: environ, dir: env.get("HOME")}.run(context.Background(), npmTimeout, "", "prefix", "-g")
	prefix := ""
	if err == nil {
		prefix, _, _ = strings.Cut(strings.TrimSpace(string(out)), "\n")
	}
	npmPrefixes.Store(key, prefix)
	return prefix
}

type FindCache struct {
	ttl    time.Duration
	now    func() time.Time
	search func(string, Env) (string, error)
	mu     sync.Mutex
	hit    map[findKey]foundBin
}

type findKey struct{ name, path string }

type foundBin struct {
	bin string
	err error
	at  time.Time
}

func NewFindCache(ttl time.Duration, now func() time.Time) *FindCache {
	if now == nil {
		now = time.Now
	}
	return &FindCache{ttl: ttl, now: now, search: search, hit: map[findKey]foundBin{}}
}

func (c *FindCache) find(name string, env Env) (string, error) {
	key := findKey{name, env.Path}
	c.mu.Lock()
	e, ok := c.hit[key]
	c.mu.Unlock()
	if ok && c.now().Sub(e.at) < c.ttl {
		return e.bin, e.err
	}
	start := c.now()
	bin, err := c.search(name, env)
	c.mu.Lock()
	if cur, ok := c.hit[key]; !ok || !cur.at.After(start) {
		c.hit[key] = foundBin{bin: bin, err: err, at: start}
	}
	c.mu.Unlock()
	return bin, err
}

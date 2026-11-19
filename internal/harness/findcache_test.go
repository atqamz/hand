package harness

import (
	"errors"
	"os"
	"sync"
	"testing"
	"time"
)

func TestACachedFindSkipsTheSearchUntilTheTTLEnds(t *testing.T) {
	t.Setenv("PATHEXT", ".EXE")
	dir := t.TempDir()
	now := time.Unix(1000, 0)
	env := Env{Path: dir, Finds: NewFindCache(5*time.Second, func() time.Time { return now })}

	if _, err := Find("tool", env); err == nil {
		t.Fatal("found a tool that is not there")
	}
	bin := exeFile(t, dir, "tool")
	if _, err := Find("tool", env); err == nil {
		t.Fatal("the miss was searched again inside the TTL")
	}
	now = now.Add(6 * time.Second)
	got, err := Find("tool", env)
	if err != nil || got != bin {
		t.Fatalf("after the TTL Find = %q, %v; want %q", got, err, bin)
	}
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	if got, err := Find("tool", env); err != nil || got != bin {
		t.Fatalf("the hit was searched again inside the TTL: %q, %v", got, err)
	}
}

func TestFindWithoutACacheSearchesEveryTime(t *testing.T) {
	t.Setenv("PATHEXT", ".EXE")
	dir := t.TempDir()
	env := Env{Path: dir}
	if _, err := Find("tool", env); err == nil {
		t.Fatal("found a tool that is not there")
	}
	exeFile(t, dir, "tool")
	if _, err := Find("tool", env); err != nil {
		t.Fatalf("an uncached Find missed a file that exists: %v", err)
	}
}

func TestACachedFindKeepsEveryNameApart(t *testing.T) {
	t.Setenv("PATHEXT", ".EXE")
	dir := t.TempDir()
	bin := exeFile(t, dir, "one")
	env := Env{Path: dir, Finds: NewFindCache(time.Minute, nil)}
	if got, err := Find("one", env); err != nil || got != bin {
		t.Fatalf("Find(one) = %q, %v", got, err)
	}
	if got, err := Find("two", env); err == nil {
		t.Fatalf("Find(two) answered with the path of one: %q", got)
	}
}

func TestACachedFindKeepsEveryPathApart(t *testing.T) {
	t.Setenv("PATHEXT", ".EXE")
	first, second := t.TempDir(), t.TempDir()
	one, two := exeFile(t, first, "tool"), exeFile(t, second, "tool")
	finds := NewFindCache(time.Minute, nil)
	if got, err := Find("tool", Env{Path: first, Finds: finds}); err != nil || got != one {
		t.Fatalf("first PATH: %q, %v", got, err)
	}
	if got, err := Find("tool", Env{Path: second, Finds: finds}); err != nil || got != two {
		t.Fatalf("second PATH answered with the first's path: %q, %v", got, err)
	}
}

func TestASlowerOlderSearchDoesNotReplaceANewerResult(t *testing.T) {
	now := time.Unix(1000, 0)
	var mu sync.Mutex
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	release, started := make(chan struct{}), make(chan struct{})
	calls := 0
	finds := NewFindCache(time.Minute, clock)
	finds.search = func(string, Env) (string, error) {
		calls++
		if calls == 1 {
			close(started)
			<-release
			return "", errors.New("missed")
		}
		return "/bin/tool", nil
	}
	env := Env{Path: "p", Finds: finds}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = Find("tool", env)
	}()
	<-started
	mu.Lock()
	now = now.Add(time.Second)
	mu.Unlock()
	if got, err := Find("tool", env); err != nil || got != "/bin/tool" {
		t.Fatalf("newer search = %q, %v", got, err)
	}
	close(release)
	<-done
	if got, err := Find("tool", env); err != nil || got != "/bin/tool" {
		t.Fatalf("the older miss replaced the newer hit: %q, %v", got, err)
	}
}

func TestACacheEntryExpiresFromWhenItsSearchBegan(t *testing.T) {
	now := time.Unix(1000, 0)
	finds := NewFindCache(5*time.Second, func() time.Time { return now })
	finds.search = func(string, Env) (string, error) {
		now = now.Add(4 * time.Second)
		return "", errors.New("missed")
	}
	env := Env{Path: "p", Finds: finds}
	_, _ = Find("tool", env)
	finds.search = func(string, Env) (string, error) { return "/bin/tool", nil }
	now = now.Add(2 * time.Second)
	if got, err := Find("tool", env); err != nil || got != "/bin/tool" {
		t.Fatalf("a miss stayed cached past the TTL of its start: %q, %v", got, err)
	}
}

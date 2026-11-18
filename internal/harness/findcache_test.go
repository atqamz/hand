package harness

import (
	"os"
	"testing"
	"time"
)

func TestACachedFindSkipsTheSearchUntilTheTTLEnds(t *testing.T) {
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

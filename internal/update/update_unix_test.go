//go:build unix

package update

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestRunKeepsItsChildrenOutOfTheTerminalGroup(t *testing.T) {
	f := newRun(t, nil)
	pgid := filepath.Join(t.TempDir(), "pgid")
	script := "#!/bin/sh\nif [ \"$1\" = version ]; then printf 'version: 0.9.0\\nchannel: edge\\ncommit: 0123456789ab\\nschema: 7\\nluvus: 0.14.4\\n'; exit 0; fi\nps -o pgid= -p $$ | tr -d ' ' >> " + pgid + "\n"
	archive := tarball(t, "hand", script)
	f.srv.set("hand-linux-amd64.tar.gz", archive)
	f.srv.set("checksums.txt", []byte(sum(archive)+"  hand-linux-amd64.tar.gz\n"))
	if _, err := Run(context.Background(), f.o); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(pgid)
	if err != nil {
		t.Fatal(err)
	}
	own := strconv.Itoa(syscall.Getpgrp())
	for line := range strings.Lines(string(b)) {
		if strings.TrimSpace(line) == own {
			t.Fatalf("a child ran in the update's process group %s", own)
		}
	}
}

func TestRunUpgradesAFleetLinkedBySymlink(t *testing.T) {
	f := newRun(t, nil)
	path := filepath.Join(f.root, "fleets", f.alphaID)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(f.alpha, path); err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Failed {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if got := byName(rep.Fleets); got[0] != (FleetResult{"alpha", "ok", "kept"}) {
		t.Fatalf("fleets = %+v", got)
	}
	if got := f.log(t, "hand init"); len(got) != 1 {
		t.Fatalf("init calls = %q", got)
	}
}

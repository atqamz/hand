package store

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	legacyV18CutoverProcessRoleEnv = "HAND_CUTOVER_PROCESS_ROLE"
	legacyV18CutoverProcessHomeEnv = "HAND_CUTOVER_PROCESS_HOME"
)

func TestLegacyV18CutoverFreshSourceReadDropsIndependentWriterExclusion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("closing an unrelated descriptor does not release Windows LockFileEx locks")
	}
	if os.Getenv(legacyV18CutoverProcessRoleEnv) == "commit-writer" {
		runLegacyV18CutoverCommitWriter(t)
		return
	}

	home := createLegacyV18CutoverTestSource(t)
	setLegacyV18CutoverTestJournalMode(t, home, "DELETE")
	readDB, err := openLegacyV18CutoverSQLite(Path(home), "ro", 5*time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = readDB.Close() }()
	readTx, err := readDB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = readTx.Rollback() }()
	var objects int
	if err := readTx.QueryRow(`SELECT COUNT(*) FROM sqlite_schema`).Scan(&objects); err != nil {
		t.Fatal(err)
	}

	child := exec.Command(os.Args[0], "-test.run=^TestLegacyV18CutoverFreshSourceReadDropsIndependentWriterExclusion$")
	child.Env = append(os.Environ(),
		legacyV18CutoverProcessRoleEnv+"=commit-writer",
		legacyV18CutoverProcessHomeEnv+"="+home,
	)
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	child.Stderr = &stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { killAndWait(child) })
	scanner := bufio.NewScanner(stdout)
	wantLegacyV18CutoverProcessLine(t, scanner, "prepared", &stderr)
	if _, err := legacyV18CutoverFileSHA256(Path(home)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(stdin, "commit\n"); err != nil {
		t.Fatal(err)
	}
	wantLegacyV18CutoverProcessLine(t, scanner, "committed", &stderr)
	if err := child.Wait(); err != nil {
		t.Fatalf("writer process: %v; stderr=%s", err, stderr.String())
	}
}

func TestLegacyV18CutoverSourceReadPreservesIndependentWriterExclusion(t *testing.T) {
	if os.Getenv(legacyV18CutoverProcessRoleEnv) == "commit-writer" {
		runLegacyV18CutoverCommitWriter(t)
		return
	}

	home := createLegacyV18CutoverTestSource(t)
	setLegacyV18CutoverTestJournalMode(t, home, "DELETE")
	source, err := openLegacyV18CutoverPinnedSource(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = source.Close() }()
	sourceDigest, err := source.sha256()
	if err != nil {
		t.Fatal(err)
	}
	readDB, err := openLegacyV18CutoverSQLite(Path(home), "ro", 5*time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = readDB.Close() }()
	readTx, err := readDB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = readTx.Rollback() }()
	var objects int
	if err := readTx.QueryRow(`SELECT COUNT(*) FROM sqlite_schema`).Scan(&objects); err != nil {
		t.Fatal(err)
	}

	child := exec.Command(os.Args[0], "-test.run=^TestLegacyV18CutoverSourceReadPreservesIndependentWriterExclusion$")
	child.Env = append(os.Environ(),
		legacyV18CutoverProcessRoleEnv+"=commit-writer",
		legacyV18CutoverProcessHomeEnv+"="+home,
	)
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	child.Stderr = &stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { killAndWait(child) })
	scanner := bufio.NewScanner(stdout)
	wantLegacyV18CutoverProcessLine(t, scanner, "prepared", &stderr)

	protectedReads := []struct {
		name string
		run  func() error
	}{
		{name: "initial SHARED hash", run: func() error { _, err := source.sha256(); return err }},
		{name: "reader-barrier hash", run: func() error { _, err := source.sha256(); return err }},
		{name: "archive candidate copy", run: func() error {
			return writeLegacyV18CutoverArchiveCandidate(source, filepath.Join(home, "state", "pinned-copy.candidate"), sourceDigest)
		}},
		{name: "post-archive barrier hash", run: func() error { _, err := source.sha256(); return err }},
		{name: "EXCLUSIVE revalidation hash", run: func() error { _, err := source.sha256(); return err }},
		{name: "pre-freeze hash", run: func() error { _, err := source.sha256(); return err }},
	}
	for _, read := range protectedReads {
		if err := read.run(); err != nil {
			t.Fatalf("%s: %v", read.name, err)
		}
	}
	if _, err := io.WriteString(stdin, "commit\n"); err != nil {
		t.Fatal(err)
	}
	wantLegacyV18CutoverProcessLine(t, scanner, "busy", &stderr)

	if err := readTx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(stdin, "retry\n"); err != nil {
		t.Fatal(err)
	}
	wantLegacyV18CutoverProcessLine(t, scanner, "committed", &stderr)
	if err := child.Wait(); err != nil {
		t.Fatalf("writer process: %v; stderr=%s", err, stderr.String())
	}
}

func TestLegacyV18CutoverGateAndFreezeExcludeIndependentWriters(t *testing.T) {
	if os.Getenv(legacyV18CutoverProcessRoleEnv) == "write-once" {
		runLegacyV18CutoverWriteOnce(t)
		return
	}

	t.Run("exclusive gate blocks until deliberate release", func(t *testing.T) {
		home := createLegacyV18CutoverTestSource(t)
		setLegacyV18CutoverTestJournalMode(t, home, "DELETE")
		gate, err := acquireLegacyV18CutoverGate(context.Background(), home)
		if err != nil {
			t.Fatal(err)
		}
		if got := runLegacyV18CutoverWriteOnceProcess(t, home); got != "busy" {
			_ = gate.Close()
			t.Fatalf("independent writer while gate held = %q, want busy", got)
		}
		if err := gate.Close(); err != nil {
			t.Fatal(err)
		}
		if got := runLegacyV18CutoverWriteOnceProcess(t, home); got != "committed" {
			t.Fatalf("independent writer after deliberate gate release = %q, want committed", got)
		}
	})

	t.Run("committed freeze rejects later writer", func(t *testing.T) {
		home := createLegacyV18CutoverTestSource(t)
		setLegacyV18CutoverTestJournalMode(t, home, "DELETE")
		gate, err := acquireLegacyV18CutoverGate(context.Background(), home)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = gate.Close() }()
		if got := runLegacyV18CutoverWriteOnceProcess(t, home); got != "busy" {
			t.Fatalf("independent writer before freeze = %q, want busy", got)
		}
		archive, err := promoteLegacyV18CutoverArchiveCandidate(home, gate.archiveCandidate)
		if err != nil {
			t.Fatal(err)
		}
		bridge, err := freezeLegacyV18CutoverSource(context.Background(), home, gate, archive)
		if err != nil {
			t.Fatal(err)
		}
		if !bridge.Committed || gate.source != nil || gate.readConn != nil || gate.probeConn != nil || gate.conn != nil || gate.readDB != nil || gate.probeDB != nil || gate.db != nil {
			t.Fatalf("committed freeze cleanup retained a gate resource: bridge committed=%t gate=%+v", bridge.Committed, gate)
		}
		if got := runLegacyV18CutoverWriteOnceProcess(t, home); got != "frozen" {
			t.Fatalf("independent writer after committed freeze = %q, want frozen", got)
		}
	})

	t.Run("archive alias refusal preserves exclusive gate", func(t *testing.T) {
		home := createLegacyV18CutoverTestSource(t)
		setLegacyV18CutoverTestJournalMode(t, home, "DELETE")
		gate, err := acquireLegacyV18CutoverGate(context.Background(), home)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = gate.Close() }()
		archive, err := promoteLegacyV18CutoverArchiveCandidate(home, gate.archiveCandidate)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(archive.Path); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(Path(home), archive.Path); err != nil {
			t.Skipf("platform cannot create hard-link alias: %v", err)
		}
		if _, err := freezeLegacyV18CutoverSource(context.Background(), home, gate, archive); err == nil {
			t.Fatal("freeze accepted an original archive aliased to the active source")
		}
		if err := os.Remove(archive.Path); err != nil {
			t.Fatal(err)
		}
		if got := runLegacyV18CutoverWriteOnceProcess(t, home); got != "busy" {
			t.Fatalf("independent writer after archive alias refusal = %q, want busy", got)
		}
	})
}

func TestLegacyV18CutoverManifestAliasRefusalPreservesIndependentWriterExclusion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("closing an unrelated descriptor does not release Windows LockFileEx locks")
	}

	for _, phase := range []string{"archive validation", "manifest read", "manifest reuse", "manifest candidate"} {
		t.Run(phase, func(t *testing.T) {
			home := createLegacyV18CutoverTestSource(t)
			setLegacyV18CutoverTestJournalMode(t, home, "DELETE")
			gate, err := acquireLegacyV18CutoverGate(context.Background(), home)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if gate != nil {
					_ = gate.Close()
				}
			}()

			archive, err := promoteLegacyV18CutoverArchiveCandidate(home, gate.archiveCandidate)
			if err != nil {
				t.Fatal(err)
			}
			fleetID, err := legacyV18CutoverFleetID(sqliteConnQueryer{ctx: context.Background(), conn: gate.conn})
			if err != nil {
				t.Fatal(err)
			}
			input := LegacyV18CutoverManifestInput{
				FleetID:    fleetID,
				ImportedAt: "2026-09-16T00:00:00Z",
				Projects:   []LegacyV18CutoverManifestProjectInput{},
			}
			manifest, err := buildLegacyV18CutoverManifest(home, archive, input)
			if err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			payload = append(payload, '\n')
			artifact := legacyV18CutoverManifestArtifact{
				MigrationID: archive.MigrationID,
				Path:        legacyV18CutoverManifestPath(home, archive.MigrationID),
				SHA256:      canonicalV19SHA256(payload),
				ImportedAt:  input.ImportedAt,
			}

			var aliasPath string
			var exercise func() error
			switch phase {
			case "archive validation":
				aliasPath = archive.Path
				exercise = func() error {
					_, err := buildLegacyV18CutoverManifest(home, archive, input)
					return err
				}
			case "manifest read":
				artifact, err = writeLegacyV18CutoverManifest(home, archive, input)
				if err != nil {
					t.Fatal(err)
				}
				aliasPath = artifact.Path
				exercise = func() error {
					_, err := stabilizeLegacyV18CutoverManifestInput(home, archive, input)
					return err
				}
			case "manifest reuse":
				artifact, err = writeLegacyV18CutoverManifest(home, archive, input)
				if err != nil {
					t.Fatal(err)
				}
				aliasPath = artifact.Path
				exercise = func() error {
					_, err := reuseExactLegacyV18CutoverManifest(gate.source, artifact.Path, payload, artifact.SHA256)
					return err
				}
			case "manifest candidate":
				aliasPath = legacyV18CutoverManifestCandidatePath(home, archive.MigrationID)
				exercise = func() error {
					return prepareLegacyV18CutoverManifestCandidate(gate.source, aliasPath, payload, artifact.SHA256)
				}
			default:
				t.Fatalf("unknown phase %q", phase)
			}

			if err := os.Remove(aliasPath); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if err := os.Link(Path(home), aliasPath); err != nil {
				t.Skipf("platform cannot create hard-link alias: %v", err)
			}
			exerciseErr := exercise()
			if got := runLegacyV18CutoverWriteOnceProcess(t, home); got != "busy" {
				t.Fatalf("independent writer after %s alias refusal = %q, want busy; refusal error=%v", phase, got, exerciseErr)
			}
			if exerciseErr == nil {
				t.Fatalf("%s accepted an artifact aliased to the active source", phase)
			}

			if err := os.Remove(aliasPath); err != nil {
				t.Fatal(err)
			}
			if err := gate.Close(); err != nil {
				t.Fatal(err)
			}
			gate = nil
			if got := runLegacyV18CutoverWriteOnceProcess(t, home); got != "committed" {
				t.Fatalf("independent writer after %s refusal cleanup = %q, want committed", phase, got)
			}
		})
	}
}

func TestLegacyV18CutoverProductionPhasesExcludeIndependentWriters(t *testing.T) {
	home := createLegacyV18CutoverTestSource(t)
	setLegacyV18CutoverTestJournalMode(t, home, "DELETE")
	var phases []string
	gate, err := acquireLegacyV18CutoverGateObserved(context.Background(), home, 5*time.Second, func(phase string) {
		phases = append(phases, phase)
		if got := runLegacyV18CutoverWriteOnceProcess(t, home); got != "busy" {
			t.Fatalf("independent writer at %s = %q, want busy", phase, got)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gate.Close() }()
	want := []string{"initial source read", "reader-barrier hash", "archive candidate copy", "EXCLUSIVE revalidation"}
	if strings.Join(phases, "|") != strings.Join(want, "|") {
		t.Fatalf("observed production phases = %q, want %q", phases, want)
	}
}

func runLegacyV18CutoverCommitWriter(t *testing.T) {
	db, err := openLegacyV18CutoverSQLite(Path(os.Getenv(legacyV18CutoverProcessHomeEnv)), "rw", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), `INSERT INTO meta(key, value) VALUES('cutover-process-writer', 'prepared')`); err != nil {
		t.Fatal(err)
	}
	fmt.Println("prepared")
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), `COMMIT`); err == nil {
		fmt.Println("committed")
		return
	} else if !isSQLiteBusy(err) {
		t.Fatal(err)
	}
	fmt.Println("busy")
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), `COMMIT`); err != nil {
		t.Fatal(err)
	}
	fmt.Println("committed")
}

func runLegacyV18CutoverWriteOnce(t *testing.T) {
	db, err := openLegacyV18CutoverSQLite(Path(os.Getenv(legacyV18CutoverProcessHomeEnv)), "rw", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	_, err = db.Exec(`INSERT INTO meta(key, value) VALUES('cutover-process-write-once', 'committed')`)
	switch {
	case err == nil:
		fmt.Println("committed")
	case isSQLiteBusy(err):
		fmt.Println("busy")
	case strings.Contains(err.Error(), legacyV18CutoverFreezeAbortMessage):
		fmt.Println("frozen")
	default:
		t.Fatal(err)
	}
}

func runLegacyV18CutoverWriteOnceProcess(t *testing.T, home string) string {
	t.Helper()
	child := exec.Command(os.Args[0], "-test.run=^TestLegacyV18CutoverGateAndFreezeExcludeIndependentWriters$")
	child.Env = append(os.Environ(),
		legacyV18CutoverProcessRoleEnv+"=write-once",
		legacyV18CutoverProcessHomeEnv+"="+home,
	)
	output, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("independent writer process: %v; output=%s", err, output)
	}
	return strings.SplitN(strings.TrimSpace(string(output)), "\n", 2)[0]
}

func wantLegacyV18CutoverProcessLine(t *testing.T, scanner *bufio.Scanner, want string, stderr *bytes.Buffer) {
	t.Helper()
	line := make(chan string, 1)
	go func() {
		if scanner.Scan() {
			line <- scanner.Text()
			return
		}
		line <- ""
	}()
	select {
	case got := <-line:
		if got != want {
			t.Fatalf("process line = %q, want %q; stderr=%s", got, want, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("process did not report %q; stderr=%s", want, stderr.String())
	}
}

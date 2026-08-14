package faketool

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Version banner written to stderr ahead of a get payload.
const TreehouseBanner = "treehouse 2.1.0"

// Treehouse models a pool whose slots are leased and returned across calls.
type Treehouse struct {
	Slots           []string
	Held            []string
	LeaseIDs        map[string]string
	NoLeaseIdentity bool
	Banner          string
	Log             string
	AcquireHeads    map[string]string
	Responses       []TreehouseResponse
}

type TreehouseResponse struct {
	Command string
	Args    []string
	Stdout  string
	Stderr  string
	Exit    int
}

type treehouseSpec struct {
	Slots           []string
	Held            []string
	LeaseIDs        map[string]string
	NoLeaseIdentity bool
	Banner          string
	StateDir        string
	Log             string
	AcquireHeads    map[string]string
	Responses       []TreehouseResponse
}

func (th Treehouse) Install(t *testing.T, bin string) {
	t.Helper()
	state := stateDir(t, bin, "treehouse")
	for _, slot := range th.Held {
		leaseID := th.LeaseIDs[slot]
		if leaseID == "" {
			leaseID = "held-" + key(slot)
		}
		if th.NoLeaseIdentity {
			leaseID = "leased"
		}
		ensureFile(t, treehouseMarker(state, slot), leaseID+"\n")
	}
	banner := th.Banner
	if banner == "" {
		banner = TreehouseBanner
	}
	installConfig(t, bin, "treehouse", "treehouse", treehouseSpec{
		Slots: th.Slots, Held: th.Held, LeaseIDs: th.LeaseIDs, NoLeaseIdentity: th.NoLeaseIdentity,
		Banner: banner, StateDir: state, Log: th.Log, AcquireHeads: th.AcquireHeads, Responses: th.Responses,
	})
}

func runTreehouseFromPayload(payload json.RawMessage, args []string) int {
	var spec treehouseSpec
	if err := json.Unmarshal(payload, &spec); err != nil {
		return fail("decode treehouse config: %v", err)
	}
	if spec.Log != "" {
		if err := appendInvocation(spec.Log, "treehouse", args); err != nil {
			return fail("log treehouse invocation: %v", err)
		}
	}
	if len(args) == 0 {
		return fail("unexpected treehouse invocation")
	}
	for _, response := range spec.Responses {
		if response.Command != args[0] || (response.Args != nil && !sameArgs(response.Args, args[1:])) {
			continue
		}
		_, _ = io.WriteString(os.Stdout, response.Stdout)
		_, _ = io.WriteString(os.Stderr, response.Stderr)
		return response.Exit
	}
	switch args[0] {
	case "get":
		return treehouseGet(spec)
	case "return":
		return treehouseReturn(spec, args)
	case "status":
		return treehouseStatus(spec)
	case "init":
		return treehouseInit()
	default:
		return fail("unexpected treehouse invocation: %s", strings.Join(args, " "))
	}
}

func treehouseGet(spec treehouseSpec) int {
	for _, slot := range spec.Slots {
		marker := treehouseMarker(spec.StateDir, slot)
		leased, err := treehouseLeased(marker)
		if err != nil {
			return fail("inspect treehouse slot: %v", err)
		}
		if leased {
			continue
		}
		if head := spec.AcquireHeads[slot]; head != "" {
			if output, err := exec.Command("git", "-C", slot, "reset", "--hard", "-q", head).CombinedOutput(); err != nil {
				return fail("reset acquired worktree: %v: %s", err, output)
			}
		}
		counter := treehouseCounter(spec.StateDir)
		if err := atomicWrite(treehouseCounterPath(spec.StateDir), fmt.Sprintf("%d\n", counter+1)); err != nil {
			return fail("write treehouse lease counter: %v", err)
		}
		leaseID := fmt.Sprintf("lease-%d", counter+1)
		markerValue := leaseID
		if spec.NoLeaseIdentity {
			markerValue = "leased"
		}
		if err := atomicWrite(marker, markerValue+"\n"); err != nil {
			return fail("lease treehouse slot: %v", err)
		}
		_, _ = fmt.Fprintln(os.Stderr, spec.Banner)
		if spec.NoLeaseIdentity {
			_, _ = fmt.Fprintf(os.Stdout, "{\"path\":%s}\n", jsonQuote(slot))
		} else {
			_, _ = fmt.Fprintf(os.Stdout, "{\"path\":%s,\"lease_id\":%q,\"lease_holder\":\"\",\"leased_at\":\"2026-01-01T00:00:00Z\"}\n", jsonQuote(slot), leaseID)
		}
		return 0
	}
	_, _ = fmt.Fprintf(os.Stderr, "all %d worktrees are in use or dirty (max_trees = %d). Run 'treehouse status' to see details, or increase max_trees in treehouse.toml\n", len(spec.Slots), len(spec.Slots))
	return 1
}

func treehouseStatus(spec treehouseSpec) int {
	if err := exec.Command("git", "rev-parse", "--show-toplevel").Run(); err != nil {
		return fail("not in a git repository")
	}
	paths := make([]string, 0, len(spec.Slots)+len(spec.Held))
	seen := make(map[string]struct{}, len(spec.Slots)+len(spec.Held))
	for _, path := range append(append([]string{}, spec.Slots...), spec.Held...) {
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	entries := make([]map[string]any, 0, len(paths))
	for _, path := range paths {
		marker := treehouseMarker(spec.StateDir, path)
		data, err := os.ReadFile(marker)
		if err != nil && !os.IsNotExist(err) {
			return fail("inspect treehouse status: %v", err)
		}
		leased := strings.TrimSpace(string(data)) != ""
		leaseID := strings.TrimSpace(string(data))
		if spec.NoLeaseIdentity {
			leaseID = ""
		}
		entry := map[string]any{
			"name":     key(path),
			"path":     path,
			"status":   "available",
			"lease_id": "",
		}
		if leased {
			entry["status"] = "leased"
			entry["lease_id"] = leaseID
			entry["lease_holder"] = ""
			entry["leased_at"] = "2026-01-01T00:00:00Z"
		} else {
			entry["lease_holder"] = ""
			entry["leased_at"] = nil
		}
		entries = append(entries, entry)
	}
	if err := json.NewEncoder(os.Stdout).Encode(entries); err != nil {
		return fail("write treehouse status: %v", err)
	}
	return 0
}

func treehouseReturn(spec treehouseSpec, args []string) int {
	var slot, expectedLeaseID string
	forced := false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--force":
			forced = true
		case "--if-lease-id":
			if i+1 >= len(args) || args[i+1] == "" {
				return fail("missing lease ID for --if-lease-id")
			}
			expectedLeaseID = args[i+1]
			i++
		default:
			if slot != "" {
				return fail("unexpected treehouse invocation: %s", strings.Join(args, " "))
			}
			slot = args[i]
		}
	}
	if slot == "" {
		return fail("unexpected treehouse invocation: %s", strings.Join(args, " "))
	}
	marker := ""
	for _, managed := range append(append([]string{}, spec.Slots...), spec.Held...) {
		if managed == slot {
			marker = treehouseMarker(spec.StateDir, managed)
			break
		}
	}
	if marker == "" {
		return fail("worktree %s is not managed by treehouse", slot)
	}
	if expectedLeaseID != "" {
		current, err := os.ReadFile(marker)
		if err != nil && !os.IsNotExist(err) {
			return fail("inspect treehouse lease: %v", err)
		}
		if strings.TrimSpace(string(current)) != expectedLeaseID {
			return fail("lease identity mismatch: expected %s, current %s", expectedLeaseID, strings.TrimSpace(string(current)))
		}
	}
	dirty, err := treehouseDirty(slot)
	if err != nil {
		return fail("inspect dirty treehouse slot: %v", err)
	}
	if dirty && !forced {
		_, _ = io.WriteString(os.Stderr, "Worktree has uncommitted changes. Clean and return? [Y/n] Aborted.\n")
		return 0
	}
	if dirty {
		if err := treehouseClean(slot); err != nil {
			return fail("clean treehouse slot: %v", err)
		}
	}
	if err := atomicWrite(marker, ""); err != nil {
		return fail("return treehouse slot: %v", err)
	}
	_, _ = io.WriteString(os.Stderr, "Worktree returned to pool.\n")
	return 0
}

func treehouseInit() int {
	path, err := filepath.Abs("treehouse.toml")
	if err != nil {
		return fail("locate treehouse.toml: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		_, _ = io.WriteString(os.Stderr, "treehouse.toml already exists\n")
		return 1
	} else if !os.IsNotExist(err) {
		return fail("inspect treehouse.toml: %v", err)
	}
	if err := os.WriteFile(path, []byte("max_trees = 16\n"), 0o644); err != nil {
		return fail("create treehouse.toml: %v", err)
	}
	_, _ = fmt.Fprintf(os.Stderr, "Created %s\n", path)
	return 0
}

func treehouseDirty(path string) (bool, error) {
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		return false, nil
	}
	out, err := exec.Command("git", "-C", path, "status", "--porcelain").Output()
	if err != nil {
		return false, err
	}
	return len(out) > 0, nil
}

func treehouseClean(path string) error {
	if out, err := exec.Command("git", "-C", path, "reset", "-q", "--hard", "HEAD").CombinedOutput(); err != nil {
		return fmt.Errorf("git reset: %w: %s", err, out)
	}
	if out, err := exec.Command("git", "-C", path, "clean", "-qfd").CombinedOutput(); err != nil {
		return fmt.Errorf("git clean: %w: %s", err, out)
	}
	return nil
}

func treehouseMarker(state, slot string) string {
	return filepath.Join(state, "slot-"+key(slot))
}

func treehouseCounterPath(state string) string {
	return filepath.Join(state, "leases")
}

func treehouseCounter(state string) int {
	data, err := os.ReadFile(treehouseCounterPath(state))
	if err != nil {
		return 0
	}
	var counter int
	_, _ = fmt.Sscanf(string(data), "%d", &counter)
	return counter
}

func treehouseLeased(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(data)) != "", nil
}

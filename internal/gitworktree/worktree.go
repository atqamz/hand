package gitworktree

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/atqamz/hand/internal/fsidentity"
	handgit "github.com/atqamz/hand/internal/git"
)

// ObservationState is the strongest provider-neutral fact available for one
// requested linked-worktree locator.
type ObservationState string

const (
	// ObservationAbsent positively proves no registered worktree and no
	// filesystem object exists at the requested locator.
	ObservationAbsent ObservationState = "absent"
	// ObservationPresent means Git registration or a filesystem object exists;
	// callers must compare all evidence before treating it as the exact binding.
	ObservationPresent ObservationState = "present"
	// ObservationUnknown means observation could not establish either fact.
	ObservationUnknown ObservationState = "unknown"
)

// Observation is bounded exact Git/filesystem evidence for one worktree path.
type Observation struct {
	State                  ObservationState
	Path                   string
	CommonGitDir           string
	PrivateGitDir          string
	LockReason             string
	HeadRevision           string
	PhysicalIdentityDigest string
	Detached               bool
	Prunable               bool
	Detail                 string
}

type porcelainRecord struct {
	path       string
	head       string
	lockReason string
	detached   bool
	prunable   bool
}

// Create performs exactly one native Git linked-worktree creation call. The
// caller is responsible for durably authorizing submission before calling it.
// --lock is part of the same Git command, avoiding an add-then-lock race.
func Create(repositoryPath, requestedPath, basisRevision, lockReason string) error {
	if repositoryPath == "" || requestedPath == "" || basisRevision == "" || lockReason == "" {
		return fmt.Errorf("create native Git worktree: incomplete request")
	}
	if _, err := handgit.Run(repositoryPath, "worktree", "add", "--detach", "--lock", "--reason", lockReason, requestedPath, basisRevision); err != nil {
		return fmt.Errorf("create native Git worktree: %w", err)
	}
	return nil
}

// Observe reads Git registration and filesystem identity without mutation.
func Observe(repositoryPath, requestedPath string) Observation {
	if repositoryPath == "" || requestedPath == "" {
		return Observation{State: ObservationUnknown, Detail: "incomplete observation request"}
	}
	out, err := handgit.Run(repositoryPath, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return Observation{State: ObservationUnknown, Detail: fmt.Sprintf("list registered worktrees: %v", err)}
	}
	records, err := parsePorcelain(out)
	if err != nil {
		return Observation{State: ObservationUnknown, Detail: err.Error()}
	}
	var matches []porcelainRecord
	for _, record := range records {
		if handgit.SamePath(record.path, requestedPath) {
			matches = append(matches, record)
		}
	}
	if len(matches) > 1 {
		return Observation{State: ObservationUnknown, Detail: "multiple Git worktree registrations match requested path"}
	}
	if len(matches) == 0 {
		_, err := os.Lstat(requestedPath)
		if errors.Is(err, os.ErrNotExist) {
			return Observation{State: ObservationAbsent, Path: filepath.Clean(requestedPath)}
		}
		if err != nil {
			return Observation{State: ObservationUnknown, Path: filepath.Clean(requestedPath), Detail: fmt.Sprintf("inspect unregistered worktree path: %v", err)}
		}
		return Observation{State: ObservationPresent, Path: filepath.Clean(requestedPath), Detail: "filesystem object exists without exact Git worktree registration"}
	}

	record := matches[0]
	observation := Observation{
		State:        ObservationPresent,
		Path:         filepath.Clean(record.path),
		LockReason:   record.lockReason,
		HeadRevision: record.head,
		Detached:     record.detached,
		Prunable:     record.prunable,
	}
	info, err := os.Lstat(record.path)
	if err != nil {
		observation.Detail = fmt.Sprintf("registered worktree path is not directly observable: %v", err)
		return observation
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		observation.Detail = "registered worktree path is not a direct directory"
		return observation
	}
	root, err := handgit.ResolveRoot(record.path)
	if err != nil || !handgit.SamePath(root, record.path) {
		observation.Detail = fmt.Sprintf("registered worktree root mismatch: root=%q err=%v", root, err)
		return observation
	}
	common, err := handgit.CommonDir(record.path)
	if err != nil {
		observation.Detail = fmt.Sprintf("resolve registered worktree common Git directory: %v", err)
		return observation
	}
	privateOut, err := handgit.Run(record.path, "rev-parse", "--absolute-git-dir")
	if err != nil {
		observation.Detail = fmt.Sprintf("resolve registered worktree private Git directory: %v", err)
		return observation
	}
	private := filepath.Clean(filepath.FromSlash(strings.TrimSpace(privateOut)))
	if private == "" {
		observation.Detail = "registered worktree private Git directory is empty"
		return observation
	}
	privateInfo, err := os.Lstat(private)
	if err != nil || privateInfo.Mode()&os.ModeSymlink != 0 || !privateInfo.IsDir() {
		observation.Detail = fmt.Sprintf("registered worktree private Git directory is not a direct directory: %v", err)
		return observation
	}
	head, err := handgit.HeadCommit(record.path)
	if err != nil {
		observation.Detail = fmt.Sprintf("resolve registered worktree HEAD: %v", err)
		return observation
	}
	digest, err := fsidentity.DirectoryDigest(record.path)
	if err != nil {
		observation.Detail = fmt.Sprintf("capture registered worktree physical identity: %v", err)
		return observation
	}
	after, err := os.Lstat(record.path)
	if err != nil || !os.SameFile(info, after) {
		observation.Detail = "registered worktree physical identity changed during observation"
		return observation
	}
	observation.CommonGitDir = common
	observation.PrivateGitDir = private
	observation.HeadRevision = head
	observation.PhysicalIdentityDigest = digest
	return observation
}

func parsePorcelain(out string) ([]porcelainRecord, error) {
	fields := strings.Split(out, "\x00")
	var records []porcelainRecord
	var current *porcelainRecord
	flush := func() error {
		if current == nil {
			return nil
		}
		if current.path == "" {
			return fmt.Errorf("parse Git worktree porcelain: record has no path")
		}
		records = append(records, *current)
		current = nil
		return nil
	}
	for _, field := range fields {
		if field == "" {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		if strings.HasPrefix(field, "worktree ") {
			if current != nil {
				if err := flush(); err != nil {
					return nil, err
				}
			}
			path := strings.TrimPrefix(field, "worktree ")
			if path == "" {
				return nil, fmt.Errorf("parse Git worktree porcelain: empty path")
			}
			current = &porcelainRecord{path: filepath.FromSlash(path)}
			continue
		}
		if current == nil {
			return nil, fmt.Errorf("parse Git worktree porcelain: attribute before worktree path")
		}
		switch {
		case strings.HasPrefix(field, "HEAD "):
			current.head = strings.TrimPrefix(field, "HEAD ")
		case field == "detached":
			current.detached = true
		case field == "locked":
			current.lockReason = ""
		case strings.HasPrefix(field, "locked "):
			current.lockReason = strings.TrimPrefix(field, "locked ")
		case field == "prunable" || strings.HasPrefix(field, "prunable "):
			current.prunable = true
		case field == "bare" || strings.HasPrefix(field, "branch "):
			// These are valid stable porcelain attributes but not part of the
			// WorktreeBinding proof.
		default:
			return nil, fmt.Errorf("parse Git worktree porcelain: unknown attribute %q", field)
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return records, nil
}

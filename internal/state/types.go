// Package state manages per-task metadata in state/<id>.json.
package state

const (
	KindShip  = "ship"
	KindScout = "scout"
)

type Herdr struct {
	Session     string `json:"session"`
	WorkspaceID string `json:"workspace_id"`
	TabID       string `json:"tab_id"`
	PaneID      string `json:"pane_id"`
}

type Task struct {
	ID       string `json:"id"`
	Project  string `json:"project"`
	Kind     string `json:"kind"`
	Harness  string `json:"harness"`
	Model    string `json:"model"`
	Effort   string `json:"effort"`
	Worktree string `json:"worktree"`
	Brief    string `json:"brief"`
	Herdr    Herdr  `json:"herdr"`
	PR       string `json:"pr"`
	Merged   bool   `json:"merged"`
	MergedAt string `json:"merged_at"`
	// ReportOffset is how far hand watch has consumed the task's report file.
	// It is durable so a watcher restart resumes exactly where it stopped
	// instead of replaying every line the previous run already surfaced.
	ReportOffset int64 `json:"report_offset"`
	// PRMergedObserved records that hand watch's own gh poll already announced
	// this PR merged. Distinct from Merged, which means hand performed the
	// merge; a restarted watcher needs to know the announcement went out.
	PRMergedObserved bool `json:"pr_merged_observed"`
	// DoneVerified records that hand watch already announced the verified "done"
	// line for this task. Durable for the same reason PRMergedObserved is:
	// evidence can land while the watcher is down (hand merge writes Merged
	// without touching the dashboard), and a restart that re-derived this from
	// current evidence would conclude the line had already gone out and never
	// print it.
	DoneVerified bool   `json:"done_verified"`
	CreatedAt    string `json:"created_at"`
}

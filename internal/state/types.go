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
	ID        string `json:"id"`
	Project   string `json:"project"`
	Kind      string `json:"kind"`
	Harness   string `json:"harness"`
	Model     string `json:"model"`
	Effort    string `json:"effort"`
	Worktree  string `json:"worktree"`
	Brief     string `json:"brief"`
	Herdr     Herdr  `json:"herdr"`
	PR        string `json:"pr"`
	Merged    bool   `json:"merged"`
	MergedAt  string `json:"merged_at"`
	CreatedAt string `json:"created_at"`
}

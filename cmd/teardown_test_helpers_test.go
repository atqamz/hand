package cmd

import (
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/runtime"
)

func closeTaskTab(client *herdr.Client, workspaceID, tabID string) error {
	return runtime.CloseTaskTab(client, workspaceID, tabID)
}

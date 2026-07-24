package ghutil

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

func PRIsMerged(ctx context.Context, pr string) (bool, error) {
	out, err := exec.CommandContext(ctx, "gh", "pr", "view", pr, "--json", "state").CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("gh pr view failed: %s", strings.TrimSpace(string(out)))
	}
	var body struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		return false, fmt.Errorf("parse gh pr view output: %w", err)
	}
	return body.State == "MERGED", nil
}

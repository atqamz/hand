package herdr

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// AgentPromptContext submits one prompt to the live agent that currently owns target.
// A nil return proves only that Herdr completed the prompt text + Enter writes; it does
// not prove the agent started a turn or consumed any application-level input.
func (c *Client) AgentPromptContext(ctx context.Context, target, text string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	args := []string{"agent", "prompt", target, text}
	trimmed, stderr, runErr := c.runContext(ctx, args...)
	if len(trimmed) == 0 {
		if runErr != nil {
			if env, err := parseEnvelope(args, []byte(stderr)); err == nil && env.Error != nil {
				return c.normalizeAPIError(newAPIError(args, env.Error))
			}
			return fmt.Errorf("herdr agent prompt: %w: %s", runErr, stderr)
		}
		return errors.New("herdr agent prompt: empty response")
	}

	env, err := parseEnvelope(args, trimmed)
	if err != nil {
		return err
	}
	if env.Error != nil {
		return c.normalizeAPIError(newAPIError(args, env.Error))
	}
	if runErr != nil {
		return fmt.Errorf("herdr agent prompt: %w: %s", runErr, stderr)
	}
	if len(env.Result) == 0 || string(env.Result) == "null" {
		return errors.New("herdr agent prompt: response missing result")
	}
	return nil
}

// IsAgentPromptPreSideEffectRejection reports only Herdr agent-prompt failures whose
// v0.8.2 contract rejects the request before terminal input is queued.
func IsAgentPromptPreSideEffectRejection(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) || !strings.HasPrefix(apiErr.Operation, "agent prompt ") {
		return false
	}
	switch strings.ToLower(apiErr.Code) {
	case "agent_blocked", "agent_not_found", "agent_not_ready", "empty_agent_prompt":
		return true
	default:
		return false
	}
}

package cmd

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/atqamz/hand/internal/toolchain"
)

func managedCommandFailure(output []byte, err error) string {
	message := strings.TrimSpace(string(output))
	if message != "" {
		return message
	}
	return err.Error()
}

func runManagedCore(ctx context.Context, name, dir string, args ...string) ([]byte, error) {
	managed, err := toolchain.Resolve()
	if err != nil {
		stdout, _, legacyErr := toolchain.RunLegacyForTests(ctx, name, dir, args...)
		if legacyErr == nil {
			return stdout, nil
		}
		return nil, fmt.Errorf("resolve managed %s: %w", name, err)
	}
	var path string
	switch name {
	case "git":
		path = managed.GitPath
		args = toolchain.GitArgsWithTemplate(managed.GitTemplateDir, args)
	case "treehouse":
		path = managed.TreehousePath
	default:
		return nil, fmt.Errorf("unsupported core tool %q", name)
	}
	spec, err := managed.Process(path, args...)
	if err != nil {
		return nil, err
	}
	spec.Dir = dir
	var stdout, stderr bytes.Buffer
	spec.Stdout = &stdout
	spec.Stderr = &stderr
	if err := spec.Run(ctx); err != nil {
		output := stderr.Bytes()
		if len(output) == 0 {
			output = stdout.Bytes()
		}
		if len(output) == 0 {
			output = []byte(err.Error())
		}
		return output, err
	}
	return stdout.Bytes(), nil
}

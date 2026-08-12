package faketool

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Run dispatches an installed fake executable. The helper binary calls this
// through the same process boundary production code uses for external tools.
func Run() int {
	dir, name, err := fakeExecutable()
	if err != nil {
		return fail("locate fake executable: %v", err)
	}

	data, err := os.ReadFile(configPath(dir, name))
	if err != nil {
		return fail("read fake %s config: %v", name, err)
	}
	var spec installSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return fail("decode fake %s config: %v", name, err)
	}

	switch spec.Kind {
	case "command":
		var config commandConfig
		if err := json.Unmarshal(spec.Payload, &config); err != nil {
			return fail("decode command fake %s: %v", name, err)
		}
		return runCommand(name, config, os.Args[1:])
	case "gh":
		return runGHFromPayload(spec.Payload, os.Args[1:])
	case "herdr":
		return runHerdrFromPayload(spec.Payload, os.Args[1:])
	case "treehouse":
		return runTreehouseFromPayload(spec.Payload, os.Args[1:])
	case "no-mistakes":
		return runNoMistakesFromPayload(spec.Payload, os.Args[1:])
	default:
		return fail("unknown fake kind %q", spec.Kind)
	}
}

func fakeExecutable() (string, string, error) {
	executable, err := os.Executable()
	if err != nil {
		executable = os.Args[0]
		if !filepath.IsAbs(executable) {
			executable, err = filepath.Abs(executable)
			if err != nil {
				return "", "", err
			}
		}
	}
	return filepath.Dir(executable), strings.TrimSuffix(filepath.Base(executable), filepath.Ext(executable)), nil
}

func runCommand(name string, config commandConfig, args []string) int {
	if config.Log != "" {
		if err := appendInvocation(config.Log, name, args); err != nil {
			return fail("log %s invocation: %v", name, err)
		}
	}
	if config.FileAction != nil {
		action := config.FileAction
		if action.PathArg < 0 || action.PathArg >= len(args) {
			return fail("file action for %s needs argv[%d], got %d arguments", name, action.PathArg, len(args))
		}
		path := filepath.Join(args[action.PathArg], action.Relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fail("create file action directory: %v", err)
		}
		flags := os.O_CREATE | os.O_WRONLY
		if action.Append {
			flags |= os.O_APPEND
		} else {
			flags |= os.O_TRUNC
		}
		file, err := os.OpenFile(path, flags, 0o644)
		if err != nil {
			return fail("write file action: %v", err)
		}
		_, writeErr := io.WriteString(file, action.Content)
		closeErr := file.Close()
		if writeErr != nil {
			return fail("write file action: %v", writeErr)
		}
		if closeErr != nil {
			return fail("close file action: %v", closeErr)
		}
	}
	if !config.Args && config.FileAction == nil && len(args) != 0 {
		return fail("unexpected %s invocation: %s", name, strings.Join(args, " "))
	}
	if config.Stdout != "" {
		_, _ = io.WriteString(os.Stdout, config.Stdout)
	}
	if config.Args {
		_, _ = fmt.Fprintf(os.Stdout, "%d\n", len(args))
		for _, arg := range args {
			_, _ = fmt.Fprintln(os.Stdout, arg)
		}
	}
	if config.Stderr != "" {
		_, _ = io.WriteString(os.Stderr, config.Stderr)
	}
	return config.Exit
}

func appendInvocation(path, name string, args []string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintln(file, strings.Join(append([]string{name}, args...), " "))
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func sameArgs(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func fail(format string, args ...any) int {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	return 1
}

func atomicWrite(path, content string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

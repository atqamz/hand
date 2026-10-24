package e2e_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

const mockKey = "sk-ant-e2e-mock-0000000000000000"

type kit struct {
	name, model, effort string
	seed                func(sb *sandbox, url string)
	cwd                 func(texts []string) string
}

func (k kit) flags() []string {
	return []string{"--harness", k.name, "--model", k.model, "--effort", k.effort}
}

func (k kit) shell(exe string, args ...string) string {
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	prefix := ""
	if k.name == "codex" && runtime.GOOS == "windows" {
		quote = func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
		prefix = "& "
	}
	cmd := prefix + quote(exe)
	for _, a := range args {
		cmd += " " + quote(a)
	}
	return cmd
}

var claudeKit = kit{
	name:   "claude",
	model:  "sonnet",
	effort: "low",
	seed: func(sb *sandbox, url string) {
		settings := map[string]any{
			"theme":                             "dark",
			"skipDangerousModePermissionPrompt": true,
			"env": map[string]string{
				"ANTHROPIC_BASE_URL":                       url,
				"ANTHROPIC_API_KEY":                        mockKey,
				"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
				"DISABLE_AUTOUPDATER":                      "1",
				"DISABLE_TELEMETRY":                        "1",
				"DISABLE_ERROR_REPORTING":                  "1",
			},
		}
		if bash := `C:\Program Files\Git\bin\bash.exe`; runtime.GOOS == "windows" {
			if _, err := os.Stat(bash); err == nil {
				settings["env"].(map[string]string)["CLAUDE_CODE_GIT_BASH_PATH"] = bash
			}
		}
		state := map[string]any{
			"hasCompletedOnboarding": true,
			"theme":                  "dark",
			"customApiKeyResponses":  map[string]any{"approved": []string{mockKey[len(mockKey)-20:]}, "rejected": []string{}},
		}
		sb.write(filepath.Join(sb.home, ".claude", "settings.json"), mustJSON(settings))
		sb.write(filepath.Join(sb.home, ".claude.json"), mustJSON(state))
	},
	cwd: func(texts []string) string { return firstMatch(texts, claudeCwd) },
}

var codexKit = kit{
	name:   "codex",
	model:  "mock-model",
	effort: "low",
	seed: func(sb *sandbox, url string) {
		sb.write(filepath.Join(sb.codexHome, "config.toml"), fmt.Sprintf(`model = "mock-model"
model_provider = "mock"

[features]
daemon_auto_start = false

[model_providers.mock]
name = "Mock"
base_url = "%s/v1"
wire_api = "responses"
supports_websockets = false
`, url))
	},
	cwd: func(texts []string) string { return firstMatch(texts, codexCwd) },
}

var (
	claudeCwd = regexp.MustCompile(`Primary working directory: ([^\n]+)`)
	codexCwd  = regexp.MustCompile(`<cwd>([^<]+)</cwd>`)
)

func firstMatch(texts []string, re *regexp.Regexp) string {
	for _, s := range texts {
		if m := re.FindStringSubmatch(s); m != nil {
			return strings.TrimSpace(m[1])
		}
	}
	return ""
}

func mustJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err)
	}
	return string(b) + "\n"
}

func sameDir(a, b string) bool {
	a, b = strings.TrimPrefix(a, `\\?\`), strings.TrimPrefix(b, `\\?\`)
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

package harness

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var copilotTimeout = 10 * time.Second

var copilotEfforts = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

var copilotModelLine = regexp.MustCompile(`^\s+- "([^"]+)"\s*$`)

var copilotLister = lister{
	ttl:      6 * time.Hour,
	prefixes: []string{"COPILOT_"},
	files:    func(Env) []string { return nil },
	list: func(ctx context.Context, x runner, _ Env) ([]Model, bool, error) {
		out, _, err := x.run(ctx, copilotTimeout, "", "help", "config")
		if err != nil {
			return nil, false, fmt.Errorf("copilot help config %w", err)
		}
		names := copilotModelNames(string(out))
		if len(names) == 0 {
			return nil, false, errors.New("copilot help config lists no models under `model`; its format may have changed")
		}
		models := []Model{{Name: "auto", Source: SourceDiscovered}}
		for _, n := range names {
			models = append(models, Model{Name: n, Efforts: copilotEfforts, Source: SourceDiscovered, Unverified: true})
		}
		return models, true, nil
	},
}

func copilotModelNames(help string) []string {
	var names []string
	in := false
	for _, line := range strings.Split(help, "\n") {
		switch m := copilotModelLine.FindStringSubmatch(line); {
		case in && m != nil:
			names = append(names, m[1])
		case in && strings.TrimSpace(line) == "":
		case in:
			return names
		default:
			in = strings.HasPrefix(strings.TrimSpace(line), "`model`:")
		}
	}
	return names
}

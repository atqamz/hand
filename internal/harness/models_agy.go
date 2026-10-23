package harness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var agyTimeout = 30 * time.Second

var agyLister = lister{
	ttl:   6 * time.Hour,
	files: func(Env) []string { return nil },
	list: func(ctx context.Context, x runner, _ Env) ([]Model, bool, error) {
		out, errOut, err := x.run(ctx, agyTimeout, "", "models")
		if err != nil {
			if bytes.Contains(bytes.ToLower(errOut), []byte("sign in")) {
				return nil, false, errors.New("agy is not signed in; run agy and sign in")
			}
			return nil, false, fmt.Errorf("agy models %w", err)
		}
		var models []Model
		for _, line := range strings.Split(string(out), "\n") {
			if id, label, ok := strings.Cut(line, "\t"); ok {
				models = append(models, Model{Name: strings.TrimSpace(id), Label: label, Source: SourceDiscovered})
			}
		}
		return models, true, nil
	},
}

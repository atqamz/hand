package main

import (
	"testing"

	"github.com/atqamz/hand/internal/desktop"
)

func TestFleetTitle(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fleet desktop.Fleet
		want  string
	}{
		{name: "a named fleet", fleet: desktop.Fleet{ID: "f1", Name: "alpha"}, want: "Hand · alpha"},
		{name: "an id without a name", fleet: desktop.Fleet{ID: "f1"}, want: "Hand"},
		{name: "a name without an id", fleet: desktop.Fleet{Name: "alpha"}, want: "Hand"},
		{name: "no fleet", want: "Hand"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var titles []string
			setFleetTitle(func(title string) { titles = append(titles, title) }, tc.fleet)
			if len(titles) != 1 || titles[0] != tc.want {
				t.Fatalf("titles = %q, want one %q", titles, tc.want)
			}
		})
	}
}

package routing

import (
	"errors"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/brief"
)

func TestResolveQualifiesRuntimeWithoutExplicitModel(t *testing.T) {
	called := 0
	availability := Availability{
		IsSupported:       func(name string) bool { return name == "antigravity" },
		PlatformSupported: func(string) bool { return true },
		OnPath:            func(string) bool { return true },
		SupportsModel:     func(string) bool { return true },
		SupportsEffort:    func(string) bool { return true },
		CarriesPrompt:     func(string) bool { return true },
		ValidateRuntime: func(name string) error {
			called++
			if name != "antigravity" {
				t.Fatalf("ValidateRuntime(%q), want antigravity", name)
			}
			return errors.New("headless contract unknown")
		},
	}
	_, err := Resolve(Request{
		Kind:        TaskKindShip,
		Declaration: brief.Declaration{ExecutionClass: brief.ExecutionClassStandard},
		Profile:     "agy", ProfileFromFlag: true,
	}, Config{Profiles: []Profile{{Name: "agy", Harness: "antigravity"}}}, LegacyDefaults{}, availability)
	if err == nil || !strings.Contains(err.Error(), "headless contract unknown") || called != 1 {
		t.Fatalf("Resolve() err=%v runtime calls=%d, want one pre-dispatch runtime qualification", err, called)
	}
}

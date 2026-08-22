package launch

import (
	"reflect"
	"strings"
	"testing"
)

func TestNewSpecRejectsUnsafeFields(t *testing.T) {
	tests := []struct {
		name string
		spec LaunchSpec
	}{
		{name: "empty executable", spec: LaunchSpec{Args: []string{"--help"}}},
		{name: "empty environment key", spec: LaunchSpec{Executable: "worker", Env: map[string]string{"": "value"}}},
		{name: "equals in environment key", spec: LaunchSpec{Executable: "worker", Env: map[string]string{"BAD=KEY": "value"}}},
		{name: "control in executable", spec: LaunchSpec{Executable: "worker\n", Env: map[string]string{}}},
		{name: "control in environment value", spec: LaunchSpec{Executable: "worker", Env: map[string]string{"KEY": "value\x00"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewSpec(test.spec); err == nil {
				t.Fatal("NewSpec accepted unsafe launch data")
			}
		})
	}
}

func TestNewSpecDefensivelyCopiesInput(t *testing.T) {
	args := []string{"--prompt", "literal"}
	env := map[string]string{"WORKER_FLAG": "one"}
	spec, err := NewSpec(LaunchSpec{Executable: "worker", Args: args, Env: env, Cwd: "/tmp/worktree"})
	if err != nil {
		t.Fatal(err)
	}

	args[1] = "changed"
	env["WORKER_FLAG"] = "changed"
	if spec.Args[1] != "literal" || spec.Env["WORKER_FLAG"] != "one" {
		t.Fatalf("spec retained mutable input: %+v", spec)
	}
}

func TestCloneDefensivelyCopiesSpec(t *testing.T) {
	spec, err := NewSpec(LaunchSpec{Executable: "worker", Args: []string{"arg"}, Env: map[string]string{"KEY": "value"}})
	if err != nil {
		t.Fatal(err)
	}
	clone := spec.Clone()
	clone.Args[0] = "changed"
	clone.Env["KEY"] = "changed"
	if spec.Args[0] != "arg" || spec.Env["KEY"] != "value" {
		t.Fatalf("clone shares mutable data: original=%+v clone=%+v", spec, clone)
	}
}

func TestMergeEnvUsesExplicitLaterLayerPrecedence(t *testing.T) {
	spec, err := NewSpec(LaunchSpec{
		Executable: "worker",
		Env:        map[string]string{"SHARED": "harness", "HARNESS": "yes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	merged, err := spec.MergeEnv(
		map[string]string{"SHARED": "hand", "HAND_ROLE": "worker"},
		map[string]string{"PATH": "/managed/bin", "SHARED": "managed"},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := LaunchSpec{
		Executable: "worker",
		Env:        map[string]string{"SHARED": "managed", "HARNESS": "yes", "HAND_ROLE": "worker", "PATH": "/managed/bin"},
	}
	if !reflect.DeepEqual(merged, want) {
		t.Fatalf("merged = %+v, want %+v", merged, want)
	}
	if strings.Contains(spec.Env["SHARED"], "managed") {
		t.Fatal("MergeEnv mutated the source spec")
	}
}

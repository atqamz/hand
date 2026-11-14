package cli

import (
	"slices"
	"testing"
)

func TestWithEnvKeepsOneValuePerName(t *testing.T) {
	got := withEnv([]string{"PATH=/one", "HOME=/h", "PATH=/two"}, "PATH=/three")
	want := []string{"HOME=/h", "PATH=/three"}
	if !slices.Equal(got, want) {
		t.Fatalf("withEnv = %q, want %q", got, want)
	}
	got = withEnv([]string{"HOME=/h"}, "PATH=/three", "HOME=/other")
	want = []string{"PATH=/three", "HOME=/other"}
	if !slices.Equal(got, want) {
		t.Fatalf("withEnv = %q, want %q", got, want)
	}
}

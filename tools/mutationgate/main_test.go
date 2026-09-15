package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareAcceptsOnlyExactReviewedEvidence(t *testing.T) {
	baseline := fixtureBaseline()
	result := fixtureResult("KILLED", "LIVED")
	if err := compare(baseline, result, "github.com/example/hand", "./internal/age", "v0.6.0", "test"); err != nil {
		t.Fatalf("compare exact evidence: %v", err)
	}
}

func TestCompareRejectsInconclusiveAndChangedEvidence(t *testing.T) {
	cases := []struct {
		name   string
		base   baselineFile
		result gremlinsResult
		want   string
	}{
		{
			name:   "formerly killed mutant lived",
			base:   fixtureBaseline(),
			result: fixtureResult("LIVED", "LIVED"),
			want:   "mutation inventory differs",
		},
		{
			name: "new unsuppressed survivor",
			base: fixtureBaseline(),
			result: gremlinsResult{GoModule: "github.com/example/hand", MutantsTotal: 3, MutantsKilled: 1, MutantsLived: 2, Files: []resultFile{
				{Filename: "age.go", Mutations: []mutation{
					{Line: 10, Column: 2, Type: "ARITHMETIC_BASE", Status: "KILLED"},
					{Line: 11, Column: 2, Type: "INCREMENT_DECREMENT", Status: "LIVED"},
					{Line: 12, Column: 2, Type: "CONDITIONALS_NEGATION", Status: "LIVED"},
				}},
			}},
			want: "mutation inventory differs",
		},
		{
			name:   "timed out mutant",
			base:   fixtureBaseline(),
			result: fixtureResult("TIMED OUT", "LIVED"),
			want:   "inconclusive status",
		},
		{
			name: "truncated totals",
			base: fixtureBaseline(),
			result: gremlinsResult{GoModule: "github.com/example/hand", MutantsTotal: 2, MutantsKilled: 2, Files: []resultFile{
				{Filename: "age.go", Mutations: []mutation{{Line: 10, Column: 2, Type: "ARITHMETIC_BASE", Status: "KILLED"}}},
			}},
			want: "incomplete results",
		},
		{
			name: "identity drift",
			base: fixtureBaseline(),
			result: gremlinsResult{GoModule: "github.com/example/hand", MutantsTotal: 2, MutantsKilled: 1, MutantsLived: 1, Files: []resultFile{
				{Filename: "age.go", Mutations: []mutation{
					{Line: 99, Column: 2, Type: "ARITHMETIC_BASE", Status: "KILLED"},
					{Line: 11, Column: 2, Type: "INCREMENT_DECREMENT", Status: "LIVED"},
				}},
			}},
			want: "mutation inventory differs",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := compare(tc.base, tc.result, "github.com/example/hand", "./internal/age", "v0.6.0", "test")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("compare error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestLoadRejectsMalformedEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.json")
	if err := os.WriteFile(path, []byte(`{"GoModule":"github.com/example/hand"`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadResult(path); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("loadResult error = %v, want malformed JSON rejection", err)
	}
}

func fixtureBaseline() baselineFile {
	result := fixtureResult("KILLED", "LIVED")
	return baselineFile{
		Schema: 1,
		Tool:   "v0.6.0",
		Tags:   "test",
		Packages: []baselinePackage{{
			Path:         "./internal/age",
			Digest:       resultDigest(result),
			Total:        2,
			Killed:       1,
			Lived:        1,
			Suppressions: []suppression{{File: "age.go", Line: 11, Column: 2, Operator: "INCREMENT_DECREMENT", Rationale: "The counter is only compared with zero, so either direction is observationally identical."}},
		}},
	}
}

func fixtureResult(first, second string) gremlinsResult {
	return gremlinsResult{
		GoModule:      "github.com/example/hand",
		MutantsTotal:  2,
		MutantsKilled: boolToInt(first == "KILLED") + boolToInt(second == "KILLED"),
		MutantsLived:  boolToInt(first == "LIVED") + boolToInt(second == "LIVED"),
		Files: []resultFile{{
			Filename: "age.go",
			Mutations: []mutation{
				{Line: 10, Column: 2, Type: "ARITHMETIC_BASE", Status: first},
				{Line: 11, Column: 2, Type: "INCREMENT_DECREMENT", Status: second},
			},
		}},
	}
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

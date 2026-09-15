package main

import (
	"encoding/json"
	"fmt"
	"math"
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

func TestCompareDoesNotBlockNotCovered(t *testing.T) {
	t.Run("transition from reviewed mutant", func(t *testing.T) {
		result := fixtureResult("KILLED", "LIVED")
		result.MutantsTotal--
		result.MutantsKilled--
		result.MutantsNotCovered++
		result.Files[0].Mutations[0].Status = "NOT COVERED"
		completeFixtureMetadata(&result)
		if err := compare(fixtureBaseline(), result, "github.com/example/hand", "./internal/age", "v0.6.0", "test"); err != nil {
			t.Fatalf("compare NOT COVERED transition: %v", err)
		}
	})
	t.Run("new uncovered mutant", func(t *testing.T) {
		result := fixtureResult("KILLED", "LIVED")
		result.MutantsNotCovered++
		result.Files[0].Mutations = append(result.Files[0].Mutations, mutation{
			Line: 12, Column: 2, Type: "CONDITIONALS_BOUNDARY", Status: "NOT COVERED",
		})
		completeFixtureMetadata(&result)
		if err := compare(fixtureBaseline(), result, "github.com/example/hand", "./internal/age", "v0.6.0", "test"); err != nil {
			t.Fatalf("compare new NOT COVERED mutant: %v", err)
		}
	})
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
			completeFixtureMetadata(&tc.result)
			err := compare(tc.base, tc.result, "github.com/example/hand", "./internal/age", "v0.6.0", "test")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("compare error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestCompareRejectsInvalidMetadata(t *testing.T) {
	cases := []struct {
		name string
		edit func(*gremlinsResult)
	}{
		{"zero elapsed time", func(result *gremlinsResult) { result.ElapsedTime = 0 }},
		{"negative elapsed time", func(result *gremlinsResult) { result.ElapsedTime = -1 }},
		{"non-finite elapsed time", func(result *gremlinsResult) { result.ElapsedTime = math.Inf(1) }},
		{"out-of-range efficacy", func(result *gremlinsResult) { result.TestEfficacy = 101 }},
		{"non-finite efficacy", func(result *gremlinsResult) { result.TestEfficacy = math.NaN() }},
		{"inconsistent efficacy", func(result *gremlinsResult) { result.TestEfficacy = 49 }},
		{"out-of-range coverage", func(result *gremlinsResult) { result.MutationsCoverage = 101 }},
		{"non-finite coverage", func(result *gremlinsResult) { result.MutationsCoverage = math.Inf(1) }},
		{"inconsistent coverage", func(result *gremlinsResult) { result.MutationsCoverage = 99 }},
		{"empty mutator statistics", func(result *gremlinsResult) { result.Statistics = map[string]int{} }},
		{"inconsistent mutator statistics", func(result *gremlinsResult) { result.Statistics["arithmetic_base"] = 2 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := fixtureResult("KILLED", "LIVED")
			tc.edit(&result)
			err := compare(fixtureBaseline(), result, "github.com/example/hand", "./internal/age", "v0.6.0", "test")
			if err == nil || !strings.Contains(err.Error(), "invalid result metadata") {
				t.Fatalf("compare error = %v, want invalid metadata rejection", err)
			}
		})
	}
}

func TestBaselineInventoryIsSorted(t *testing.T) {
	baseline, err := loadBaseline(filepath.Join("..", "..", "mutation", "baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	for index := 1; index < len(baseline.Packages); index++ {
		if baseline.Packages[index-1].Path > baseline.Packages[index].Path {
			t.Fatalf("packages not sorted: %q before %q", baseline.Packages[index-1].Path, baseline.Packages[index].Path)
		}
	}
	for _, pkg := range baseline.Packages {
		for index := 1; index < len(pkg.Mutants); index++ {
			if baselineMutationKey(pkg.Mutants[index-1]) > baselineMutationKey(pkg.Mutants[index]) {
				t.Fatalf("%s mutants not sorted", pkg.Path)
			}
		}
		for index := 1; index < len(pkg.Suppressions); index++ {
			if suppressionKey(pkg.Suppressions[index-1]) > suppressionKey(pkg.Suppressions[index]) {
				t.Fatalf("%s suppressions not sorted", pkg.Path)
			}
		}
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

func TestLoadRejectsMissingRequiredResultFields(t *testing.T) {
	data, err := json.Marshal(fixtureResult("KILLED", "LIVED"))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		"go_module", "files", "test_efficacy", "mutations_coverage", "mutants_total",
		"mutants_killed", "mutants_lived", "mutants_not_viable", "mutants_not_covered",
		"elapsed_time", "mutator_statistics",
	} {
		t.Run(field, func(t *testing.T) {
			missing := make(map[string]json.RawMessage, len(fields)-1)
			for key, value := range fields {
				if key != field {
					missing[key] = value
				}
			}
			path := filepath.Join(t.TempDir(), "results.json")
			data, err := json.Marshal(missing)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadResult(path); err == nil || !strings.Contains(err.Error(), "missing required result field") {
				t.Fatalf("loadResult error = %v, want missing required result field", err)
			}
		})
	}
}

func fixtureBaseline() baselineFile {
	return baselineFile{
		Schema: 2,
		Tool:   "v0.6.0",
		Tags:   "test",
		Packages: []baselinePackage{{
			Path: "./internal/age",
			Mutants: []baselineMutation{
				{File: "age.go", Line: 10, Column: 2, Operator: "ARITHMETIC_BASE", Outcome: "KILLED"},
				{File: "age.go", Line: 11, Column: 2, Operator: "INCREMENT_DECREMENT", Outcome: "LIVED"},
			},
			Suppressions: []suppression{{File: "age.go", Line: 11, Column: 2, Operator: "INCREMENT_DECREMENT", Rationale: "The counter is only compared with zero, so either direction is observationally identical."}},
		}},
	}
}

func fixtureResult(first, second string) gremlinsResult {
	return gremlinsResult{
		GoModule:          "github.com/example/hand",
		TestEfficacy:      50,
		MutationsCoverage: 100,
		MutantsTotal:      2,
		MutantsKilled:     boolToInt(first == "KILLED") + boolToInt(second == "KILLED"),
		MutantsLived:      boolToInt(first == "LIVED") + boolToInt(second == "LIVED"),
		ElapsedTime:       1,
		Statistics:        map[string]int{"arithmetic_base": 1, "increment_decrement": 1},
		Files: []resultFile{{
			Filename: "age.go",
			Mutations: []mutation{
				{Line: 10, Column: 2, Type: "ARITHMETIC_BASE", Status: first},
				{Line: 11, Column: 2, Type: "INCREMENT_DECREMENT", Status: second},
			},
		}},
	}
}

func baselineMutationKey(mutant baselineMutation) string {
	return fmt.Sprintf("%s:%010d:%010d:%s:%s", mutant.File, mutant.Line, mutant.Column, mutant.Operator, mutant.Outcome)
}

func suppressionKey(suppression suppression) string {
	return fmt.Sprintf("%s:%010d:%010d:%s:%s", suppression.File, suppression.Line, suppression.Column, suppression.Operator, suppression.Rationale)
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func completeFixtureMetadata(result *gremlinsResult) {
	statistics := map[string]int{}
	result.MutantsKilled = 0
	result.MutantsLived = 0
	result.MutantsNotViable = 0
	result.MutantsNotCovered = 0
	for _, file := range result.Files {
		for _, mutant := range file.Mutations {
			switch mutant.Status {
			case "KILLED":
				result.MutantsKilled++
			case "LIVED":
				result.MutantsLived++
			case "NOT VIABLE":
				result.MutantsNotViable++
			case "NOT COVERED":
				result.MutantsNotCovered++
			}
			statistics[strings.ToLower(mutant.Type)]++
		}
	}
	result.MutantsTotal = result.MutantsKilled + result.MutantsLived + result.MutantsNotViable
	result.TestEfficacy = 0
	if result.MutantsKilled > 0 {
		result.TestEfficacy = float64(result.MutantsKilled) / float64(result.MutantsKilled+result.MutantsLived) * 100
	}
	result.MutationsCoverage = 0
	if result.MutantsKilled+result.MutantsLived > 0 {
		result.MutationsCoverage = float64(result.MutantsKilled+result.MutantsLived) / float64(result.MutantsKilled+result.MutantsLived+result.MutantsNotCovered) * 100
	}
	result.ElapsedTime = 1
	result.Statistics = statistics
}

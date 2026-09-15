package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
)

type baselineFile struct {
	Schema   int               `json:"schema"`
	Tool     string            `json:"tool"`
	Tags     string            `json:"tags"`
	Packages []baselinePackage `json:"packages"`
}

type baselinePackage struct {
	Path         string             `json:"path"`
	Mutants      []baselineMutation `json:"mutants"`
	Suppressions []suppression      `json:"suppressions"`
}

type baselineMutation struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Operator string `json:"operator"`
	Outcome  string `json:"outcome"`
}

type suppression struct {
	File      string `json:"file"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	Operator  string `json:"operator"`
	Rationale string `json:"rationale"`
}

type gremlinsResult struct {
	GoModule          string         `json:"go_module"`
	Files             []resultFile   `json:"files"`
	TestEfficacy      float64        `json:"test_efficacy"`
	MutationsCoverage float64        `json:"mutations_coverage"`
	MutantsTotal      int            `json:"mutants_total"`
	MutantsKilled     int            `json:"mutants_killed"`
	MutantsLived      int            `json:"mutants_lived"`
	MutantsNotViable  int            `json:"mutants_not_viable"`
	MutantsNotCovered int            `json:"mutants_not_covered"`
	ElapsedTime       float64        `json:"elapsed_time"`
	Statistics        map[string]int `json:"mutator_statistics"`
}

type requiredGremlinsResult struct {
	GoModule          *string         `json:"go_module"`
	Files             *[]resultFile   `json:"files"`
	TestEfficacy      *float64        `json:"test_efficacy"`
	MutationsCoverage *float64        `json:"mutations_coverage"`
	MutantsTotal      *int            `json:"mutants_total"`
	MutantsKilled     *int            `json:"mutants_killed"`
	MutantsLived      *int            `json:"mutants_lived"`
	MutantsNotViable  *int            `json:"mutants_not_viable"`
	MutantsNotCovered *int            `json:"mutants_not_covered"`
	ElapsedTime       *float64        `json:"elapsed_time"`
	Statistics        *map[string]int `json:"mutator_statistics"`
}

type resultFile struct {
	Filename  string     `json:"file_name"`
	Mutations []mutation `json:"mutations"`
}

type mutation struct {
	Type   string `json:"type"`
	Status string `json:"status"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

func main() {
	baselinePath := flag.String("baseline", "", "reviewed mutation baseline")
	resultPath := flag.String("results", "", "gremlins JSON result")
	module := flag.String("module", "", "module path from go.mod")
	packagePath := flag.String("package", "", "tested package path")
	tool := flag.String("tool", "v0.6.0", "pinned gremlins version")
	tags := flag.String("tags", "test", "Go build tags")
	flag.Parse()
	if *baselinePath == "" || *resultPath == "" || *module == "" || *packagePath == "" {
		fmt.Fprintln(os.Stderr, "usage: mutationgate -baseline FILE -results FILE -module MODULE -package PACKAGE [-tool v0.6.0] [-tags test]")
		os.Exit(2)
	}
	baseline, err := loadBaseline(*baselinePath)
	if err == nil {
		var result gremlinsResult
		result, err = loadResult(*resultPath)
		if err == nil {
			err = compare(baseline, result, *module, *packagePath, *tool, *tags)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "mutation gate:", err)
		os.Exit(1)
	}
	fmt.Printf("mutation evidence valid: %s (%s, %s)\n", *packagePath, *tool, *tags)
}

func loadBaseline(path string) (baselineFile, error) {
	var baseline baselineFile
	if err := decode(path, &baseline); err != nil {
		return baselineFile{}, fmt.Errorf("decode baseline: %w", err)
	}
	return baseline, nil
}

func loadResult(path string) (gremlinsResult, error) {
	var result requiredGremlinsResult
	if err := decode(path, &result); err != nil {
		return gremlinsResult{}, fmt.Errorf("decode results: %w", err)
	}
	return result.value()
}

func (result requiredGremlinsResult) value() (gremlinsResult, error) {
	if result.GoModule == nil || result.Files == nil || result.TestEfficacy == nil ||
		result.MutationsCoverage == nil || result.MutantsTotal == nil || result.MutantsKilled == nil ||
		result.MutantsLived == nil || result.MutantsNotViable == nil || result.MutantsNotCovered == nil ||
		result.ElapsedTime == nil || result.Statistics == nil {
		return gremlinsResult{}, errors.New("missing required result field")
	}
	return gremlinsResult{
		GoModule: *result.GoModule, Files: *result.Files, TestEfficacy: *result.TestEfficacy,
		MutationsCoverage: *result.MutationsCoverage, MutantsTotal: *result.MutantsTotal,
		MutantsKilled: *result.MutantsKilled, MutantsLived: *result.MutantsLived,
		MutantsNotViable: *result.MutantsNotViable, MutantsNotCovered: *result.MutantsNotCovered,
		ElapsedTime: *result.ElapsedTime, Statistics: *result.Statistics,
	}, nil
}

func decode(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func compare(baseline baselineFile, result gremlinsResult, module, packagePath, tool, tags string) error {
	if baseline.Schema != 2 || baseline.Tool != tool || baseline.Tags != tags {
		return errors.New("baseline metadata does not match pinned tool and build tags")
	}
	if result.GoModule != module {
		return fmt.Errorf("result module %q, want %q", result.GoModule, module)
	}
	baselinePackage, ok := matchingPackage(baseline.Packages, packagePath)
	if !ok {
		return fmt.Errorf("baseline has no package %q", packagePath)
	}
	actual := make(map[string]string)
	counts := map[string]int{}
	statistics := map[string]int{}
	for _, file := range result.Files {
		if !relativeSource(file.Filename) {
			return fmt.Errorf("result file %q is not a package-relative source path", file.Filename)
		}
		for _, mutant := range file.Mutations {
			if mutant.Status == "TIMED OUT" || mutant.Status == "RUNNABLE" || mutant.Status == "SKIPPED" {
				return fmt.Errorf("inconclusive status %q at %s", mutant.Status, identity(file.Filename, mutant.Line, mutant.Column, mutant.Type))
			}
			if !validOutcome(mutant.Status) || mutant.Line < 1 || mutant.Column < 1 || mutant.Type == "" {
				return fmt.Errorf("malformed mutant at %s", file.Filename)
			}
			statistic, ok := mutatorStatistic(mutant.Type)
			if !ok {
				return fmt.Errorf("malformed mutant at %s", file.Filename)
			}
			id := identity(file.Filename, mutant.Line, mutant.Column, mutant.Type)
			if _, exists := actual[id]; exists {
				return fmt.Errorf("duplicate mutant %s", id)
			}
			actual[id] = mutant.Status
			counts[mutant.Status]++
			statistics[statistic]++
		}
	}
	if counts["KILLED"]+counts["LIVED"]+counts["NOT VIABLE"] != result.MutantsTotal ||
		counts["KILLED"] != result.MutantsKilled || counts["LIVED"] != result.MutantsLived ||
		counts["NOT VIABLE"] != result.MutantsNotViable || counts["NOT COVERED"] != result.MutantsNotCovered {
		return errors.New("incomplete results: aggregate counts do not match mutant inventory")
	}
	if err := validateMetadata(result, statistics); err != nil {
		return err
	}
	expected := make(map[string]string, len(baselinePackage.Mutants))
	for _, mutant := range baselinePackage.Mutants {
		if mutant.File == "" || mutant.Line < 1 || mutant.Column < 1 || mutant.Operator == "" || !validOutcome(mutant.Outcome) || mutant.Outcome == "NOT COVERED" {
			return errors.New("malformed baseline mutant")
		}
		id := identity(mutant.File, mutant.Line, mutant.Column, mutant.Operator)
		if _, exists := expected[id]; exists {
			return fmt.Errorf("duplicate baseline mutant %s", id)
		}
		expected[id] = mutant.Outcome
	}
	for id, outcome := range actual {
		if outcome == "NOT COVERED" {
			continue
		}
		if expectedOutcome, ok := expected[id]; !ok || outcome != expectedOutcome {
			return fmt.Errorf("mutation inventory differs from reviewed baseline at %s", id)
		}
	}
	for id := range expected {
		outcome, ok := actual[id]
		if !ok {
			return fmt.Errorf("incomplete results: reviewed mutant %s is missing", id)
		}
		if outcome == "NOT COVERED" {
			continue
		}
	}
	suppressed := make(map[string]struct{})
	for _, entry := range baselinePackage.Suppressions {
		if entry.File == "" || entry.Line < 1 || entry.Column < 1 || entry.Operator == "" || entry.Rationale == "" {
			return errors.New("malformed suppression")
		}
		id := identity(entry.File, entry.Line, entry.Column, entry.Operator)
		if _, exists := suppressed[id]; exists {
			return fmt.Errorf("duplicate suppression %s", id)
		}
		suppressed[id] = struct{}{}
	}
	for id, outcome := range actual {
		if outcome == "LIVED" {
			if _, exists := suppressed[id]; !exists {
				return fmt.Errorf("unsuppressed lived mutant %s", id)
			}
		}
	}
	for id := range suppressed {
		if actual[id] != "LIVED" && actual[id] != "NOT COVERED" {
			return fmt.Errorf("suppression %s does not match a lived mutant", id)
		}
	}
	return nil
}

func matchingPackage(packages []baselinePackage, path string) (baselinePackage, bool) {
	for _, candidate := range packages {
		if candidate.Path == path {
			return candidate, true
		}
	}
	return baselinePackage{}, false
}

func identity(file string, line, column int, operator string) string {
	return fmt.Sprintf("%s:%d:%d:%s", filepath.ToSlash(file), line, column, operator)
}

func validOutcome(outcome string) bool {
	return outcome == "KILLED" || outcome == "LIVED" || outcome == "NOT COVERED" || outcome == "NOT VIABLE"
}

func validateMetadata(result gremlinsResult, statistics map[string]int) error {
	if result.GoModule == "" || !finitePercent(result.TestEfficacy) || !finitePercent(result.MutationsCoverage) ||
		math.IsNaN(result.ElapsedTime) || math.IsInf(result.ElapsedTime, 0) || result.ElapsedTime <= 0 {
		return errors.New("invalid result metadata")
	}
	efficacy := 0.0
	if result.MutantsKilled > 0 {
		efficacy = float64(result.MutantsKilled) / float64(result.MutantsKilled+result.MutantsLived) * 100
	}
	coverage := 0.0
	if result.MutantsKilled+result.MutantsLived > 0 {
		coverage = float64(result.MutantsKilled+result.MutantsLived) / float64(result.MutantsKilled+result.MutantsLived+result.MutantsNotCovered) * 100
	}
	if !sameFloat(result.TestEfficacy, efficacy) || !sameFloat(result.MutationsCoverage, coverage) || len(result.Statistics) != len(statistics) {
		return errors.New("invalid result metadata")
	}
	for name, count := range statistics {
		if result.Statistics[name] != count {
			return errors.New("invalid result metadata")
		}
	}
	return nil
}

func finitePercent(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 100
}

func sameFloat(got, want float64) bool {
	return math.Abs(got-want) <= 1e-9
}

func mutatorStatistic(mutator string) (string, bool) {
	switch mutator {
	case "ARITHMETIC_BASE", "CONDITIONALS_NEGATION", "CONDITIONALS_BOUNDARY", "INCREMENT_DECREMENT", "INVERT_ASSIGNMENTS", "INVERT_BITWISE", "INVERT_BITWISE_ASSIGNMENTS", "INVERT_LOGICAL", "INVERT_LOOP_CTRL", "INVERT_NEGATIVES", "REMOVE_SELF_ASSIGNMENTS":
		return strings.ToLower(mutator), true
	default:
		return "", false
	}
}

func relativeSource(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	return clean != "." && !filepath.IsAbs(path) && clean != ".." && !strings.HasPrefix(clean, "../")
}

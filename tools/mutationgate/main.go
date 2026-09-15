package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type baselineFile struct {
	Schema   int               `json:"schema"`
	Tool     string            `json:"tool"`
	Tags     string            `json:"tags"`
	Packages []baselinePackage `json:"packages"`
}

type baselinePackage struct {
	Path         string        `json:"path"`
	Digest       string        `json:"digest"`
	Total        int           `json:"total"`
	Killed       int           `json:"killed"`
	Lived        int           `json:"lived"`
	Suppressions []suppression `json:"suppressions"`
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
	var result gremlinsResult
	if err := decode(path, &result); err != nil {
		return gremlinsResult{}, fmt.Errorf("decode results: %w", err)
	}
	return result, nil
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
	if baseline.Schema != 1 || baseline.Tool != tool || baseline.Tags != tags {
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
			id := identity(file.Filename, mutant.Line, mutant.Column, mutant.Type)
			if _, exists := actual[id]; exists {
				return fmt.Errorf("duplicate mutant %s", id)
			}
			actual[id] = mutant.Status
			counts[mutant.Status]++
		}
	}
	if counts["KILLED"]+counts["LIVED"]+counts["NOT VIABLE"] != result.MutantsTotal ||
		counts["KILLED"] != result.MutantsKilled || counts["LIVED"] != result.MutantsLived ||
		counts["NOT VIABLE"] != result.MutantsNotViable || counts["NOT COVERED"] != result.MutantsNotCovered {
		return errors.New("incomplete results: aggregate counts do not match mutant inventory")
	}
	if baselinePackage.Total != result.MutantsTotal || baselinePackage.Killed != result.MutantsKilled || baselinePackage.Lived != result.MutantsLived {
		return errors.New("mutation inventory differs from reviewed baseline")
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
		if actual[id] != "LIVED" {
			return fmt.Errorf("suppression %s does not match a lived mutant", id)
		}
	}
	if baselinePackage.Digest != resultDigest(result) {
		return errors.New("mutation inventory differs from reviewed baseline")
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

func resultDigest(result gremlinsResult) string {
	entries := make([]string, 0)
	for _, file := range result.Files {
		for _, mutant := range file.Mutations {
			if mutant.Status == "NOT COVERED" {
				continue
			}
			entries = append(entries, identity(file.Filename, mutant.Line, mutant.Column, mutant.Type)+"\t"+mutant.Status)
		}
	}
	sort.Strings(entries)
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(strings.Join(entries, "\n")+"\n")))
}

func relativeSource(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	return clean != "." && !filepath.IsAbs(path) && clean != ".." && !strings.HasPrefix(clean, "../")
}

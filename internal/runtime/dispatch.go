package runtime

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/atqamz/hand/internal/brief"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/routing"
)

type TierResult struct {
	Model               string
	Effort              string
	ExecutionClass      brief.ExecutionClass
	PlannedAgainst      string
	BriefHasFrontMatter bool
	Warnings            []string
}

func ResolveTier(homeDir, briefPath, harnessName, model, effort string) (TierResult, error) {
	declaration, frontMatter, err := brief.Parse(briefPath)
	if err != nil {
		return TierResult{}, fmt.Errorf("parse brief %s: %w", briefPath, err)
	}

	result := TierResult{
		Model:               cmp.Or(model, declaration.Model, workerDefault(homeDir, "model", harnessName)),
		Effort:              cmp.Or(effort, declaration.Effort, workerDefault(homeDir, "effort", harnessName)),
		ExecutionClass:      declaration.ExecutionClass,
		PlannedAgainst:      declaration.PlannedAgainst,
		BriefHasFrontMatter: frontMatter,
	}
	var dropped []string
	if result.Model != "" && !harness.SupportsModel(harnessName) {
		dropped = append(dropped, fmt.Sprintf("model %q", result.Model))
	}
	if result.Effort != "" && !harness.SupportsEffort(harnessName) {
		dropped = append(dropped, fmt.Sprintf("effort %q", result.Effort))
	}
	if !harness.CarriesPrompt(harnessName) {
		dropped = append(dropped, "the operator-decision rule")
		if frontMatter {
			dropped = append(dropped, "the front-matter disclaimer")
		}
		if result.ExecutionClass == brief.ExecutionClassMechanical {
			dropped = append(dropped, "the mechanical execution guidance")
		}
	}
	if len(dropped) > 0 {
		result.Warnings = []string{fmt.Sprintf("warning: harness %q cannot carry %s; launching anyway", harnessName, strings.Join(dropped, ", "))}
	}
	return result, nil
}

func classifyTierError(err error) error {
	var validationErr *brief.ValidationError
	if errors.As(err, &validationErr) {
		return Precondition(err)
	}
	return err
}

func (r *Runtime) preflightBrief(declaration brief.Declaration, clonePath string) error {
	if declaration.ExecutionClass != brief.ExecutionClassMechanical {
		return nil
	}
	if declaration.PlannedAgainst == "" {
		return Precondition(fmt.Errorf("mechanical execution class requires planned_against before dispatch"))
	}
	current, err := r.deps.projectBaseCommit(clonePath)
	if err != nil {
		return Precondition(fmt.Errorf("resolve current project base for mechanical plan: %w", err))
	}
	if declaration.PlannedAgainst != current {
		return Precondition(fmt.Errorf("mechanical plan is stale: planned against %s, current project base is %s; re-check and rewrite the brief before dispatch", declaration.PlannedAgainst, current))
	}
	return nil
}

func (r *Runtime) preflightTier(tier TierResult, clonePath string) error {
	return r.preflightBrief(brief.Declaration{
		ExecutionClass: tier.ExecutionClass,
		PlannedAgainst: tier.PlannedAgainst,
	}, clonePath)
}

func resolveExecution(homeDir, briefPath, kind, profile string, profileFromFlag bool, harnessName string, harnessFromFlag bool, model string, modelFromFlag bool, effort string, effortFromFlag bool) (routing.ResolvedRoute, error) {
	declaration, frontMatter, err := brief.Parse(briefPath)
	if err != nil {
		return routing.ResolvedRoute{}, classifyResolutionError(fmt.Errorf("parse brief %s: %w", briefPath, err), harnessName, harnessFromFlag)
	}
	config := routing.Config{}
	if declaration.ExecutionClass != "" || profileFromFlag {
		config, err = routing.Load(homeDir)
		if err != nil {
			return routing.ResolvedRoute{}, err
		}
	}
	detectedHarness := ""
	if !harnessFromFlag && !profileFromFlag {
		detected, detectErr := harness.DetectCurrent()
		if detectErr != nil {
			return routing.ResolvedRoute{}, classifyResolutionError(detectErr, harnessName, harnessFromFlag)
		}
		detectedHarness = detected.Name
	}
	legacy, err := routing.LoadLegacyDefaults(homeDir, detectedHarness)
	if err != nil {
		return routing.ResolvedRoute{}, err
	}
	resolved, err := routing.Resolve(routing.Request{
		Kind:                routing.TaskKind(kind),
		Declaration:         declaration,
		BriefHasFrontMatter: frontMatter,
		Profile:             profile,
		ProfileFromFlag:     profileFromFlag,
		Harness:             harnessName,
		HarnessFromFlag:     harnessFromFlag,
		Model:               model,
		ModelFromFlag:       modelFromFlag,
		Effort:              effort,
		EffortFromFlag:      effortFromFlag,
	}, config, legacy, routing.DefaultAvailability())
	if err != nil {
		return routing.ResolvedRoute{}, classifyResolutionError(err, harnessName, harnessFromFlag)
	}
	return resolved, nil
}

func classifyResolutionError(err error, harnessName string, harnessFromFlag bool) error {
	var validationErr *brief.ValidationError
	if errors.As(err, &validationErr) {
		return Precondition(err)
	}
	if harnessFromFlag && !harness.IsSupported(harnessName) {
		return Usage(err)
	}
	return Precondition(err)
}

func (r *Runtime) preflightExecution(route routing.ResolvedRoute, clonePath string) error {
	return r.preflightBrief(brief.Declaration{
		ExecutionClass: route.ExecutionClass,
		PlannedAgainst: route.PlannedAgainst,
	}, clonePath)
}

func workerDefault(homeDir, key, harnessName string) string {
	data, err := os.ReadFile(filepath.Join(homeDir, "config", key+"."+harnessName))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (r *Runtime) gatePreflight(projectInfo project.Project, clonePath string, skip bool) ([]string, error) {
	if projectInfo.Mode != project.ModeNoMistakes {
		return nil, nil
	}
	if skip {
		return []string{fmt.Sprintf("warning: --skip-gate-check bypassing the no-mistakes gate check for project %q", projectInfo.Name)}, nil
	}
	gateState, err := project.GateStatus(clonePath)
	if err != nil {
		return nil, fmt.Errorf("check no-mistakes gate for project %q: %w", projectInfo.Name, err)
	}
	if gateState == project.GateNotInitialized {
		return nil, Precondition(fmt.Errorf("no-mistakes gate not initialized for project %q, run: %s", projectInfo.Name, project.GateInitCommand(clonePath)))
	}
	return nil, nil
}

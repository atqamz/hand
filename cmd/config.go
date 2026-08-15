package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/atqamz/hand/internal/atomicfile"
	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/routing"
	"github.com/spf13/cobra"
)

const (
	settingHarness = "harness"
	settingModel   = "model"
	settingEffort  = "effort"
)

// In the order configuration asks for them: the harness decides which of the other two apply at all.
var workerSettingKeys = []string{settingHarness, settingModel, settingEffort}

const (
	stateConfigured    = "configured"
	stateDetected      = "detected"
	stateNativeDefault = "native-default"
	stateMissing       = "missing"
	// The selected harness takes no such launch flag, so there is nothing to configure - distinct from
	// missing, which is a question still owed an answer.
	stateUnsupported = "unsupported"
	// Applicability is a property of the harness, so it is unknown until one is chosen.
	statePendingHarness = "pending-harness"
)

type workerSetting struct {
	key   string
	state string
	value string
}

var workerSettingFields = []axi.Column[workerSetting]{
	{Name: "key", Value: func(s workerSetting) string { return s.key }},
	{Name: "state", Value: func(s workerSetting) string { return s.state }},
	{Name: "value", Value: func(s workerSetting) string { return orNone(s.value) }},
}

// Every capability column is read off internal/harness rather than restated here: a second table that
// claims to know which harness takes a model flag is one that can disagree with the launch command.
var harnessFields = []axi.Column[string]{
	{Name: "name", Value: func(name string) string { return name }},
	{Name: "installed", Value: func(name string) string { return strconv.FormatBool(onPath(name)) }},
	{Name: "model", Value: func(name string) string { return strconv.FormatBool(harness.SupportsModel(name)) }},
	{Name: "effort", Value: func(name string) string { return strconv.FormatBool(harness.SupportsEffort(name)) }},
}

var profileFields = []axi.Column[routing.Profile]{
	{Name: "name", Value: func(profile routing.Profile) string { return profile.Name }},
	{Name: "harness", Value: func(profile routing.Profile) string { return profile.Harness }},
	{Name: "model", Value: func(profile routing.Profile) string { return orNone(profile.Model) }},
	{Name: "effort", Value: func(profile routing.Profile) string { return orNone(profile.Effort) }},
}

type routeCell struct {
	kind           routing.TaskKind
	executionClass routing.ExecutionClass
	profile        string
	state          string
}

var routeCellFields = []axi.Column[routeCell]{
	{Name: "kind", Value: func(route routeCell) string { return string(route.kind) }},
	{Name: "execution_class", Value: func(route routeCell) string { return string(route.executionClass) }},
	{Name: "profile", Value: func(route routeCell) string { return orNone(route.profile) }},
	{Name: "state", Value: func(route routeCell) string { return route.state }},
}

type configProblem struct {
	code           string
	kind           string
	executionClass string
	profile        string
	message        string
}

var configProblemFields = []axi.Column[configProblem]{
	{Name: "code", Value: func(problem configProblem) string { return problem.code }},
	{Name: "kind", Value: func(problem configProblem) string { return orNone(problem.kind) }},
	{Name: "execution_class", Value: func(problem configProblem) string { return orNone(problem.executionClass) }},
	{Name: "profile", Value: func(problem configProblem) string { return orNone(problem.profile) }},
	{Name: "message", Value: func(problem configProblem) string { return problem.message }},
}

type workerConfig struct {
	detection harness.Detection
	harness   string
	settings  []workerSetting
	legacy    routing.LegacyDefaults
}

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Report effective worker defaults and optional overrides",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			fleetHome, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}
			detection, err := harness.DetectCurrent()
			if err != nil {
				return err
			}
			snapshot, err := routing.LoadExecutionSnapshot(fleetHome, detection.Name, true)
			if err != nil {
				return err
			}
			cfg := workerConfigFromLegacy(detection, snapshot.Legacy)
			problems := configurationProblems(cfg, snapshot.Config)
			detected := orNone(cfg.detection.Name)
			detectionSource := orNone(cfg.detection.Source)

			var doc axi.Doc
			doc.Field("home", fleetHome)
			doc.Field("supervisor_harness", detected)
			doc.Field("supervisor_harness_source", detectionSource)
			doc.Field("harness", orNone(cfg.harness))
			appendWorkerConfig(&doc, cfg)
			axi.Table(&doc, "harnesses", harness.Names(), harnessFields)
			axi.Table(&doc, "profiles", snapshot.Config.Profiles, profileFields)
			axi.Table(&doc, "routes", routeCells(snapshot.Config), routeCellFields)
			axi.Table(&doc, "problems", problems, configProblemFields)
			doc.Help(workerConfigHelp(cfg)...)
			return doc.Render(cmd.OutOrStdout())
		},
	}
	cmd.AddCommand(newConfigSetCmd(), newConfigProfileCmd(), newConfigRouteCmd())
	return cmd
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Validate and persist one worker default",
		Args:  usageArgs(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := args[0], args[1]
			fleetHome, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}
			detected, err := harness.DetectCurrent()
			if err != nil {
				return err
			}
			release, err := routing.Lock(fleetHome)
			if err != nil {
				return err
			}
			cfg := readWorkerConfig(fleetHome, detected)
			rel, err := writeWorkerSettingLocked(fleetHome, key, value, cfg.harness)
			if err != nil {
				release()
				return err
			}
			cfg = readWorkerConfig(fleetHome, cfg.detection)
			release()

			var doc axi.Doc
			doc.Field("result", "set")
			doc.Field("home", fleetHome)
			doc.Field("key", key)
			doc.Field("value", value)
			doc.Field("file", rel)
			doc.Field("harness", orNone(cfg.harness))
			appendWorkerConfig(&doc, cfg)
			help := workerConfigHelp(cfg)
			if len(help) == 0 {
				help = append(help, "Every applicable worker default is configured; run `hand project add <repo-url>` to register a project")
			}
			doc.Help(help...)
			return doc.Render(cmd.OutOrStdout())
		},
	}
}

func newConfigProfileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Manage operator-defined execution profiles",
	}
	cmd.AddCommand(newConfigProfileListCmd(), newConfigProfileSetCmd(), newConfigProfileRemoveCmd())
	return cmd
}

func newConfigProfileListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured execution profiles",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			fleetHome, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}
			profiles, err := routing.ListProfiles(fleetHome)
			if err != nil {
				return err
			}
			var doc axi.Doc
			doc.Field("home", fleetHome)
			axi.Table(&doc, "profiles", profiles, profileFields)
			return doc.Render(cmd.OutOrStdout())
		},
	}
}

func newConfigProfileSetCmd() *cobra.Command {
	var harnessName, model, effort string
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Replace an execution profile",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("harness") {
				return &ExitError{Err: fmt.Errorf("required flag(s) \"harness\" not set"), Code: 2}
			}
			profile := routing.Profile{Name: args[0], Harness: harnessName, Model: model, Effort: effort}
			if err := routing.ValidateProfile(profile); err != nil {
				return &ExitError{Err: err, Code: 2}
			}
			fleetHome, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}
			if err := routing.WriteProfile(fleetHome, profile); err != nil {
				return err
			}
			var doc axi.Doc
			doc.Field("result", "set")
			doc.Field("home", fleetHome)
			doc.Field("profile", profile.Name)
			doc.Field("harness", profile.Harness)
			doc.Field("model", orNone(profile.Model))
			doc.Field("effort", orNone(profile.Effort))
			return doc.Render(cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&harnessName, "harness", "", "supported harness for this profile")
	cmd.Flags().StringVar(&model, "model", "", "model identifier for harnesses that support it")
	cmd.Flags().StringVar(&effort, "effort", "", "effort level for harnesses that support it")
	return cmd
}

func newConfigProfileRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove an unreferenced execution profile",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := routing.ValidateProfileName(args[0]); err != nil {
				return &ExitError{Err: err, Code: 2}
			}
			fleetHome, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}
			if err := routing.RemoveProfile(fleetHome, args[0]); err != nil {
				return &ExitError{Err: err, Code: 3}
			}
			var doc axi.Doc
			doc.Field("result", "removed")
			doc.Field("home", fleetHome)
			doc.Field("profile", args[0])
			return doc.Render(cmd.OutOrStdout())
		},
	}
}

func newConfigRouteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "route",
		Short: "Manage execution profile routes",
	}
	cmd.AddCommand(newConfigRouteListCmd(), newConfigRouteSetCmd(), newConfigRouteRemoveCmd())
	return cmd
}

func newConfigRouteListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured execution profile routes",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			fleetHome, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}
			routes, err := routing.Load(fleetHome)
			if err != nil {
				return err
			}
			var doc axi.Doc
			doc.Field("home", fleetHome)
			axi.Table(&doc, "routes", routeCells(routes), routeCellFields[:3])
			return doc.Render(cmd.OutOrStdout())
		},
	}
}

func newConfigRouteSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <kind> <execution-class> <profile>",
		Short: "Set one execution profile route",
		Args:  usageArgs(cobra.ExactArgs(3)),
		RunE: func(cmd *cobra.Command, args []string) error {
			route := routing.Route{Kind: routing.TaskKind(args[0]), ExecutionClass: routing.ExecutionClass(args[1]), Profile: args[2]}
			if err := routing.ValidateRoute(route); err != nil {
				return &ExitError{Err: err, Code: 2}
			}
			fleetHome, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}
			if err := routing.WriteRoute(fleetHome, route); err != nil {
				return &ExitError{Err: err, Code: 3}
			}
			var doc axi.Doc
			doc.Field("result", "set")
			doc.Field("home", fleetHome)
			doc.Field("kind", string(route.Kind))
			doc.Field("execution_class", string(route.ExecutionClass))
			doc.Field("profile", route.Profile)
			return doc.Render(cmd.OutOrStdout())
		},
	}
}

func newConfigRouteRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <kind> <execution-class>",
		Short: "Remove one execution profile route",
		Args:  usageArgs(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind := routing.TaskKind(args[0])
			class := routing.ExecutionClass(args[1])
			if err := routing.ValidateTaskKind(kind); err != nil {
				return &ExitError{Err: err, Code: 2}
			}
			if err := routing.ValidateExecutionClass(class); err != nil {
				return &ExitError{Err: err, Code: 2}
			}
			fleetHome, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}
			if err := routing.RemoveRoute(fleetHome, kind, class); err != nil {
				return &ExitError{Err: err, Code: 3}
			}
			var doc axi.Doc
			doc.Field("result", "removed")
			doc.Field("home", fleetHome)
			doc.Field("kind", string(kind))
			doc.Field("execution_class", string(class))
			return doc.Render(cmd.OutOrStdout())
		},
	}
}

// The report the session hook and `hand config` both render, so a supervisor rechecking after an answer
// reads the same shape it read at session start.
func appendWorkerConfig(doc *axi.Doc, cfg workerConfig) {
	doc.Int("config_missing", configMissing(cfg))
	axi.Table(doc, "config", cfg.settings, workerSettingFields)
}

func configMissing(cfg workerConfig) int {
	missing := 0
	for _, s := range cfg.settings {
		if s.state == stateMissing {
			missing++
		}
	}
	return missing
}

func routeCells(config routing.Config) []routeCell {
	routes := make(map[string]routing.Route, len(config.Routes))
	for _, route := range config.Routes {
		routes[routeKey(route.Kind, route.ExecutionClass)] = route
	}
	malformed := make(map[string]bool)
	for _, problem := range config.Problems {
		if problem.Code == routing.ConfigProblemMalformedRoute {
			malformed[routeKey(problem.Kind, problem.ExecutionClass)] = true
		}
	}

	cells := make([]routeCell, 0, len(routing.TaskKinds())*len(routing.ExecutionClasses()))
	for _, kind := range routing.TaskKinds() {
		for _, class := range routing.ExecutionClasses() {
			cell := routeCell{kind: kind, executionClass: class, state: stateMissing}
			if route, found := routes[routeKey(kind, class)]; found {
				cell.profile = route.Profile
				cell.state = stateConfigured
			} else if malformed[routeKey(kind, class)] {
				cell.state = "malformed"
			}
			cells = append(cells, cell)
		}
	}
	return cells
}

func routeKey(kind routing.TaskKind, class routing.ExecutionClass) string {
	return string(kind) + "." + string(class)
}

func configurationProblems(cfg workerConfig, config routing.Config) []configProblem {
	problems := make([]configProblem, 0, len(config.Problems)+len(config.Profiles)+3)
	for _, problem := range config.Problems {
		problems = append(problems, configProblem{
			code:           string(problem.Code),
			kind:           string(problem.Kind),
			executionClass: string(problem.ExecutionClass),
			profile:        problem.Profile,
			message:        problem.Message,
		})
	}
	if cfg.harness != "" {
		if !harness.IsSupported(cfg.harness) {
			problems = append(problems, configProblem{
				code:    string(routing.ConfigProblemUnsupportedHarness),
				message: fmt.Sprintf("legacy harness %q is not recognized", cfg.harness),
			})
		} else {
			if !onPath(cfg.harness) {
				problems = append(problems, pathProblem("", cfg.harness))
			}
			for _, setting := range []struct {
				key       string
				supported bool
				code      routing.ConfigProblemCode
			}{
				{key: settingModel, supported: harness.SupportsModel(cfg.harness), code: routing.ConfigProblemUnsupportedModel},
				{key: settingEffort, supported: harness.SupportsEffort(cfg.harness), code: routing.ConfigProblemUnsupportedEffort},
			} {
				value := cfg.legacy.Models[cfg.harness]
				if setting.key == settingEffort {
					value = cfg.legacy.Efforts[cfg.harness]
				}
				if setting.supported || value == "" {
					continue
				}
				problems = append(problems, configProblem{
					code:    string(setting.code),
					message: fmt.Sprintf("legacy harness %q takes no %s", cfg.harness, setting.key),
				})
			}
		}
	}
	for _, profile := range config.Profiles {
		if !onPath(profile.Harness) {
			problems = append(problems, pathProblem(profile.Name, profile.Harness))
		}
	}
	slices.SortFunc(problems, func(a, b configProblem) int {
		for _, pair := range [][2]string{
			{a.code, b.code},
			{a.kind, b.kind},
			{a.executionClass, b.executionClass},
			{a.profile, b.profile},
			{a.message, b.message},
		} {
			if comparison := strings.Compare(pair[0], pair[1]); comparison != 0 {
				return comparison
			}
		}
		return 0
	})
	return problems
}

func pathProblem(profile, harnessName string) configProblem {
	message := fmt.Sprintf("harness %q is not installed on PATH", harnessName)
	if profile != "" {
		message = fmt.Sprintf("profile %q selects a harness that is not installed on PATH: %q", profile, harnessName)
	}
	return configProblem{code: "path-unavailable", profile: profile, message: message}
}

// An unknown supervisor is the only decision that blocks effective defaults; model and effort can
// stay with the harness's native selection until an operator chooses an override.
func workerConfigHelp(cfg workerConfig) []string {
	if cfg.harness != "" {
		return nil
	}
	return []string{"Ask the operator which harness this fleet's workers should default to, then run `hand config set harness <name>`; `hand config` lists the supported ones and which are installed"}
}

func currentWorkerConfig(fleetHome string) (workerConfig, error) {
	detected, err := harness.DetectCurrent()
	if err != nil {
		return workerConfig{}, err
	}
	release, err := routing.Lock(fleetHome)
	if err != nil {
		return workerConfig{}, err
	}
	defer release()
	return readWorkerConfig(fleetHome, detected), nil
}

// Effective model and effort overrides are read from files keyed to the selected harness, never from a
// bare config/model: a value chosen for one harness is not a default for the next one.
func readWorkerConfig(fleetHome string, detected harness.Detection) workerConfig {
	configured := configDefault(fleetHome, settingHarness, "")
	legacy := routing.LegacyDefaults{
		Harness:           configured,
		ConfiguredHarness: configured,
		Models:            make(map[string]string),
		Efforts:           make(map[string]string),
	}
	if legacy.Harness == "" {
		legacy.Harness = detected.Name
	}
	for _, name := range harness.Names() {
		legacy.Models[name] = configDefault(fleetHome, harnessSettingKey(settingModel, name), "")
		legacy.Efforts[name] = configDefault(fleetHome, harnessSettingKey(settingEffort, name), "")
	}
	return workerConfigFromLegacy(detected, legacy)
}

func workerConfigFromLegacy(detected harness.Detection, legacy routing.LegacyDefaults) workerConfig {
	harnessName := legacy.Harness
	if legacy.ConfiguredHarness == "" && !harness.IsSupported(detected.Name) {
		harnessName = ""
	}
	cfg := workerConfig{detection: detected, harness: harnessName, legacy: legacy}
	harnessState := stateMissing
	if legacy.ConfiguredHarness != "" {
		harnessState = stateConfigured
	} else if harness.IsSupported(detected.Name) {
		harnessState = stateDetected
	}
	cfg.settings = []workerSetting{{key: settingHarness, state: harnessState, value: cfg.harness}}
	for _, key := range []string{settingModel, settingEffort} {
		s := workerSetting{key: key}
		switch {
		case cfg.harness == "":
			s.state = statePendingHarness
		case !harnessCarries(key, cfg.harness):
			s.state = stateUnsupported
		default:
			if key == settingModel {
				s.value = legacy.Models[cfg.harness]
			} else {
				s.value = legacy.Efforts[cfg.harness]
			}
			s.state = stateConfigured
			if s.value == "" {
				s.state = stateNativeDefault
			}
		}
		cfg.settings = append(cfg.settings, s)
	}
	return cfg
}

func harnessSettingKey(key, harnessName string) string {
	return key + "." + harnessName
}

func harnessCarries(key, harnessName string) bool {
	if key == settingEffort {
		return harness.SupportsEffort(harnessName)
	}
	return harness.SupportsModel(harnessName)
}

func onPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// Harness names are validated against internal/harness, and effort and model are not: hand knows which
// launch flags a harness takes, and a model identifier belongs to the harness's own catalog, which a
// release of hand cannot keep up with.
func writeWorkerSettingLocked(fleetHome, key, value, currentHarness string) (string, error) {
	if !slices.Contains(workerSettingKeys, key) {
		return "", &ExitError{Err: fmt.Errorf("unknown setting %q: want one of %s", key, strings.Join(workerSettingKeys, ", ")), Code: 2}
	}
	if value != strings.TrimSpace(value) || len(strings.Fields(value)) != 1 {
		return "", &ExitError{Err: fmt.Errorf("%s value %q must be one word with no surrounding whitespace", key, value), Code: 2}
	}

	name := key
	if key == settingHarness {
		if !harness.IsSupported(value) {
			return "", &ExitError{Err: fmt.Errorf("harness %q not recognized: want one of %s", value, strings.Join(harness.Names(), ", ")), Code: 2}
		}
	} else {
		if currentHarness == "" {
			return "", &ExitError{Err: fmt.Errorf("current supervisor harness is unknown and no worker harness override is configured; run hand config set harness <name> before setting %s", key), Code: 3}
		}
		if !harnessCarries(key, currentHarness) {
			return "", &ExitError{Err: fmt.Errorf("harness %q takes no %s, so there is nothing to configure", currentHarness, key), Code: 2}
		}
		name = harnessSettingKey(key, currentHarness)
	}

	dir := filepath.Join(fleetHome, "config")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create config: %w", err)
	}
	if err := atomicfile.Write(filepath.Join(dir, name), ".config-", []byte(value+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("write config/%s: %w", name, err)
	}
	return filepath.Join("config", name), nil
}

// Moves a bare config/model or config/effort under the harness it was written for, and reports which keys
// moved. Left unkeyed, that value would become the default for whatever harness the home switches to
// next, which is how a claude model identifier reaches an opencode worker.
func migrateWorkerSettings(fleetHome string) ([]string, error) {
	release, err := routing.Lock(fleetHome)
	if err != nil {
		return nil, err
	}
	defer release()
	return migrateWorkerSettingsLocked(fleetHome)
}

func migrateWorkerSettingsLocked(fleetHome string) ([]string, error) {
	harnessName := configDefault(fleetHome, settingHarness, "")
	if harnessName == "" {
		return nil, nil
	}
	var moved []string
	var errs []error
	for _, key := range []string{settingModel, settingEffort} {
		unkeyed := filepath.Join(fleetHome, "config", key)
		if _, err := os.Stat(unkeyed); err != nil {
			continue
		}
		keyed := filepath.Join(fleetHome, "config", harnessSettingKey(key, harnessName))
		if _, err := os.Stat(keyed); err == nil {
			continue
		}
		if err := os.Rename(unkeyed, keyed); err != nil {
			errs = append(errs, fmt.Errorf("move config/%s under %s: %w", key, harnessName, err))
			continue
		}
		moved = append(moved, key)
	}
	return moved, errors.Join(errs...)
}

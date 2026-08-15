package routing

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/atqamz/hand/internal/atomicfile"
)

const (
	configDirectory   = "config"
	profilesDirectory = "profiles"
	routesDirectory   = "routes"
)

func Load(home string) (Config, error) {
	var config Config
	err := withLock(home, func() error {
		var err error
		config, err = loadConfig(home)
		return err
	})
	return config, err
}

func loadConfig(home string) (Config, error) {
	profiles, problems, err := loadProfiles(home)
	if err != nil {
		return Config{}, err
	}

	config := Config{Profiles: profiles, Problems: problems}
	profileNames := make(map[string]bool, len(profiles))
	for _, profile := range profiles {
		profileNames[profile.Name] = true
	}
	for _, kind := range TaskKinds() {
		for _, class := range ExecutionClasses() {
			route, found, err := loadRoute(home, kind, class)
			if err != nil {
				config.Problems = append(config.Problems, ConfigProblem{
					Code:           ConfigProblemMalformedRoute,
					Kind:           kind,
					ExecutionClass: class,
					Message:        err.Error(),
				})
				continue
			}
			if !found {
				config.Problems = append(config.Problems, ConfigProblem{
					Code:           ConfigProblemMissingRoute,
					Kind:           kind,
					ExecutionClass: class,
					Message:        "route is not configured",
				})
				continue
			}
			config.Routes = append(config.Routes, route)
			if !profileNames[route.Profile] {
				config.Problems = append(config.Problems, ConfigProblem{
					Code:           ConfigProblemDanglingRoute,
					Kind:           kind,
					ExecutionClass: class,
					Profile:        route.Profile,
					Message:        fmt.Sprintf("profile %q does not exist or is invalid", route.Profile),
				})
			}
		}
	}
	return config, nil
}

func ListProfiles(home string) ([]Profile, error) {
	config, err := Load(home)
	if err != nil {
		return nil, err
	}
	return slices.Clone(config.Profiles), nil
}

func ListRoutes(home string) ([]Route, error) {
	config, err := Load(home)
	if err != nil {
		return nil, err
	}
	return slices.Clone(config.Routes), nil
}

func WriteProfile(home string, profile Profile) error {
	return writeProfileWithHook(home, profile, nil)
}

type profileWritePhase string

const (
	profilePhaseStagingCreated     profileWritePhase = "staging-created"
	profilePhaseHarnessWritten     profileWritePhase = "harness-written"
	profilePhaseModelWritten       profileWritePhase = "model-written"
	profilePhaseEffortWritten      profileWritePhase = "effort-written"
	profilePhaseGenerationComplete profileWritePhase = "generation-complete"
	profilePhasePublished          profileWritePhase = "published"
)

func writeProfileWithHook(home string, profile Profile, hook func(profileWritePhase) error) error {
	return withLock(home, func() error {
		return writeProfile(home, profile, hook)
	})
}

// A profile generation is immutable after its directory is published.
// The current pointer becomes authoritative only after every required field is complete.
func writeProfile(home string, profile Profile, hook func(profileWritePhase) error) error {
	if err := ValidateProfile(profile); err != nil {
		return err
	}
	dir, err := prepareProfileDirectory(home, profile.Name)
	if err != nil {
		return err
	}
	generations := filepath.Join(dir, "generations")
	staging, err := os.MkdirTemp(generations, ".staging-")
	if err != nil {
		return fmt.Errorf("create profile staging directory: %w", err)
	}
	if err := os.Chmod(staging, 0o755); err != nil {
		return fmt.Errorf("set profile staging directory permissions: %w", err)
	}
	if err := runProfileWriteHook(hook, profilePhaseStagingCreated); err != nil {
		return err
	}
	if err := writeConfigValue(filepath.Join(staging, "harness"), profile.Harness); err != nil {
		return fmt.Errorf("write profile harness: %w", err)
	}
	if err := runProfileWriteHook(hook, profilePhaseHarnessWritten); err != nil {
		return err
	}
	for _, setting := range []struct {
		name  string
		value string
	}{
		{name: "model", value: profile.Model},
		{name: "effort", value: profile.Effort},
	} {
		if setting.value != "" {
			if err := writeConfigValue(filepath.Join(staging, setting.name), setting.value); err != nil {
				return fmt.Errorf("write profile %s: %w", setting.name, err)
			}
		}
		phase := profilePhaseModelWritten
		if setting.name == "effort" {
			phase = profilePhaseEffortWritten
		}
		if err := runProfileWriteHook(hook, phase); err != nil {
			return err
		}
	}
	generationID := "generation-" + strings.TrimPrefix(filepath.Base(staging), ".staging-")
	generation, err := generationDirectory(dir, generationID)
	if err != nil {
		return err
	}
	if err := os.Rename(staging, generation); err != nil {
		return fmt.Errorf("publish complete profile generation: %w", err)
	}
	if err := runProfileWriteHook(hook, profilePhaseGenerationComplete); err != nil {
		return err
	}
	if err := writeConfigValue(filepath.Join(dir, "current"), generationID); err != nil {
		return fmt.Errorf("publish profile generation pointer: %w", err)
	}
	if err := runProfileWriteHook(hook, profilePhasePublished); err != nil {
		return err
	}
	if err := writeProfileMirror(dir, profile); err != nil {
		return err
	}
	_ = cleanupProfileGenerations(dir, generationID)
	return nil
}

func RemoveProfile(home, name string) error {
	return removeProfileWithHook(home, name, nil)
}

type configMutationPhase string

const (
	configPhaseMutationStarted          configMutationPhase = "mutation-started"
	configPhaseRouteProfileValidated    configMutationPhase = "route-profile-validated"
	configPhaseProfileReferencesChecked configMutationPhase = "profile-references-checked"
)

func removeProfileWithHook(home, name string, hook func(configMutationPhase) error) error {
	if err := runConfigMutationHook(hook, configPhaseMutationStarted); err != nil {
		return err
	}
	return withLock(home, func() error {
		return removeProfile(home, name, hook)
	})
}

func removeProfile(home, name string, hook func(configMutationPhase) error) error {
	if err := ValidateProfileName(name); err != nil {
		return err
	}
	config, err := loadConfig(home)
	if err != nil {
		return err
	}
	var reference *Route
	for i := range config.Routes {
		if config.Routes[i].Profile == name {
			reference = &config.Routes[i]
			break
		}
	}
	if err := runConfigMutationHook(hook, configPhaseProfileReferencesChecked); err != nil {
		return err
	}
	if reference != nil {
		return fmt.Errorf("profile %q is still referenced by %s.%s", name, reference.Kind, reference.ExecutionClass)
	}
	dir, err := profileDirectory(home, name)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("profile %q is not configured", name)
		}
		return fmt.Errorf("inspect profile %q: %w", name, err)
	}
	if err := removeOwnedDirectory(dir); err != nil {
		return fmt.Errorf("remove profile %q: %w", name, err)
	}
	return nil
}

func WriteRoute(home string, route Route) error {
	return writeRouteWithHook(home, route, nil)
}

func writeRouteWithHook(home string, route Route, hook func(configMutationPhase) error) error {
	if err := runConfigMutationHook(hook, configPhaseMutationStarted); err != nil {
		return err
	}
	return withLock(home, func() error {
		return writeRoute(home, route, hook)
	})
}

func writeRoute(home string, route Route, hook func(configMutationPhase) error) error {
	if err := ValidateRoute(route); err != nil {
		return err
	}
	profiles, _, err := loadProfiles(home)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(profiles, func(profile Profile) bool { return profile.Name == route.Profile }) {
		return fmt.Errorf("route profile %q is not configured", route.Profile)
	}
	if err := runConfigMutationHook(hook, configPhaseRouteProfileValidated); err != nil {
		return err
	}
	path, err := routeFile(home, route.Kind, route.ExecutionClass)
	if err != nil {
		return err
	}
	if err := prepareRoutesDirectory(home); err != nil {
		return err
	}
	if err := writeConfigValue(path, route.Profile); err != nil {
		return fmt.Errorf("write route %s.%s: %w", route.Kind, route.ExecutionClass, err)
	}
	return nil
}

func RemoveRoute(home string, kind TaskKind, class ExecutionClass) error {
	return removeRouteWithHook(home, kind, class, nil)
}

func removeRouteWithHook(home string, kind TaskKind, class ExecutionClass, hook func(configMutationPhase) error) error {
	if err := runConfigMutationHook(hook, configPhaseMutationStarted); err != nil {
		return err
	}
	return withLock(home, func() error {
		return removeRoute(home, kind, class)
	})
}

func removeRoute(home string, kind TaskKind, class ExecutionClass) error {
	if err := validateRouteCell(kind, class); err != nil {
		return err
	}
	if err := inspectOptionalDirectory(filepath.Join(home, configDirectory)); err != nil {
		return fmt.Errorf("inspect routes directory: %w", err)
	}
	if err := inspectOptionalDirectory(filepath.Join(home, configDirectory, routesDirectory)); err != nil {
		return fmt.Errorf("inspect routes directory: %w", err)
	}
	path, err := routeFile(home, kind, class)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("route %s.%s is not configured", kind, class)
		}
		return fmt.Errorf("remove route %s.%s: %w", kind, class, err)
	}
	return nil
}

func loadProfiles(home string) ([]Profile, []ConfigProblem, error) {
	if err := inspectOptionalDirectory(filepath.Join(home, configDirectory)); err != nil {
		return nil, nil, fmt.Errorf("inspect profiles directory: %w", err)
	}
	dir := filepath.Join(home, configDirectory, profilesDirectory)
	if err := inspectOptionalDirectory(dir); err != nil {
		return nil, nil, fmt.Errorf("inspect profiles directory: %w", err)
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read profiles directory: %w", err)
	}

	profiles := make([]Profile, 0, len(entries))
	var problems []ConfigProblem
	for _, entry := range entries {
		info, err := os.Lstat(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, nil, fmt.Errorf("inspect profile %q: %w", entry.Name(), err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			problems = append(problems, ConfigProblem{
				Code:    ConfigProblemMalformedProfile,
				Profile: entry.Name(),
				Message: "profile directory must not be a symlink",
			})
			continue
		}
		if !info.IsDir() {
			continue
		}
		profile, err := loadProfile(home, entry.Name())
		if err != nil {
			problems = append(problems, ConfigProblem{
				Code:    profileProblemCode(err),
				Profile: entry.Name(),
				Message: err.Error(),
			})
			continue
		}
		profiles = append(profiles, profile)
	}
	slices.SortFunc(profiles, func(a, b Profile) int { return strings.Compare(a.Name, b.Name) })
	return profiles, problems, nil
}

func profileProblemCode(err error) ConfigProblemCode {
	var validationErr *profileValidationError
	if errors.As(err, &validationErr) {
		return validationErr.code
	}
	return ConfigProblemMalformedProfile
}

func loadProfile(home, name string) (Profile, error) {
	dir, err := profileDirectory(home, name)
	if err != nil {
		return Profile{}, err
	}
	dataDir, err := profileDataDirectory(dir)
	if err != nil {
		return Profile{}, err
	}
	harnessName, found, err := readConfigValue(filepath.Join(dataDir, "harness"))
	if err != nil {
		return Profile{}, fmt.Errorf("read harness: %w", err)
	}
	if !found {
		return Profile{}, fmt.Errorf("profile harness is required")
	}
	model, modelSet, err := readConfigValue(filepath.Join(dataDir, "model"))
	if err != nil {
		return Profile{}, fmt.Errorf("read model: %w", err)
	}
	effort, effortSet, err := readConfigValue(filepath.Join(dataDir, "effort"))
	if err != nil {
		return Profile{}, fmt.Errorf("read effort: %w", err)
	}
	if modelSet && model == "" {
		return Profile{}, fmt.Errorf("profile model is empty")
	}
	if effortSet && effort == "" {
		return Profile{}, fmt.Errorf("profile effort is empty")
	}
	profile := Profile{Name: name, Harness: harnessName, Model: model, Effort: effort}
	if err := ValidateProfile(profile); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func loadRoute(home string, kind TaskKind, class ExecutionClass) (Route, bool, error) {
	if err := inspectOptionalDirectory(filepath.Join(home, configDirectory)); err != nil {
		return Route{}, false, err
	}
	if err := inspectOptionalDirectory(filepath.Join(home, configDirectory, routesDirectory)); err != nil {
		return Route{}, false, err
	}
	path, err := routeFile(home, kind, class)
	if err != nil {
		return Route{}, false, err
	}
	profile, found, err := readConfigValue(path)
	if err != nil {
		return Route{}, false, err
	}
	if !found {
		return Route{}, false, nil
	}
	route := Route{Kind: kind, ExecutionClass: class, Profile: profile}
	if err := ValidateRoute(route); err != nil {
		return Route{}, false, err
	}
	return route, true, nil
}

func readConfigValue(path string) (string, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("must be a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	value := strings.TrimSuffix(string(data), "\n")
	if strings.ContainsAny(value, "\r\n") {
		return "", false, fmt.Errorf("must contain one line")
	}
	return value, true, nil
}

func writeConfigValue(path, value string) error {
	return atomicfile.Write(path, ".config-", []byte(value+"\n"), 0o644)
}

func writeProfileMirror(dir string, profile Profile) error {
	// Direct fields remain for pre-generation checkouts; current-generation readers ignore them.
	if err := writeConfigValue(filepath.Join(dir, "harness"), profile.Harness); err != nil {
		return fmt.Errorf("update profile harness mirror: %w", err)
	}
	for _, setting := range []struct {
		name  string
		value string
	}{
		{name: "model", value: profile.Model},
		{name: "effort", value: profile.Effort},
	} {
		path := filepath.Join(dir, setting.name)
		if setting.value != "" {
			if err := writeConfigValue(path, setting.value); err != nil {
				return fmt.Errorf("update profile %s mirror: %w", setting.name, err)
			}
			continue
		}
		if err := removeConfigValue(path); err != nil {
			return fmt.Errorf("remove profile %s mirror: %w", setting.name, err)
		}
	}
	return nil
}

func removeConfigValue(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("must be a regular file")
	}
	return os.Remove(path)
}

func profileDataDirectory(dir string) (string, error) {
	generationID, found, err := readConfigValue(filepath.Join(dir, "current"))
	if err != nil {
		return "", fmt.Errorf("read active profile generation: %w", err)
	}
	if !found {
		return dir, nil
	}
	if err := validateGenerationID(generationID); err != nil {
		return "", fmt.Errorf("active profile generation: %w", err)
	}
	generations := filepath.Join(dir, "generations")
	if err := inspectRequiredDirectory(generations, "profile generations directory"); err != nil {
		return "", err
	}
	generation, err := generationDirectory(dir, generationID)
	if err != nil {
		return "", err
	}
	if err := inspectRequiredDirectory(generation, "active profile generation"); err != nil {
		return "", err
	}
	return generation, nil
}

func generationDirectory(profileDir, generationID string) (string, error) {
	if err := validateGenerationID(generationID); err != nil {
		return "", err
	}
	root := filepath.Join(profileDir, "generations")
	path := filepath.Join(root, generationID)
	if !within(root, path) {
		return "", fmt.Errorf("profile generation %q escapes generations directory", generationID)
	}
	return path, nil
}

func cleanupProfileGenerations(profileDir, activeID string) error {
	generations := filepath.Join(profileDir, "generations")
	if err := inspectRequiredDirectory(generations, "profile generations directory"); err != nil {
		return err
	}
	entries, err := os.ReadDir(generations)
	if err != nil {
		return fmt.Errorf("read profile generations directory: %w", err)
	}
	var firstErr error
	for _, entry := range entries {
		name := entry.Name()
		if name == activeID || !ownedGenerationName(name) {
			continue
		}
		path := filepath.Join(generations, name)
		if err := removeOwnedDirectory(path); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("remove inactive profile generation %q: %w", name, err)
		}
	}
	return firstErr
}

func ownedGenerationName(name string) bool {
	if strings.HasPrefix(name, ".staging-") {
		return true
	}
	return strings.HasPrefix(name, "generation-") && validateGenerationID(name) == nil
}

func validateGenerationID(id string) error {
	if strings.HasPrefix(id, ".staging-") {
		return fmt.Errorf("generation identifier %q is not valid", id)
	}
	if err := ValidateProfileName(id); err != nil {
		return fmt.Errorf("generation identifier %q is not filename-safe: %w", id, err)
	}
	return nil
}

func runProfileWriteHook(hook func(profileWritePhase) error, phase profileWritePhase) error {
	if hook == nil {
		return nil
	}
	return hook(phase)
}

func runConfigMutationHook(hook func(configMutationPhase) error, phase configMutationPhase) error {
	if hook == nil {
		return nil
	}
	return hook(phase)
}

func inspectOptionalDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("directory %s must not be a symlink", path)
	}
	return nil
}

func inspectRequiredDirectory(path, label string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s is missing", label)
	}
	if err != nil {
		return fmt.Errorf("inspect %s: %w", label, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%s must be a real directory", label)
	}
	return nil
}

func prepareRoutesDirectory(home string) error {
	configDir := filepath.Join(home, configDirectory)
	routesDir := filepath.Join(configDir, routesDirectory)
	for _, path := range []string{configDir, routesDir} {
		if err := ensureDirectory(path); err != nil {
			return err
		}
	}
	return nil
}

func removeOwnedDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("must not be a symlink")
	}
	if !info.IsDir() {
		return fmt.Errorf("must be a directory")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		child := filepath.Join(path, entry.Name())
		childInfo, err := os.Lstat(child)
		if err != nil {
			return err
		}
		if childInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s must not contain symlinks", path)
		}
		if childInfo.IsDir() {
			if err := removeOwnedDirectory(child); err != nil {
				return err
			}
			continue
		}
		if err := os.Remove(child); err != nil {
			return err
		}
	}
	return os.Remove(path)
}

func profileDirectory(home, name string) (string, error) {
	if err := ValidateProfileName(name); err != nil {
		return "", err
	}
	root := filepath.Join(home, configDirectory, profilesDirectory)
	path := filepath.Join(root, name)
	if !within(root, path) {
		return "", fmt.Errorf("profile name %q escapes profiles directory", name)
	}
	return path, nil
}

func prepareProfileDirectory(home, name string) (string, error) {
	dir, err := profileDirectory(home, name)
	if err != nil {
		return "", err
	}
	root := filepath.Join(home, configDirectory, profilesDirectory)
	for _, path := range []string{
		filepath.Join(home, configDirectory),
		root,
		dir,
		filepath.Join(dir, "generations"),
	} {
		if err := ensureDirectory(path); err != nil {
			return "", err
		}
	}
	if !within(root, dir) {
		return "", fmt.Errorf("profile name %q escapes profiles directory", name)
	}
	return dir, nil
}

func ensureDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, 0o755); err != nil {
			return fmt.Errorf("create directory %s: %w", path, err)
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return fmt.Errorf("inspect directory %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("directory %s must not be a symlink", path)
	}
	return nil
}

func routeFile(home string, kind TaskKind, class ExecutionClass) (string, error) {
	if err := validateRouteCell(kind, class); err != nil {
		return "", err
	}
	return filepath.Join(home, configDirectory, routesDirectory, string(kind)+"."+string(class)), nil
}

func validateRouteCell(kind TaskKind, class ExecutionClass) error {
	if err := ValidateTaskKind(kind); err != nil {
		return err
	}
	if err := ValidateExecutionClass(class); err != nil {
		return err
	}
	return nil
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

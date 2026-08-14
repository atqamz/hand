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
	if err := ValidateProfile(profile); err != nil {
		return err
	}
	dir, err := profileDirectory(home, profile.Name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create profile directory: %w", err)
	}
	if err := writeConfigValue(filepath.Join(dir, "harness"), profile.Harness); err != nil {
		return fmt.Errorf("write profile harness: %w", err)
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
				return fmt.Errorf("write profile %s: %w", setting.name, err)
			}
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove profile %s: %w", setting.name, err)
		}
	}
	return nil
}

func RemoveProfile(home, name string) error {
	if err := ValidateProfileName(name); err != nil {
		return err
	}
	config, err := Load(home)
	if err != nil {
		return err
	}
	for _, route := range config.Routes {
		if route.Profile == name {
			return fmt.Errorf("profile %q is still referenced by %s.%s", name, route.Kind, route.ExecutionClass)
		}
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
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove profile %q: %w", name, err)
	}
	return nil
}

func WriteRoute(home string, route Route) error {
	if err := ValidateRoute(route); err != nil {
		return err
	}
	profiles, err := ListProfiles(home)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(profiles, func(profile Profile) bool { return profile.Name == route.Profile }) {
		return fmt.Errorf("route profile %q is not configured", route.Profile)
	}
	path, err := routeFile(home, route.Kind, route.ExecutionClass)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create routes directory: %w", err)
	}
	if err := writeConfigValue(path, route.Profile); err != nil {
		return fmt.Errorf("write route %s.%s: %w", route.Kind, route.ExecutionClass, err)
	}
	return nil
}

func RemoveRoute(home string, kind TaskKind, class ExecutionClass) error {
	if err := validateRouteCell(kind, class); err != nil {
		return err
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
	dir := filepath.Join(home, configDirectory, profilesDirectory)
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
		if !entry.IsDir() {
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
	harnessName, found, err := readConfigValue(filepath.Join(dir, "harness"))
	if err != nil {
		return Profile{}, fmt.Errorf("read harness: %w", err)
	}
	if !found {
		return Profile{}, fmt.Errorf("profile harness is required")
	}
	model, modelSet, err := readConfigValue(filepath.Join(dir, "model"))
	if err != nil {
		return Profile{}, fmt.Errorf("read model: %w", err)
	}
	effort, effortSet, err := readConfigValue(filepath.Join(dir, "effort"))
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

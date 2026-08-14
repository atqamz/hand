package routing

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestValidateProfileNameAcceptsArbitrarySafeNames(t *testing.T) {
	for _, name := range []string{"daily", "brain-2", "luna.v3", "local profile"} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateProfileName(name); err != nil {
				t.Fatalf("ValidateProfileName(%q) = %v", name, err)
			}
		})
	}
}

func TestValidateProfileNameRejectsUnsafeFilename(t *testing.T) {
	for _, name := range []string{
		"",
		".",
		"..",
		"profile/name",
		`profile\name`,
		"profile" + "\x00" + "name",
		"profile" + "\n" + "name",
		"profile:name",
		"profile*name",
		"profile?name",
		"profile\"name",
		"profile<name",
		"profile>name",
		"profile|name",
		"CON",
		"lpt9.txt",
		"profile.",
		"profile ",
	} {
		t.Run("reject", func(t *testing.T) {
			if err := ValidateProfileName(name); err == nil {
				t.Fatalf("ValidateProfileName(%q) = nil", name)
			}
		})
	}
}

func TestValidateProfileRequiresOneLineValues(t *testing.T) {
	valid := Profile{Name: "daily", Harness: "codex", Model: "provider/model", Effort: "high"}
	if err := ValidateProfile(valid); err != nil {
		t.Fatalf("ValidateProfile(%+v) = %v", valid, err)
	}

	for _, profile := range []Profile{
		{Name: "daily", Harness: ""},
		{Name: "daily", Harness: "codex\nother"},
		{Name: "daily", Harness: "codex", Model: "model\nother"},
		{Name: "daily", Harness: "codex", Effort: "high\rother"},
	} {
		t.Run("reject", func(t *testing.T) {
			if err := ValidateProfile(profile); err == nil {
				t.Fatalf("ValidateProfile(%+v) = nil", profile)
			}
		})
	}
}

func TestValidateProfileRequiresSupportedHarnessCapabilities(t *testing.T) {
	for _, profile := range []Profile{
		{Name: "daily", Harness: "unknown"},
		{Name: "daily", Harness: "pi", Model: "provider/model"},
		{Name: "daily", Harness: "opencode", Effort: "high"},
	} {
		t.Run("reject", func(t *testing.T) {
			if err := ValidateProfile(profile); err == nil {
				t.Fatalf("ValidateProfile(%+v) = nil", profile)
			}
		})
	}
}

func TestValidateRouteUsesCanonicalKindsAndExecutionClasses(t *testing.T) {
	for _, kind := range TaskKinds() {
		if err := ValidateTaskKind(kind); err != nil {
			t.Fatalf("ValidateTaskKind(%q) = %v", kind, err)
		}
	}
	if err := ValidateTaskKind("review"); err == nil {
		t.Fatal("ValidateTaskKind(\"review\") = nil")
	}
	for _, class := range ExecutionClasses() {
		if err := ValidateExecutionClass(class); err != nil {
			t.Fatalf("ValidateExecutionClass(%q) = %v", class, err)
		}
	}
	if err := ValidateExecutionClass("fast"); err == nil {
		t.Fatal("ValidateExecutionClass(\"fast\") = nil")
	}

	for _, kind := range TaskKinds() {
		for _, class := range ExecutionClasses() {
			route := Route{Kind: kind, ExecutionClass: class, Profile: "daily"}
			if err := ValidateRoute(route); err != nil {
				t.Fatalf("ValidateRoute(%+v) = %v", route, err)
			}
		}
	}

	for _, route := range []Route{
		{Kind: "review", ExecutionClass: ExecutionClassMechanical, Profile: "daily"},
		{Kind: TaskKindScout, ExecutionClass: "fast", Profile: "daily"},
		{Kind: TaskKindShip, ExecutionClass: ExecutionClassDeep, Profile: "../daily"},
	} {
		t.Run("reject", func(t *testing.T) {
			if err := ValidateRoute(route); err == nil {
				t.Fatalf("ValidateRoute(%+v) = nil", route)
			}
		})
	}
}

func TestProfileConfigurationListsInNameOrderAndReplacesOptionalValues(t *testing.T) {
	home := t.TempDir()
	for _, profile := range []Profile{
		{Name: "zeta", Harness: "claude", Model: "claude-opus-5", Effort: "high"},
		{Name: "alpha", Harness: "codex"},
	} {
		if err := WriteProfile(home, profile); err != nil {
			t.Fatalf("WriteProfile(%+v) = %v", profile, err)
		}
	}

	profiles, err := ListProfiles(home)
	if err != nil {
		t.Fatal(err)
	}
	want := []Profile{
		{Name: "alpha", Harness: "codex"},
		{Name: "zeta", Harness: "claude", Model: "claude-opus-5", Effort: "high"},
	}
	if !slices.Equal(profiles, want) {
		t.Fatalf("ListProfiles() = %+v, want %+v", profiles, want)
	}

	for _, setting := range []struct {
		path string
		want string
	}{
		{path: filepath.Join(home, "config", "profiles", "zeta", "harness"), want: "claude\n"},
		{path: filepath.Join(home, "config", "profiles", "zeta", "model"), want: "claude-opus-5\n"},
		{path: filepath.Join(home, "config", "profiles", "zeta", "effort"), want: "high\n"},
	} {
		got, err := os.ReadFile(setting.path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != setting.want {
			t.Fatalf("%s = %q, want %q", setting.path, got, setting.want)
		}
	}

	if err := WriteProfile(home, Profile{Name: "zeta", Harness: "claude"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"model", "effort"} {
		_, err := os.Stat(filepath.Join(home, "config", "profiles", "zeta", name))
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stale %s file error = %v, want not exist", name, err)
		}
	}
}

func TestRouteConfigurationCoversAllCellsInCanonicalOrder(t *testing.T) {
	home := t.TempDir()
	if err := WriteProfile(home, Profile{Name: "daily", Harness: "codex"}); err != nil {
		t.Fatal(err)
	}

	var want []Route
	for _, kind := range TaskKinds() {
		for _, class := range ExecutionClasses() {
			route := Route{Kind: kind, ExecutionClass: class, Profile: "daily"}
			if err := WriteRoute(home, route); err != nil {
				t.Fatalf("WriteRoute(%+v) = %v", route, err)
			}
			want = append(want, route)
		}
	}

	routes, err := ListRoutes(home)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(routes, want) {
		t.Fatalf("ListRoutes() = %+v, want %+v", routes, want)
	}

	config, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Problems) != 0 {
		t.Fatalf("Load().Problems = %+v, want none", config.Problems)
	}
}

func TestLoadReportsMissingRoutesDanglingRoutesAndMalformedProfiles(t *testing.T) {
	home := t.TempDir()
	for _, path := range []string{
		filepath.Join(home, "config", "profiles", "broken"),
		filepath.Join(home, "config", "routes"),
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "config", "routes", "scout.mechanical"), []byte("missing\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	config, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Profiles) != 0 {
		t.Fatalf("Load().Profiles = %+v, want none", config.Profiles)
	}
	if got, want := config.Routes, []Route{{Kind: TaskKindScout, ExecutionClass: ExecutionClassMechanical, Profile: "missing"}}; !slices.Equal(got, want) {
		t.Fatalf("Load().Routes = %+v, want %+v", got, want)
	}
	for _, code := range []ConfigProblemCode{ConfigProblemMalformedProfile, ConfigProblemMissingRoute, ConfigProblemDanglingRoute} {
		if !hasProblem(config.Problems, code) {
			t.Fatalf("Load().Problems = %+v, want %s", config.Problems, code)
		}
	}
	if got, want := countProblems(config.Problems, ConfigProblemMissingRoute), 5; got != want {
		t.Fatalf("missing route problems = %d, want %d", got, want)
	}
}

func TestLoadDistinguishesUnsupportedProfileCapabilities(t *testing.T) {
	home := t.TempDir()
	for _, profile := range []Profile{
		{Name: "unknown", Harness: "unknown"},
		{Name: "model", Harness: "pi", Model: "provider/model"},
		{Name: "effort", Harness: "opencode", Effort: "high"},
	} {
		dir := filepath.Join(home, "config", "profiles", profile.Name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, setting := range []struct {
			name  string
			value string
		}{
			{name: "harness", value: profile.Harness},
			{name: "model", value: profile.Model},
			{name: "effort", value: profile.Effort},
		} {
			if setting.value == "" {
				continue
			}
			if err := os.WriteFile(filepath.Join(dir, setting.name), []byte(setting.value+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	config, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		profile string
		code    ConfigProblemCode
	}{
		{profile: "unknown", code: ConfigProblemUnsupportedHarness},
		{profile: "model", code: ConfigProblemUnsupportedModel},
		{profile: "effort", code: ConfigProblemUnsupportedEffort},
	} {
		if !hasProblemForProfile(config.Problems, want.profile, want.code) {
			t.Fatalf("Load().Problems = %+v, want %s for %q", config.Problems, want.code, want.profile)
		}
	}
}

func TestWriteRouteRequiresExistingProfile(t *testing.T) {
	home := t.TempDir()
	route := Route{Kind: TaskKindShip, ExecutionClass: ExecutionClassDeep, Profile: "missing"}
	if err := WriteRoute(home, route); err == nil {
		t.Fatal("WriteRoute() = nil")
	}
	_, err := os.Stat(filepath.Join(home, "config", "routes", "ship.deep"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("route file error = %v, want not exist", err)
	}
}

func TestRemoveProfileRefusesReferencedRoute(t *testing.T) {
	home := t.TempDir()
	profile := Profile{Name: "daily", Harness: "codex"}
	if err := WriteProfile(home, profile); err != nil {
		t.Fatal(err)
	}
	if err := WriteRoute(home, Route{Kind: TaskKindScout, ExecutionClass: ExecutionClassStandard, Profile: profile.Name}); err != nil {
		t.Fatal(err)
	}
	if err := RemoveProfile(home, profile.Name); err == nil {
		t.Fatal("RemoveProfile() = nil")
	}
	profiles, err := ListProfiles(home)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(profiles, []Profile{profile}) {
		t.Fatalf("ListProfiles() = %+v, want %+v", profiles, []Profile{profile})
	}
}

func TestRemoveRouteThenProfile(t *testing.T) {
	home := t.TempDir()
	profile := Profile{Name: "daily", Harness: "codex"}
	route := Route{Kind: TaskKindScout, ExecutionClass: ExecutionClassStandard, Profile: profile.Name}
	if err := WriteProfile(home, profile); err != nil {
		t.Fatal(err)
	}
	if err := WriteRoute(home, route); err != nil {
		t.Fatal(err)
	}
	if err := RemoveRoute(home, route.Kind, route.ExecutionClass); err != nil {
		t.Fatal(err)
	}
	if err := RemoveProfile(home, profile.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "config", "profiles", profile.Name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("profile directory error = %v, want not exist", err)
	}
}

func TestRejectedProfileAndRouteDoNotCreateConfiguration(t *testing.T) {
	home := t.TempDir()
	if err := WriteProfile(home, Profile{Name: "../escape", Harness: "codex"}); err == nil {
		t.Fatal("WriteProfile() = nil")
	}
	if _, err := os.Stat(filepath.Join(home, "config")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config directory error = %v, want not exist", err)
	}
	if err := WriteRoute(home, Route{Kind: TaskKindScout, ExecutionClass: ExecutionClassDeep, Profile: "missing"}); err == nil {
		t.Fatal("WriteRoute() = nil")
	}
	if _, err := os.Stat(filepath.Join(home, "config", "routes")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("routes directory error = %v, want not exist", err)
	}
}

func TestWriteProfileRefusesSymlinkedProfileDirectory(t *testing.T) {
	home := t.TempDir()
	profiles := filepath.Join(home, "config", "profiles")
	if err := os.MkdirAll(profiles, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(profiles, "daily")); err != nil {
		t.Skipf("create symlink: %v", err)
	}

	err := WriteProfile(home, Profile{Name: "daily", Harness: "codex"})
	if err == nil {
		t.Fatal("WriteProfile() = nil")
	}
	if _, err := os.Stat(filepath.Join(outside, "harness")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside harness error = %v, want not exist", err)
	}
}

func TestWriteProfileRefusesSymlinkedProfilesDirectory(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(home, "config", "profiles")); err != nil {
		t.Skipf("create symlink: %v", err)
	}

	err := WriteProfile(home, Profile{Name: "daily", Harness: "codex"})
	if err == nil {
		t.Fatal("WriteProfile() = nil")
	}
	if _, err := os.Stat(filepath.Join(outside, "daily", "harness")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside harness error = %v, want not exist", err)
	}
}

func hasProblem(problems []ConfigProblem, want ConfigProblemCode) bool {
	for _, problem := range problems {
		if problem.Code == want {
			return true
		}
	}
	return false
}

func countProblems(problems []ConfigProblem, want ConfigProblemCode) int {
	count := 0
	for _, problem := range problems {
		if problem.Code == want {
			count++
		}
	}
	return count
}

func hasProblemForProfile(problems []ConfigProblem, profile string, want ConfigProblemCode) bool {
	for _, problem := range problems {
		if problem.Profile == profile && problem.Code == want {
			return true
		}
	}
	return false
}

// Command fakenpm stands in for the real npm CLI in tests/npmpublish, answering
// exactly the subcommands .github/scripts/npm-registry-check.sh and
// npm-publish-target.sh call: view, whoami, config list, and publish. State lives
// under FAKE_NPM_STATE, one JSON file per package so a publish call's read-modify-write
// never has to reason about any package but its own.
package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type packageState struct {
	RepositoryURL string                  `json:"repository_url"`
	Versions      map[string]versionState `json:"versions"`
}

type versionState struct {
	Integrity string `json:"integrity"`
}

type publishManifest struct {
	Name          string `json:"name"`
	Version       string `json:"version"`
	Integrity     string `json:"integrity"`
	RepositoryURL string `json:"repository_url"`
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	state := os.Getenv("FAKE_NPM_STATE")
	if state == "" {
		fmt.Fprintln(os.Stderr, "fakenpm: FAKE_NPM_STATE is not set")
		return 2
	}
	if err := os.MkdirAll(state, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "fakenpm:", err)
		return 2
	}
	// Every subcommand gets the same trailing marker, not just publish, so a test can
	// tell whether a *view* call (post-publish verification) ran authenticated - the
	// bug atqamz/hand#506 fixed was specific to verification, not to publish itself.
	appendLog(state, append(append([]string{}, args...), "authtoken="+fmt.Sprint(os.Getenv("NODE_AUTH_TOKEN") != "")))
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "fakenpm: no subcommand given")
		return 2
	}
	switch args[0] {
	case "view":
		return cmdView(state, args[1:])
	case "whoami":
		return cmdWhoami(state)
	case "config":
		return cmdConfig(args[1:])
	case "publish":
		return cmdPublish(state, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "fakenpm: unhandled subcommand %q\n", args[0])
		return 2
	}
}

func cmdView(state string, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "fakenpm: view needs a target")
		return 2
	}
	// A test-only trigger for a registry answer npm-registry-check.sh cannot get from
	// the real registry on demand: something other than a clean hit or a shaped E404.
	if _, err := os.Stat(filepath.Join(state, "force-ambiguous")); err == nil {
		doc := map[string]any{"error": map[string]any{"code": "E500", "summary": "internal server error"}}
		data, _ := json.MarshalIndent(doc, "", "  ")
		fmt.Println(string(data))
		return 1
	}
	target := args[0]
	var fields []string
	for _, a := range args[1:] {
		if a == "--json" || a == "" {
			continue
		}
		fields = append(fields, a)
	}
	name, version, hasVersion := splitTarget(target)

	pkg, ok := readPackage(state, name)
	if !ok {
		return writeError(name, "Not Found - GET https://registry.npmjs.org/"+url.PathEscape(name)+" - Not found")
	}
	if !hasVersion {
		version, ok = latestVersion(pkg)
		if !ok {
			return writeError(name, "Not Found - GET https://registry.npmjs.org/"+url.PathEscape(name)+" - Not found")
		}
	}
	entry, ok := pkg.Versions[version]
	if !ok {
		return writeError(name+"@"+version, "No match found for version "+version)
	}
	return writeFields(name, version, entry, pkg, fields)
}

func writeError(target, summary string) int {
	doc := map[string]any{"error": map[string]any{
		"code":    "E404",
		"summary": summary,
		"detail":  "The requested resource '" + target + "' could not be found or you do not have permission to access it.",
	}}
	data, _ := json.MarshalIndent(doc, "", "  ")
	fmt.Println(string(data))
	return 1
}

func writeFields(name, version string, entry versionState, pkg packageState, fields []string) int {
	values := map[string]string{
		"name":           name,
		"version":        version,
		"dist.integrity": entry.Integrity,
		"repository.url": pkg.RepositoryURL,
	}
	var doc any
	if len(fields) == 1 {
		doc = values[fields[0]]
	} else {
		out := map[string]string{}
		for _, f := range fields {
			out[f] = values[f]
		}
		doc = out
	}
	if arrayViewShape() {
		doc = []any{doc}
	}
	data, _ := json.MarshalIndent(doc, "", "  ")
	fmt.Println(string(data))
	return 0
}

// npm 12 wraps a successful `npm view --json` document in an array where npm 11 and
// earlier printed it bare. Release CI pins npm 12, so the array is the default and the
// object shape is opt-in, letting one test table prove both are read (atqamz/hand#511).
func arrayViewShape() bool {
	return os.Getenv("FAKE_NPM_VIEW_SHAPE") != "object"
}

func cmdWhoami(state string) int {
	if _, err := os.Stat(filepath.Join(state, "whoami-fail")); err == nil {
		fmt.Fprintln(os.Stderr, "npm error code ENEEDAUTH")
		fmt.Fprintln(os.Stderr, "npm error need auth This command requires you to be logged in.")
		return 1
	}
	identity := "faketool-npm-user"
	if data, err := os.ReadFile(filepath.Join(state, "whoami-identity")); err == nil {
		identity = strings.TrimSpace(string(data))
	}
	fmt.Println(identity)
	return 0
}

// Real npm refuses `config get //registry.npmjs.org/:_authToken` as protected whether
// or not one is set (observed, not documented), so the caller greps `config list`'s
// masked output for the key's presence instead; this reproduces that same shape.
func cmdConfig(args []string) int {
	if len(args) == 0 || args[0] != "list" {
		fmt.Fprintln(os.Stderr, "fakenpm: unhandled config subcommand")
		return 2
	}
	userconfig := os.Getenv("NPM_CONFIG_USERCONFIG")
	if userconfig != "" {
		if data, err := os.ReadFile(userconfig); err == nil && strings.Contains(string(data), "_authToken") {
			fmt.Println(`//registry.npmjs.org/:_authToken = (protected)`)
		}
	}
	fmt.Println("; node bin location = fakenpm")
	return 0
}

func cmdPublish(state string, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "fakenpm: publish needs a tarball path")
		return 2
	}
	tarball := args[0]
	provenance := false
	for _, a := range args[1:] {
		if a == "--provenance" {
			provenance = true
		}
	}
	if _, err := os.Stat(filepath.Join(state, "publish-fail")); err == nil {
		fmt.Fprintln(os.Stderr, "npm error code E401")
		fmt.Fprintln(os.Stderr, "npm error 401 Unauthorized - PUT - you must be logged in to publish packages")
		return 1
	}
	manifestData, err := os.ReadFile(tarball + ".manifest.json")
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakenpm: no manifest for tarball", tarball, ":", err)
		return 2
	}
	var manifest publishManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		fmt.Fprintln(os.Stderr, "fakenpm: decode manifest:", err)
		return 2
	}
	pkg, _ := readPackage(state, manifest.Name)
	if pkg.Versions == nil {
		pkg.Versions = map[string]versionState{}
	}
	pkg.RepositoryURL = manifest.RepositoryURL
	pkg.Versions[manifest.Version] = versionState{Integrity: manifest.Integrity}
	if err := writePackage(state, manifest.Name, pkg); err != nil {
		fmt.Fprintln(os.Stderr, "fakenpm: record publish:", err)
		return 2
	}
	appendLog(state, []string{"published", manifest.Name, manifest.Version,
		"provenance=" + fmt.Sprint(provenance), "authtoken=" + fmt.Sprint(os.Getenv("NODE_AUTH_TOKEN") != "")})
	fmt.Printf("+ %s@%s\n", manifest.Name, manifest.Version)
	return 0
}

func splitTarget(target string) (name, version string, hasVersion bool) {
	at := strings.LastIndex(target, "@")
	if at <= 0 {
		return target, "", false
	}
	return target[:at], target[at+1:], true
}

func latestVersion(pkg packageState) (string, bool) {
	best := ""
	for v := range pkg.Versions {
		if v > best {
			best = v
		}
	}
	return best, best != ""
}

func packagePath(state, name string) string {
	return filepath.Join(state, "pkg-"+strings.NewReplacer("/", "_", "@", "_").Replace(name)+".json")
}

func readPackage(state, name string) (packageState, bool) {
	data, err := os.ReadFile(packagePath(state, name))
	if err != nil {
		return packageState{}, false
	}
	var pkg packageState
	if err := json.Unmarshal(data, &pkg); err != nil {
		return packageState{}, false
	}
	return pkg, true
}

func writePackage(state, name string, pkg packageState) error {
	data, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(packagePath(state, name), data, 0o644)
}

func appendLog(state string, args []string) {
	f, err := os.OpenFile(filepath.Join(state, "calls.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintln(f, strings.Join(args, " "))
}

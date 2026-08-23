package supervision

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The generated host artifacts must be executable syntax, not plausible
// substrings: node parses the OpenCode plugin and bun transpiles the Pi
// extension whenever those toolchains run this suite.
func TestGeneratedOpenCodePluginParsesAsESM(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not available for artifact parsing")
	}
	rendered := renderAsset(HostAssets("opencode")[0], "/opt/bin/hand", "/fleet home")
	path := filepath.Join(t.TempDir(), "hand-supervisor-wake.mjs")
	if err := os.WriteFile(path, rendered, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("node", "--check", path).CombinedOutput()
	if err != nil {
		t.Fatalf("node --check rejected the generated plugin: %v\n%s", err, out)
	}
}

func TestGeneratedPiExtensionTranspiles(t *testing.T) {
	bunPath, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("bun is not available for artifact transpiling")
	}
	rendered := renderAsset(HostAssets("pi")[0], `/opt/my hand\bin`, "/fleet home")
	dir := t.TempDir()
	path := filepath.Join(dir, "hand-supervisor-wake.ts")
	if err := os.WriteFile(path, rendered, 0o644); err != nil {
		t.Fatal(err)
	}
	// Stub the host's extension module so bundling resolves offline; only
	// syntax and type-stripping of the generated artifact are under test.
	stub := filepath.Join(dir, "node_modules", "@earendil-works", "pi-coding-agent")
	if err := os.MkdirAll(stub, 0o755); err != nil {
		t.Fatal(err)
	}
	pkg := `{"name":"@earendil-works/pi-coding-agent","version":"0.0.0","types":"./index.d.ts","main":"./index.js"}`
	if err := os.WriteFile(filepath.Join(stub, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stub, "index.d.ts"), []byte("export type ExtensionAPI = any;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stub, "index.js"), []byte("export {};\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(bunPath, "build", path, "--outdir", filepath.Join(dir, "out")).CombinedOutput()
	if err != nil {
		t.Fatalf("transpiler rejected the generated extension: %v\n%s", err, out)
	}
}

// The protocol schema constant and both generated adapters must agree; a
// drifted literal would fail closed at runtime instead of delivering.
func TestWakeSchemaMatchesGeneratedAdapters(t *testing.T) {
	for _, asset := range []Asset{HostAssets("opencode")[0], HostAssets("pi")[0]} {
		body := string(asset.Body)
		if !strings.Contains(body, `"`+WakeSchema+`"`) && !strings.Contains(body, WakeSchema) {
			t.Fatalf("%s does not carry the wake schema constant %q", asset.RelPath, WakeSchema)
		}
	}
}

// Executes the generated OpenCode plugin's ownership predicate inside its own
// runtime: subagents (parentID) and sessions outside the Fleet home can never
// qualify as the primary supervisor.
func TestGeneratedOpenCodeOwnershipPredicateExecutes(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not available to execute the generated plugin")
	}
	rendered := string(renderAsset(HostAssets("opencode")[0], "/opt/bin/hand", "/fleet/home"))
	script := `
const { qualifiesSession } = await import(process.env.HAND_PLUGIN_PATH);
const assert = (cond, msg) => { if (!cond) { console.error(msg); process.exit(1); } };
assert(qualifiesSession({ directory: "/fleet/home" }, "/fleet/home") === true, "primary session must qualify");
assert(qualifiesSession({ directory: "/fleet/home", parentID: "p1" }, "/fleet/home") === false, "subagent must not qualify");
assert(qualifiesSession({ directory: "/other/project" }, "/fleet/home") === false, "foreign project must not qualify");
assert(qualifiesSession(null, "/fleet/home") === false, "missing info must not qualify");
console.log("ok");
`
	tmp := t.TempDir()
	pluginPath := filepath.Join(tmp, "plugin.mjs")
	if err := os.WriteFile(pluginPath, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	testPath := filepath.Join(tmp, "ownership.test.mjs")
	if err := os.WriteFile(testPath, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", testPath)
	cmd.Env = append(os.Environ(), "HAND_PLUGIN_PATH="+pluginPath, "HAND_HOME=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated plugin failed its executed contract: %v\n%s", err, out)
	}
}

// Executes the generated Pi extension's protocol parser in bun when present:
// only the versioned machine protocol parses; human rendering never does.
func TestGeneratedPiProtocolParserExecutes(t *testing.T) {
	bunPath, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("bun is not available to execute the generated extension")
	}
	rendered := string(renderAsset(HostAssets("pi")[0], "/opt/bin/hand", "/fleet/home"))
	script := rendered + `
const assert = (cond, msg) => { if (!cond) { console.error(msg); process.exit(1); } };
const good = parseWake(JSON.stringify({ schema: "hand.supervision.wake.v1", fleet_id: "f_1", message: "m", episodes: [{ key: "k" }] }));
assert(good !== null && good.fleet_id === "f_1" && good.episodes[0].key === "k", "valid protocol must parse");
assert(parseWake("text: hand has work") === null, "rendered TOON must not parse");
assert(parseWake("{\"schema\":\"other.v9\"}") === null, "unknown schema must fail closed");
assert(parseWake("not json") === null, "garbage must fail closed");
console.log("ok");
`
	path := filepath.Join(t.TempDir(), "extension.test.ts")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bunPath, "run", path).CombinedOutput()
	if err != nil {
		t.Fatalf("generated extension failed its executed contract: %v\n%s", err, out)
	}
}

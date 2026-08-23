package supervision

import (
	"bytes"
	"embed"
	"encoding/json"
)

//go:embed assets
var assetsFS embed.FS

// One managed host-integration file. Script assets carry a template body
// whose path placeholders are substituted as JSON string literals; structured
// documents carry a Render function producing their full bytes.
type Asset struct {
	// RelPath is the destination path relative to the fleet home.
	RelPath string
	// Body is the canonical template for placeholder substitution.
	Body []byte
	// Render, when set, produces the entire file and Body is unused.
	Render func(exe string) []byte
}

func asset(name string) []byte {
	data, err := assetsFS.ReadFile("assets/" + name)
	if err != nil {
		panic("supervision: embedded asset missing: " + name)
	}
	return data
}

// HostAssets returns the managed script-template files for one host. Grok
// needs none: its bridge is instruction-established. Codex is absent too: its
// Fleet-local hooks.json is merged through InstallCodexHooks instead.
func HostAssets(host string) []Asset {
	switch host {
	case "opencode":
		return []Asset{{
			RelPath: ".opencode/plugins/hand-supervisor-wake.js",
			Body:    asset("opencode/hand-supervisor-wake.js"),
		}}
	case "pi":
		return []Asset{{
			RelPath: ".pi/extensions/hand-supervisor-wake.ts",
			Body:    asset("pi/hand-supervisor-wake.ts"),
		}}
	default:
		return nil
	}
}

// ManagedAssetHosts lists every host whose bridge installs through the
// template pipeline, in the stable order used by init.
func ManagedAssetHosts() []string {
	return []string{"opencode", "pi"}
}

// CodexHooksRelPath is the Fleet-local hooks document hand owns a Stop group
// inside.
const CodexHooksRelPath = ".codex/hooks.json"

// Produces one asset's final bytes with install-time substitutions applied.
func renderAsset(a Asset, exe, home string) []byte {
	if a.Render != nil {
		return a.Render(exe)
	}
	body := bytes.ReplaceAll(a.Body, []byte("__HAND_EXECUTABLE__"), mustJSONString(exe))
	return bytes.ReplaceAll(body, []byte("__HAND_HOME__"), mustJSONString(home))
}

// Yields a complete JSON string literal: encoding/json owns
// the escaping grammar, so no path byte can survive misquoted.
func mustJSONString(value string) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic("supervision: json string marshal failed: " + err.Error())
	}
	return encoded
}

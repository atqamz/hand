package supervision

import "embed"

//go:embed assets
var assetsFS embed.FS

// Asset describes one managed host-integration file rendered into its
// destination with install-time substitutions applied.
type Asset struct {
	// RelPath is the destination path relative to the fleet home.
	RelPath string
	// Body is the canonical template. The __HAND_EXECUTABLE__ and
	// __HAND_HOME__ placeholders are replaced with quoted absolute paths at
	// render time, so doctor can compare exact bytes to detect staleness.
	Body []byte
}

func asset(name string) []byte {
	data, err := assetsFS.ReadFile("assets/" + name)
	if err != nil {
		panic("supervision: embedded asset missing: " + name)
	}
	return data
}

// HostAssets returns the managed files for one host. Grok needs none: its
// bridge is the host's own background-task lifecycle, established through the
// generated Supervisor instructions rather than an installed file.
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

// AllManagedAssets maps every host with installed assets to them, in the
// stable harness-name order used by init and doctor.
func AllManagedAssets() map[string][]Asset {
	hosts := map[string][]Asset{}
	for _, host := range []string{"opencode", "pi"} {
		if list := HostAssets(host); len(list) > 0 {
			hosts[host] = list
		}
	}
	return hosts
}

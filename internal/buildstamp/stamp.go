package buildstamp

import "runtime/debug"

func Commit(stamped string) string {
	id := stamped
	if info, ok := debug.ReadBuildInfo(); ok && id == "" {
		for _, kv := range info.Settings {
			if kv.Key == "vcs.revision" {
				id = kv.Value
			}
		}
	}
	if id == "" {
		return "unknown"
	}
	return id[:min(12, len(id))]
}

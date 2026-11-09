package buildstamp

import "runtime/debug"

const Unknown = "unknown"

func Known(id string) bool {
	return id != "" && id != Unknown
}

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
		return Unknown
	}
	return id[:min(12, len(id))]
}

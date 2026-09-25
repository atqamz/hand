package store

// canonicalV19ExecGuardKeyPrefix marks a provider_executor_key encoded by the v1 exec-guard
// key grammar, checked cross-platform even though only Linux currently encodes one.
const (
	canonicalV19ExecGuardKeyPrefix = canonicalV19ExecGuardKeyFamily + "v1?"
	canonicalV19ExecGuardUnknown   = "unknown"
)

package launch

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"sort"
	"strconv"
)

// CanonicalEnvironmentValue is one exact persisted process environment entry of a
// canonical v19 Launch. Secret values use value_kind=secret-ref so plaintext need not be
// stored; ValueDigest pins the resolved value that the exec guard verifies.
type CanonicalEnvironmentValue struct {
	ValueKind     string
	ValueMaterial string
	ValueDigest   string
}

// CanonicalSpec is the exact structured process request persisted for one canonical v19
// Launch. It contains process data only, never shell source or provider workspace/tab/pane identity.
type CanonicalSpec struct {
	Executable  string
	Arguments   []string
	Environment map[string]CanonicalEnvironmentValue
	Cwd         string
}

// CanonicalSpecDigest is the launch-spec commitment Tx A persists and the exec guard
// recomputes from its handoff before starting the harness.
func CanonicalSpecDigest(spec CanonicalSpec) string {
	hash := sha256.New()
	WriteDigestField(hash, "domain", "hand:v19:launch-spec:v1")
	WriteDigestField(hash, "executable", spec.Executable)
	WriteDigestField(hash, "cwd", spec.Cwd)
	WriteDigestField(hash, "argument_count", strconv.Itoa(len(spec.Arguments)))
	for ordinal, value := range spec.Arguments {
		WriteDigestField(hash, "argument_"+strconv.Itoa(ordinal), value)
	}
	names := CanonicalEnvironmentNames(spec.Environment)
	WriteDigestField(hash, "environment_count", strconv.Itoa(len(names)))
	for _, name := range names {
		value := spec.Environment[name]
		WriteDigestField(hash, "environment_name", name)
		WriteDigestField(hash, "environment_kind", value.ValueKind)
		WriteDigestField(hash, "environment_material", value.ValueMaterial)
		WriteDigestField(hash, "environment_digest", value.ValueDigest)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// EnvironmentValueDigest is the ValueDigest committed for one resolved environment
// value other than the exec-guard credential.
func EnvironmentValueDigest(value string) string {
	hash := sha256.New()
	WriteDigestField(hash, "domain", "hand:v19:launch-environment-value:v1")
	WriteDigestField(hash, "value", value)
	return hex.EncodeToString(hash.Sum(nil))
}

// ExecGuardCredentialVerifier is V_B, the ValueDigest of the HAND_WORKER_CREDENTIAL
// entry for one Fleet and ExecutorBinding.
func ExecGuardCredentialVerifier(fleetID, executorBindingID, credential string) string {
	hash := sha256.New()
	WriteDigestField(hash, "domain", "hand:v19:exec-guard-credential:v1")
	WriteDigestField(hash, "fleet_id", fleetID)
	WriteDigestField(hash, "executor_binding_id", executorBindingID)
	WriteDigestField(hash, "credential", credential)
	return hex.EncodeToString(hash.Sum(nil))
}

func CanonicalEnvironmentNames(environment map[string]CanonicalEnvironmentValue) []string {
	names := make([]string, 0, len(environment))
	for name := range environment {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// WriteDigestField writes one length-prefixed field of the framing every canonical v19
// digest shares, store's included.
func WriteDigestField(digest io.Writer, name, value string) {
	for _, field := range []string{name, value} {
		_, _ = digest.Write([]byte(strconv.Itoa(len(field)) + ":" + field))
	}
}

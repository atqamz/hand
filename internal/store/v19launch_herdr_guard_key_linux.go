//go:build linux

package store

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/atqamz/hand/internal/execguard"
	"github.com/atqamz/hand/internal/osfacts"
)

const (
	canonicalV19ExecGuardKeyPrefix = "herdr-exec-guard:v1?"
	canonicalV19ExecGuardUnknown   = "unknown"
)

type canonicalV19ExecGuardKey struct {
	Session      canonicalV19HerdrSessionProviderKey
	Assoc        string
	Guard        *osfacts.Incarnation
	Root         *osfacts.Incarnation
	ProcessGroup int
	Terminal     uint64
	Object       *execguard.Object
	ObjectClass  string
}

func encodeCanonicalV19ExecGuardKey(key canonicalV19ExecGuardKey) (string, error) {
	encoded := formatCanonicalV19ExecGuardKey(key)
	if _, err := parseCanonicalV19ExecGuardKey(encoded); err != nil {
		return "", err
	}
	return encoded, nil
}

func formatCanonicalV19ExecGuardKey(key canonicalV19ExecGuardKey) string {
	values := url.Values{}
	values.Set("assoc", key.Assoc)
	values.Set("os", "linux")
	values.Set("session", key.Session.SessionName)
	values.Set("workspace", key.Session.WorkspaceID)
	values.Set("tab", key.Session.TabID)
	values.Set("pane", key.Session.PaneID)
	values.Set("g", formatCanonicalV19ExecGuardIncarnation(key.Guard))
	values.Set("r", formatCanonicalV19ExecGuardIncarnation(key.Root))
	values.Set("p", canonicalV19ExecGuardUnknown)
	values.Set("tty", canonicalV19ExecGuardUnknown)
	if key.Root != nil {
		values.Set("p", strconv.Itoa(key.ProcessGroup))
		values.Set("tty", strconv.FormatUint(key.Terminal, 10))
	}
	values.Set("exe", canonicalV19ExecGuardUnknown)
	values.Set("exe_sha256", canonicalV19ExecGuardUnknown)
	if key.Object != nil {
		values.Set("exe", strconv.FormatUint(key.Object.Device, 10)+":"+strconv.FormatUint(key.Object.Inode, 10))
		values.Set("exe_sha256", key.Object.SHA256)
	}
	values.Set("exe_class", key.ObjectClass)
	return canonicalV19ExecGuardKeyPrefix + values.Encode()
}

func formatCanonicalV19ExecGuardIncarnation(incarnation *osfacts.Incarnation) string {
	if incarnation == nil {
		return canonicalV19ExecGuardUnknown
	}
	return incarnation.BootID + "." + strconv.Itoa(incarnation.PID) + "." + strconv.FormatUint(incarnation.StartTicks, 10)
}

// g, r (with p and tty) and exe_class, with exe and exe_sha256, are each `unknown` only in a
// key the attested claimed branch writes; the observed success path always knows them.
func parseCanonicalV19ExecGuardKey(value string) (canonicalV19ExecGuardKey, error) {
	var key canonicalV19ExecGuardKey
	query, ok := strings.CutPrefix(value, canonicalV19ExecGuardKeyPrefix)
	if !ok {
		return key, fmt.Errorf("missing %q prefix", canonicalV19ExecGuardKeyPrefix)
	}
	values, err := url.ParseQuery(query)
	if err != nil {
		return key, fmt.Errorf("parse query: %w", err)
	}
	fields := map[string]string{}
	for _, name := range []string{"assoc", "exe", "exe_class", "exe_sha256", "g", "os", "p", "pane", "r", "session", "tab", "tty", "workspace"} {
		if items := values[name]; len(items) != 1 || items[0] == "" {
			return key, fmt.Errorf("field %q must occur exactly once and be non-empty", name)
		}
		fields[name] = values.Get(name)
	}
	if len(values) != len(fields) {
		return key, errors.New("key carries an unknown field")
	}
	key.Session = canonicalV19HerdrSessionProviderKey{
		SessionName: fields["session"], WorkspaceID: fields["workspace"], TabID: fields["tab"], PaneID: fields["pane"],
	}
	key.Assoc, key.ObjectClass = fields["assoc"], fields["exe_class"]
	if key.Guard, err = parseCanonicalV19ExecGuardIncarnation(fields["g"]); err != nil {
		return key, fmt.Errorf("g: %w", err)
	}
	if key.Root, err = parseCanonicalV19ExecGuardIncarnation(fields["r"]); err != nil {
		return key, fmt.Errorf("r: %w", err)
	}
	if key.Root != nil {
		key.ProcessGroup, _ = strconv.Atoi(fields["p"])
		key.Terminal, _ = strconv.ParseUint(fields["tty"], 10, 64)
	}
	if device, inode, found := strings.Cut(fields["exe"], ":"); found {
		key.Object = &execguard.Object{SHA256: fields["exe_sha256"]}
		key.Object.Device, _ = strconv.ParseUint(device, 10, 64)
		key.Object.Inode, _ = strconv.ParseUint(inode, 10, 64)
	}
	switch {
	case fields["os"] != "linux":
		return key, fmt.Errorf("os %q has no guard grammar in this build", fields["os"])
	case key.Assoc != "observed" && key.Assoc != "unobserved" && key.Assoc != "mismatch":
		return key, fmt.Errorf("assoc %q is not observed, unobserved or mismatch", key.Assoc)
	case key.ObjectClass != execguard.ClassExact && key.ObjectClass != execguard.ClassSampled && key.ObjectClass != canonicalV19ExecGuardUnknown:
		return key, fmt.Errorf("exe_class %q is not exact, sampled or unknown", key.ObjectClass)
	case key.Root != nil && key.ProcessGroup <= 0:
		return key, errors.New("a known harness root needs a process group")
	case key.Object != nil && len(key.Object.SHA256) != sha256.Size*2:
		return key, errors.New("a known executable object needs its SHA-256")
	case formatCanonicalV19ExecGuardKey(key) != value:
		return key, errors.New("key is not in canonical form")
	}
	return key, nil
}

func parseCanonicalV19ExecGuardIncarnation(value string) (*osfacts.Incarnation, error) {
	if value == canonicalV19ExecGuardUnknown {
		return nil, nil
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 || parts[0] == "" {
		return nil, fmt.Errorf("incarnation %q is not boot.pid.starttime", value)
	}
	pid, pidErr := strconv.Atoi(parts[1])
	ticks, ticksErr := strconv.ParseUint(parts[2], 10, 64)
	if pidErr != nil || ticksErr != nil || pid <= 0 {
		return nil, fmt.Errorf("incarnation %q is not boot.pid.starttime", value)
	}
	return &osfacts.Incarnation{BootID: parts[0], PID: pid, StartTicks: ticks}, nil
}

//go:build linux

package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"github.com/atqamz/hand/internal/execguard"
	"github.com/atqamz/hand/internal/launch"
	"github.com/atqamz/hand/internal/osfacts"
)

// Tx A commits B and V_B with the spec, then handoff(L) alone receives S_B. An error
// returned with a prepared Launch came after Tx A, so that Launch must be fenced to settle.
func prepareCanonicalV19ExecGuardLaunch(
	ctx context.Context,
	homeDir string,
	input CanonicalV19LaunchPrepareInput,
) (canonicalV19HerdrLaunchCurrent, error) {
	if err := validateCanonicalV19ExecGuardSpec(input.Spec); err != nil {
		return canonicalV19HerdrLaunchCurrent{}, fmt.Errorf("launch canonical v19 Herdr: %w", err)
	}
	fleetID, err := readCanonicalV19FleetID(ctx, homeDir)
	if err != nil {
		return canonicalV19HerdrLaunchCurrent{}, fmt.Errorf("launch canonical v19 Herdr: read Fleet identity: %w", err)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return canonicalV19HerdrLaunchCurrent{}, fmt.Errorf("launch canonical v19 Herdr: generate credential: %w", err)
	}
	credential := base64.RawURLEncoding.EncodeToString(secret)
	input.Spec = cloneCanonicalV19LaunchSpec(input.Spec)
	if input.Spec.Environment == nil {
		input.Spec.Environment = map[string]CanonicalV19LaunchEnvironmentValue{}
	}
	input.Spec.Environment[execguard.ExecutorBindingEnv] = CanonicalV19LaunchEnvironmentValue{
		ValueKind: "literal", ValueMaterial: input.BindingID, ValueDigest: launch.EnvironmentValueDigest(input.BindingID),
	}
	input.Spec.Environment[execguard.CredentialEnv] = CanonicalV19LaunchEnvironmentValue{
		ValueKind: "secret-ref", ValueMaterial: execguard.Protocol,
		ValueDigest: launch.ExecGuardCredentialVerifier(fleetID, input.BindingID, credential),
	}
	if _, err := PrepareCanonicalV19Launch(ctx, homeDir, input); err != nil {
		return canonicalV19HerdrLaunchCurrent{}, err
	}
	current, err := readCanonicalV19HerdrLaunchCurrent(ctx, homeDir, input.OperationID)
	if err != nil {
		return canonicalV19HerdrLaunchCurrent{}, fmt.Errorf("launch canonical v19 Herdr: %w", err)
	}
	if err := writeCanonicalV19ExecGuardHandoff(canonicalV19ExecGuardDir(homeDir, input.OperationID), current, credential); err != nil {
		return current, fmt.Errorf("launch canonical v19 Herdr: write exec guard handoff: %w", err)
	}
	return current, nil
}

func validateCanonicalV19ExecGuardSpec(spec CanonicalV19LaunchSpec) error {
	if !filepath.IsAbs(spec.Executable) {
		return fmt.Errorf("launch executable %q is not an absolute path", spec.Executable)
	}
	for name, value := range spec.Environment {
		if name == execguard.ExecutorBindingEnv || name == execguard.CredentialEnv {
			return fmt.Errorf("launch environment %q is reserved for Hand core", name)
		}
		if value.ValueKind != "literal" {
			return fmt.Errorf("launch environment %q uses %s %q; no canonical v19 secret resolver is available", name, value.ValueKind, value.ValueMaterial)
		}
		if value.ValueDigest != launch.EnvironmentValueDigest(value.ValueMaterial) {
			return fmt.Errorf("launch environment %q value digest does not commit its literal value", name)
		}
	}
	return nil
}

func readCanonicalV19FleetID(ctx context.Context, homeDir string) (string, error) {
	db, err := openReadOnly(homeDir)
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close() }()
	var fleetID string
	return fleetID, db.sql.QueryRowContext(ctx, `SELECT fleet_id FROM fleet WHERE singleton=1`).Scan(&fleetID)
}

func canonicalV19ExecGuardDir(homeDir, operationID string) string {
	return filepath.Join(homeDir, "state", "exec-guard", operationID)
}

func writeCanonicalV19ExecGuardHandoff(dir string, current canonicalV19HerdrLaunchCurrent, credential string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	self, err := osfacts.ReadIncarnation(os.Getpid())
	if err != nil {
		return err
	}
	namespace, err := osfacts.SelfPIDNamespace()
	if err != nil {
		return err
	}
	request := current.Current.Request
	values := make(map[string]string, len(request.Spec.Environment))
	for name, value := range request.Spec.Environment {
		values[name] = value.ValueMaterial
	}
	values[execguard.CredentialEnv] = credential
	return execguard.WriteHandoff(dir, execguard.Handoff{
		LaunchOperationID: request.OperationID,
		FleetID:           current.FleetID,
		ExecutorBindingID: request.BindingID,
		RequestDigest:     request.RequestDigest,
		LaunchSpecDigest:  request.LaunchSpecDigest,
		Spec:              request.Spec,
		Values:            values,
		WorktreePath:      current.WorktreePath,
		BootID:            self.BootID,
		PIDNamespace:      namespace,
		UID:               uint32(os.Getuid()),
	})
}

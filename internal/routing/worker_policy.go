package routing

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
)

const WorkerPolicySchema = "hand.worker-policy.v1"

type WorkerPolicy struct {
	Schema   string        `json:"schema"`
	Profiles []Profile     `json:"profiles"`
	Routes   []WorkerRoute `json:"routes"`
	Witness  string        `json:"-"`
}

type WorkerRoute struct {
	Intent   string `json:"intent"`
	Judgment string `json:"judgment"`
	Profile  string `json:"profile"`
}

var workerRouteKeys = [][2]string{
	{"explore", "mechanical"}, {"explore", "bounded"}, {"explore", "substantial"},
	{"execute", "mechanical"}, {"execute", "bounded"}, {"execute", "substantial"},
}

func LoadWorkerPolicy(home string) (WorkerPolicy, error) {
	if err := inspectOptionalDirectory(filepath.Join(home, configDirectory)); err != nil {
		return WorkerPolicy{}, fmt.Errorf("inspect worker policy directory: %w", err)
	}
	path := filepath.Join(home, configDirectory, "worker-policy.json")
	info, err := os.Lstat(path)
	if err != nil {
		return WorkerPolicy{}, fmt.Errorf("inspect worker policy: %w", err)
	}
	if !info.Mode().IsRegular() {
		return WorkerPolicy{}, errors.New("worker policy must be a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return WorkerPolicy{}, fmt.Errorf("read worker policy: %w", err)
	}
	if err := validateWorkerPolicyFields(data); err != nil {
		return WorkerPolicy{}, fmt.Errorf("decode worker policy: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var policy WorkerPolicy
	if err := decoder.Decode(&policy); err != nil {
		return WorkerPolicy{}, fmt.Errorf("decode worker policy: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return WorkerPolicy{}, errors.New("worker policy has trailing data")
	}
	if policy.Schema != WorkerPolicySchema {
		return WorkerPolicy{}, fmt.Errorf("unsupported worker policy schema %q: want %q", policy.Schema, WorkerPolicySchema)
	}
	profiles := make(map[string]bool, len(policy.Profiles))
	for _, profile := range policy.Profiles {
		for _, field := range []struct {
			name  string
			value string
		}{
			{"name", profile.Name},
			{"model", profile.Model},
			{"effort", profile.Effort},
		} {
			if !validWorkerCandidateValue(field.value) {
				return WorkerPolicy{}, fmt.Errorf("invalid worker profile %s value", field.name)
			}
		}
		if err := ValidateProfile(profile); err != nil {
			return WorkerPolicy{}, fmt.Errorf("invalid worker profile %q: %w", profile.Name, err)
		}
		if profiles[profile.Name] {
			return WorkerPolicy{}, fmt.Errorf("duplicate worker profile %q", profile.Name)
		}
		profiles[profile.Name] = true
	}
	routes := make(map[[2]string]WorkerRoute, len(policy.Routes))
	for _, route := range policy.Routes {
		key := [2]string{route.Intent, route.Judgment}
		valid := false
		for _, candidate := range workerRouteKeys {
			if key == candidate {
				valid = true
				break
			}
		}
		if !valid {
			return WorkerPolicy{}, fmt.Errorf("invalid Worker Route %q.%q", route.Intent, route.Judgment)
		}
		if _, found := routes[key]; found {
			return WorkerPolicy{}, fmt.Errorf("duplicate Worker Route %s.%s", route.Intent, route.Judgment)
		}
		if !profiles[route.Profile] {
			return WorkerPolicy{}, fmt.Errorf("worker route %s.%s names missing profile %q", route.Intent, route.Judgment, route.Profile)
		}
		routes[key] = route
	}
	policy.Routes = policy.Routes[:0]
	for _, key := range workerRouteKeys {
		route, found := routes[key]
		if !found {
			return WorkerPolicy{}, fmt.Errorf("worker route %s.%s is missing", key[0], key[1])
		}
		policy.Routes = append(policy.Routes, route)
	}
	digest := sha256.Sum256(data)
	policy.Witness = fmt.Sprintf("sha256:%x", digest)
	return policy, nil
}

func validateWorkerPolicyFields(data []byte) error {
	fields, err := workerPolicyFields(data, "schema", "profiles", "routes")
	if err != nil {
		return err
	}
	for _, group := range []struct {
		name    string
		allowed []string
	}{
		{name: "profiles", allowed: []string{"name", "harness", "model", "effort"}},
		{name: "routes", allowed: []string{"intent", "judgment", "profile"}},
	} {
		var entries []json.RawMessage
		if err := json.Unmarshal(fields[group.name], &entries); err != nil {
			return fmt.Errorf("%s: %w", group.name, err)
		}
		for _, entry := range entries {
			if _, err := workerPolicyFields(entry, group.allowed...); err != nil {
				return fmt.Errorf("%s: %w", group.name, err)
			}
		}
	}
	return nil
}

func workerPolicyFields(data []byte, allowed ...string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if start != json.Delim('{') {
		return nil, errors.New("worker policy object is required")
	}
	fields := make(map[string]json.RawMessage, len(allowed))
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name, ok := key.(string)
		if !ok || !slices.Contains(allowed, name) {
			return nil, fmt.Errorf("unsupported worker policy field %q", key)
		}
		if _, found := fields[name]; found {
			return nil, fmt.Errorf("duplicate worker policy field %q", name)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		fields[name] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("worker policy object has trailing data")
	}
	return fields, nil
}

package launch

import "testing"

// Tx A commits these and the exec guard and the WorkerInput acceptance check recompute them,
// so a change to any framing strands every committed Launch, credential and resolved value.
func TestCanonicalDigestsKeepTheirFraming(t *testing.T) {
	spec := CanonicalSpec{
		Executable: "/opt/worker", Arguments: []string{"run", ""}, Cwd: "/worktree",
		Environment: map[string]CanonicalEnvironmentValue{
			"HAND_ROLE": {ValueKind: "literal", ValueMaterial: "worker", ValueDigest: "d1"},
			"TOKEN":     {ValueKind: "secret-ref", ValueMaterial: "secret://token", ValueDigest: "d2"},
		},
	}
	for _, test := range []struct{ name, got, want string }{
		{"launch-spec", CanonicalSpecDigest(spec), "a272c00ec23055295a7ccf5d6854ea7fa263b18f880f633bc0e1337dc8806e58"},
		{"V_B", ExecGuardCredentialVerifier("f_0123456789abcdef0123456789abcdef", "eb_1", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"), "ced0893885f7a8e22451cb5999cafbbab151de562e8d68294ccfe74859f2da0f"},
		{"environment value", EnvironmentValueDigest("worker"), "6fbc304613a8b171b21745d6645cf3638c793d17ecbe521560964997f4738846"},
	} {
		if test.got != test.want {
			t.Errorf("%s digest = %s, want %s", test.name, test.got, test.want)
		}
	}
}

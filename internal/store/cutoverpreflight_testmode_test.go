//go:build test

package store

import "testing"

func TestLegacyV18CutoverPlatformHonorsTestOverrides(t *testing.T) {
	t.Setenv("HAND_TEST_CUTOVER_BOOT_TOKEN", testCutoverBootB)
	t.Setenv("HAND_TEST_CUTOVER_MACHINE_ID", testCutoverMachine)
	t.Setenv("HAND_TEST_CUTOVER_FILESYSTEM", "local")
	platform := legacyV18CutoverPlatform()
	token, tokenErr := platform.bootToken()
	machine, machineErr := platform.machineID()
	if token != testCutoverBootB || machine != testCutoverMachine || tokenErr != nil || machineErr != nil {
		t.Fatalf("overrides = %q/%v, %q/%v", token, tokenErr, machine, machineErr)
	}
	if err := platform.filesystem("/nonexistent"); err != nil {
		t.Fatalf("filesystem override: %v", err)
	}
}

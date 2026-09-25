//go:build test

package store

import "os"

// Separate-process cutover tests simulate a restart or a local filesystem; untagged builds never read these.
func legacyV18CutoverPlatform() legacyV18CutoverPlatformSource {
	platform := legacyV18CutoverNativePlatform()
	if token, ok := os.LookupEnv("HAND_TEST_CUTOVER_BOOT_TOKEN"); ok {
		platform.bootToken = func() (string, error) { return token, nil }
	}
	if machine, ok := os.LookupEnv("HAND_TEST_CUTOVER_MACHINE_ID"); ok {
		platform.machineID = func() (string, error) { return machine, nil }
	}
	if os.Getenv("HAND_TEST_CUTOVER_FILESYSTEM") == "local" {
		platform.filesystem = func(string) error { return nil }
	}
	return platform
}

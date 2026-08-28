//go:build !test

package testtag

// Present is false: the fakes are compiled out, so every call reaches a real external tool.
const Present = false

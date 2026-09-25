//go:build !test

package store

func legacyV18CutoverPlatform() legacyV18CutoverPlatformSource {
	return legacyV18CutoverNativePlatform()
}

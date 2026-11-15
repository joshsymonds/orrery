package store

import "testing"

// SeedMigrated gives the store_test package the image seedMigrated writes, so the test binary migrates one image,
// not one here and another in storetest.
func SeedMigrated(t *testing.T, path string) {
	t.Helper()
	seedMigrated(t, path)
}

// NoSync gives the store_test package the Open option noSync returns.
func NoSync() Option {
	return noSync()
}

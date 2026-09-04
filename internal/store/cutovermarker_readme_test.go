package store

import (
	"path/filepath"
	"testing"
)

func TestLegacyV18CutoverMarkerNamesStayOutsideLooseEvidenceNamespace(t *testing.T) {
	home := t.TempDir()
	for _, path := range []string{legacyV18CutoverMarkerPath(home), legacyV18CutoverMarkerCandidatePath(home)} {
		if isLegacyV18CutoverLooseArtifact(filepath.Base(path)) {
			t.Fatalf("marker filename %q overlaps authority-like loose evidence namespace", filepath.Base(path))
		}
	}
}

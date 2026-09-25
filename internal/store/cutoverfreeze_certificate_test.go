package store

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// #348 revision 4 "Freeze evidence": a v1 bridge carries no E_F, so recovery never publishes from it.
func TestRecoverCanonicalV19CutoverRefusesV1FrozenBridge(t *testing.T) {
	home, bridge, _, _, _, _ := canonicalV19CutoverPublicationFixture(t)
	downgradeLegacyV18CutoverBridgeToV1(t, home, bridge.SourceSHA256)
	before, err := legacyV18CutoverFileSHA256(Path(home))
	if err != nil {
		t.Fatal(err)
	}

	state, err := recoverCanonicalV19Cutover(home)
	if !errors.Is(err, errLegacyV18CutoverRecoveryExecutionUnsafe) || state.Disposition != legacyV18CutoverRecoveryRefuse || !strings.Contains(state.Reason, "carries no committed boot evidence") {
		t.Fatalf("v1 bridge recovery = %#v, %v; want refusal", state, err)
	}
	if after, err := legacyV18CutoverFileSHA256(Path(home)); err != nil || after != before {
		t.Fatalf("v1 bridge refusal changed the active bridge: %s, %v; want %s", after, err, before)
	}
}

func TestReadLegacyV18CutoverFreezeCertificateRefusesInexactRows(t *testing.T) {
	source, manifest := strings.Repeat("a", 64), strings.Repeat("b", 64)
	evidence, err := encodeLegacyV18CutoverBootEvidence(testLegacyV18CutoverFreezeEvidence())
	if err != nil {
		t.Fatal(err)
	}
	certificate := legacyV18CutoverCertificateValue(source, manifest, evidence)
	spaced := strings.Replace(string(evidence), ":", ": ", 1)
	for name, rows := range map[string]map[string]string{
		"v2 without evidence":       {legacyV18CutoverFreezeCertificateKey: certificate},
		"evidence digest mismatch":  {legacyV18CutoverFreezeCertificateKey: certificate, legacyV18CutoverFreezeEvidenceKey: string(evidence) + " "},
		"non-canonical evidence":    {legacyV18CutoverFreezeCertificateKey: legacyV18CutoverCertificateValue(source, manifest, []byte(spaced)), legacyV18CutoverFreezeEvidenceKey: spaced},
		"extra cutover row":         {legacyV18CutoverFreezeCertificateKey: certificate, legacyV18CutoverFreezeEvidenceKey: string(evidence), "v19-cutover-extra": "x"},
		"v1 with evidence row":      {legacyV18CutoverFreezeCertificateKey: "v1:" + source, legacyV18CutoverFreezeEvidenceKey: string(evidence)},
		"unknown certificate":       {legacyV18CutoverFreezeCertificateKey: "v3:" + source},
		"certificate absent":        {legacyV18CutoverFreezeEvidenceKey: string(evidence)},
		"malformed manifest digest": {legacyV18CutoverFreezeCertificateKey: legacyV18CutoverCertificateValue(source, "B", evidence), legacyV18CutoverFreezeEvidenceKey: string(evidence)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := readLegacyV18CutoverFreezeCertificate(testLegacyV18CutoverMetaDB(t, rows)); err == nil {
				t.Fatal("inexact certificate rows were accepted")
			}
		})
	}
	got, err := readLegacyV18CutoverFreezeCertificate(testLegacyV18CutoverMetaDB(t, map[string]string{
		legacyV18CutoverFreezeCertificateKey: certificate,
		legacyV18CutoverFreezeEvidenceKey:    string(evidence),
		"fleet-id":                           "unrelated",
	}))
	if err != nil || got.Value != certificate || got.SourceSHA256 != source || got.ManifestSHA256 != manifest || got.Evidence != testLegacyV18CutoverFreezeEvidence() {
		t.Fatalf("exact v2 certificate = %+v, %v", got, err)
	}
}

// #348 revision 4 "Drift gate": device-numbered identities cannot be compared across the required restart.
func TestBuildLegacyV18CutoverManifestRefusesDeviceNumberIdentity(t *testing.T) {
	_, err := buildLegacyV18CutoverManifestProjects([]LegacyV18CutoverManifestProjectInput{{
		SourceProjectID:      "p_00000000000000000000000000000001",
		Locator:              "projects/alpha",
		RepositoryPhysicalID: "unix-v1:dev=0000000000000001:ino=0000000000000002",
		CommonDirPhysicalID:  "unix-v2:ino=0000000000000003:btime=1.000000000",
		Revision:             strings.Repeat("a", 40),
		LegacyName:           "alpha",
		LegacyURL:            "https://example.invalid/alpha.git",
		LegacyMode:           "clone",
	}})
	if err == nil || !strings.Contains(err.Error(), "not a restart-stable") {
		t.Fatalf("device-number identity = %v, want refusal", err)
	}
}

func downgradeLegacyV18CutoverBridgeToV1(t *testing.T, home, sourceSHA256 string) {
	t.Helper()
	db := openLegacyV18CutoverTestDB(t, home, true)
	defer func() { _ = db.Close() }()
	statements := []string{}
	for _, operation := range legacyV18CutoverFreezeOperations {
		statements = append(statements, "DROP TRIGGER "+legacyV18CutoverFreezeTriggerName("meta", operation))
	}
	statements = append(statements,
		"DELETE FROM meta WHERE key = '"+legacyV18CutoverFreezeEvidenceKey+"'",
		"UPDATE meta SET value = 'v1:"+sourceSHA256+"' WHERE key = '"+legacyV18CutoverFreezeCertificateKey+"'")
	for _, operation := range legacyV18CutoverFreezeOperations {
		statements = append(statements, legacyV18CutoverFreezeTriggerSQL("meta", operation))
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
}

func testLegacyV18CutoverMetaDB(t *testing.T, rows map[string]string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for key, value := range rows {
		if _, err := db.Exec(`INSERT INTO meta(key, value) VALUES(?, ?)`, key, value); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

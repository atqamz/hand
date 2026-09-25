package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	legacyV18CutoverFreezeEvidenceKey          = "v19-cutover-freeze-evidence"
	legacyV18CutoverFreezeCertificateVersionV1 = "v1"
)

// The certificate row of a frozen bridge; v1 bridges carry no boot evidence and never publish.
type legacyV18CutoverFreezeCertificate struct {
	Version        string
	Value          string
	SourceSHA256   string
	ManifestSHA256 string
	Evidence       legacyV18CutoverBootEvidence
}

func legacyV18CutoverCertificateValue(sourceSHA256, manifestSHA256 string, evidence []byte) string {
	return strings.Join([]string{legacyV18CutoverFreezeCertificateVersion, sourceSHA256, manifestSHA256, canonicalV19SHA256(evidence)}, ":")
}

func encodeLegacyV18CutoverBootEvidence(e legacyV18CutoverBootEvidence) ([]byte, error) {
	if err := validateLegacyV18CutoverBootEvidence(e); err != nil {
		return nil, err
	}
	return json.Marshal(e)
}

func decodeLegacyV18CutoverBootEvidence(payload []byte) (legacyV18CutoverBootEvidence, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var evidence legacyV18CutoverBootEvidence
	if err := decoder.Decode(&evidence); err != nil {
		return legacyV18CutoverBootEvidence{}, fmt.Errorf("decode boot evidence: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return legacyV18CutoverBootEvidence{}, fmt.Errorf("boot evidence has trailing data")
	}
	canonical, err := encodeLegacyV18CutoverBootEvidence(evidence)
	if err != nil {
		return legacyV18CutoverBootEvidence{}, err
	}
	if !bytes.Equal(canonical, payload) {
		return legacyV18CutoverBootEvidence{}, fmt.Errorf("boot evidence bytes are not canonical")
	}
	return evidence, nil
}

// Reads every v19-cutover- meta row, so an extra or missing row is refused rather than ignored.
func readLegacyV18CutoverFreezeCertificate(q sqliteQueryer) (legacyV18CutoverFreezeCertificate, error) {
	rows, err := q.Query(`SELECT key, value FROM meta WHERE substr(key, 1, 12) = 'v19-cutover-' ORDER BY key`)
	if err != nil {
		return legacyV18CutoverFreezeCertificate{}, fmt.Errorf("read freeze certificate: %w", err)
	}
	values := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			_ = rows.Close()
			return legacyV18CutoverFreezeCertificate{}, fmt.Errorf("read freeze certificate: %w", err)
		}
		values[key] = value
	}
	rowsErr := rows.Err()
	if closeErr := rows.Close(); rowsErr == nil {
		rowsErr = closeErr
	}
	if rowsErr != nil {
		return legacyV18CutoverFreezeCertificate{}, fmt.Errorf("read freeze certificate: %w", rowsErr)
	}
	value, ok := values[legacyV18CutoverFreezeCertificateKey]
	if !ok {
		return legacyV18CutoverFreezeCertificate{}, fmt.Errorf("freeze certificate is absent")
	}
	parts := strings.Split(value, ":")
	certificate := legacyV18CutoverFreezeCertificate{Version: parts[0], Value: value}
	switch {
	case parts[0] == legacyV18CutoverFreezeCertificateVersionV1 && len(parts) == 2 && len(values) == 1:
		certificate.SourceSHA256 = parts[1]
	case parts[0] == legacyV18CutoverFreezeCertificateVersion && len(parts) == 4 && len(values) == 2:
		certificate.SourceSHA256, certificate.ManifestSHA256 = parts[1], parts[2]
		evidence, ok := values[legacyV18CutoverFreezeEvidenceKey]
		if !ok || canonicalV19SHA256([]byte(evidence)) != parts[3] {
			return legacyV18CutoverFreezeCertificate{}, fmt.Errorf("freeze evidence row does not match its certificate")
		}
		if certificate.Evidence, err = decodeLegacyV18CutoverBootEvidence([]byte(evidence)); err != nil {
			return legacyV18CutoverFreezeCertificate{}, err
		}
		if err := validateLegacyV18CutoverSHA256(certificate.ManifestSHA256); err != nil {
			return legacyV18CutoverFreezeCertificate{}, fmt.Errorf("freeze certificate manifest digest: %w", err)
		}
	default:
		return legacyV18CutoverFreezeCertificate{}, fmt.Errorf("freeze certificate %q with %d cutover meta rows is not an exact v1 or v2 certificate", value, len(values))
	}
	if err := validateLegacyV18CutoverSHA256(certificate.SourceSHA256); err != nil {
		return legacyV18CutoverFreezeCertificate{}, fmt.Errorf("freeze certificate source digest: %w", err)
	}
	return certificate, nil
}

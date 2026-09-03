package store

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestProbeCanonicalV19CutoverProof(t *testing.T) {
	file, err := os.Open("../../docs/architecture/v19-proof.py.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()

	var out []string
	inside := false
	scanner := bufio.NewScanner(reader)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		if strings.HasPrefix(line, "def cutover_target_proof(") {
			inside = true
		}
		if !inside {
			continue
		}
		if len(out) > 0 && strings.HasPrefix(line, "def ") {
			break
		}
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			out = append(out, fmt.Sprintf("%04d %s", lineNumber, trimmed))
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("locked v19 proof contains no cutover_target_proof function")
	}
	t.Fatalf("locked v19 cutover proof function: %s", strings.Join(out, " || "))
}

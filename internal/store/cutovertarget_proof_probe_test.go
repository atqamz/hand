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

	var lines []string
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}

	seen := make(map[int]struct{})
	var out []string
	for i, line := range lines {
		if !strings.Contains(line, "legacy_import") {
			continue
		}
		start := i - 40
		if start < 0 {
			start = 0
		}
		end := i + 41
		if end > len(lines) {
			end = len(lines)
		}
		for j := start; j < end; j++ {
			if _, ok := seen[j]; ok {
				continue
			}
			seen[j] = struct{}{}
			out = append(out, fmt.Sprintf("%04d %s", j+1, strings.TrimSpace(lines[j])))
		}
	}
	if len(out) == 0 {
		t.Fatal("locked v19 proof contains no legacy_import reference")
	}
	t.Fatalf("locked v19 cutover proof context:\n%s", strings.Join(out, "\n"))
}

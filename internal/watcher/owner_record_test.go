package watcher

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	testGen = "0123456789abcdef0123456789abcdef"
	genA    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	genB    = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func validRecord() OwnerRecord {
	return OwnerRecord{Version: ownerRecordVersion, Generation: testGen, PID: 12345}
}

func TestOwnerRecordRoundTrips(t *testing.T) {
	data, err := json.Marshal(validRecord())
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseOwnerRecord(data)
	if err != nil {
		t.Fatalf("parseOwnerRecord: %v", err)
	}
	if got != validRecord() {
		t.Fatalf("got %+v, want %+v", got, validRecord())
	}
}

func TestNewGenerationIsLowercaseHex(t *testing.T) {
	for range 20 {
		gen, err := newGeneration()
		if err != nil {
			t.Fatal(err)
		}
		rec := validRecord()
		rec.Generation = gen
		if _, err := parseOwnerRecord(mustMarshal(t, rec)); err != nil {
			t.Fatalf("fresh generation %q rejected: %v", gen, err)
		}
	}
}

// The kind names what acquired ownership so a contended refusal can describe
// the holder truthfully - atqamz/hand#410. Empty (legacy/unrecorded) and both
// known kinds must all round-trip; only foreign values are rejected.
func TestOwnerRecordAcceptsEmptyOrKnownKind(t *testing.T) {
	for _, kind := range []string{"", OwnerKindWatch, OwnerKindBridge} {
		rec := validRecord()
		rec.Kind = kind
		got, err := parseOwnerRecord(mustMarshal(t, rec))
		if err != nil {
			t.Fatalf("kind %q rejected: %v", kind, err)
		}
		if got.Kind != kind {
			t.Fatalf("kind %q round-tripped as %q", kind, got.Kind)
		}
	}
}

func mustMarshal(t *testing.T, rec OwnerRecord) []byte {
	t.Helper()
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Every invalid record is non-actionable routing metadata: parseOwnerRecord must
// reject it so no takeover path can ever target a process or generation from it.
func TestParseOwnerRecordRejectsInvalidRecords(t *testing.T) {
	gen := testGen
	cases := []struct {
		name  string
		input string
	}{
		{"empty file", ""},
		{"partial json", `{"version":1`},
		{"malformed json", `{oops`},
		{"trailing garbage", `{"version":1,"generation":"` + gen + `","pid":1} \n extra`},
		{"wrong version", `{"version":2,"generation":"` + gen + `","pid":1}`},
		{"missing generation", `{"version":1,"pid":5}`},
		{"empty generation", `{"version":1,"generation":"","pid":5}`},
		{"short generation", `{"version":1,"generation":"abcd","pid":5}`},
		{"uppercase generation", `{"version":1,"generation":"` + strings.ToUpper(gen) + `","pid":5}`},
		{"nonhex generation", `{"version":1,"generation":"gggggggggggggggggggggggggggggggg","pid":5}`},
		{"missing pid", `{"version":1,"generation":"` + gen + `"}`},
		{"zero pid", `{"version":1,"generation":"` + gen + `","pid":0}`},
		{"negative pid", `{"version":1,"generation":"` + gen + `","pid":-3}`},
		{"noninteger pid", `{"version":1,"generation":"` + gen + `","pid":"abc"}`},
		{"unknown field", `{"version":1,"generation":"` + gen + `","pid":1,"evil":2}`},
		{"unsupported kind", `{"version":1,"generation":"` + gen + `","pid":1,"kind":"nonsense"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseOwnerRecord([]byte(tc.input)); err == nil {
				t.Fatalf("parseOwnerRecord(%q) succeeded, want rejection", tc.input)
			}
		})
	}
}

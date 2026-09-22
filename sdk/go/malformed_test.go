package simplefin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// malformedDir holds payloads every SDK must reject rather than coerce.
//
// The rule both language targets implement is Go's encoding/json semantics: a
// missing field or an explicit null yields the zero value, but a field present
// with the wrong type is an error. That second half matters most for money — a
// server sending the number 12.5 where the protocol documents a string is
// broken, and quietly rendering that as an empty balance would be worse than
// refusing the response.
const malformedDir = "../../spec/malformed"

func TestMalformedPayloadsAreRejected(t *testing.T) {
	entries, err := os.ReadDir(malformedDir)
	if err != nil {
		t.Fatalf("reading %s: %v", malformedDir, err)
	}

	ran := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}

		ran++
		t.Run(strings.TrimSuffix(name, ".json"), func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(malformedDir, name))
			if err != nil {
				t.Fatalf("reading fixture: %v", err)
			}

			set, err := ParseAccountSet(data)
			if err == nil {
				t.Fatalf("ParseAccountSet accepted a malformed payload: %+v", set)
			}
		})
	}

	if ran == 0 {
		t.Fatalf("no fixtures found in %s", malformedDir)
	}
}

// Valid payloads must still parse. Without this, a parser that rejected
// everything would pass the test above.
func TestSparsePayloadIsAccepted(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(goldenDir, "sparse.json"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	set, err := ParseAccountSet(data)
	if err != nil {
		t.Fatalf("ParseAccountSet rejected a valid payload: %v", err)
	}
	if len(set.Accounts) != 2 {
		t.Fatalf("accounts = %d, want 2", len(set.Accounts))
	}
	// An absent available-balance and an explicit null must both read as
	// absent rather than as an empty string.
	for _, account := range set.Accounts {
		if account.AvailableBalance != nil {
			t.Errorf("%s: availableBalance = %q, want nil", account.ID, *account.AvailableBalance)
		}
	}
}

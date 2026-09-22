package ir

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const specPath = "../../spec/simplefin.yaml"

func TestLoadRealSpec(t *testing.T) {
	spec, err := Load(specPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if spec.Protocol.Name != "SimpleFIN" {
		t.Errorf("protocol name = %q", spec.Protocol.Name)
	}

	// The types the protocol actually defines. A missing one here means the
	// generated SDKs are missing a type entirely.
	for _, name := range []string{
		"Error", "Connection", "Transaction", "Account", "AccountSet", "Info", "Currency",
	} {
		if _, ok := spec.TypeByName(name); !ok {
			t.Errorf("type %q is missing from the spec", name)
		}
	}

	// The v1 org shape is what closes the gap both prior SDKs had: they
	// offered version "1" but could not parse a v1 response.
	if _, ok := spec.TypeByName("OrgV1"); !ok {
		t.Error("OrgV1 is missing; protocol v1 responses could not be normalized")
	}

	for _, name := range []string{"info", "claim", "getAccounts", "resolveCurrency"} {
		if _, ok := spec.EndpointByName(name); !ok {
			t.Errorf("endpoint %q is missing from the spec", name)
		}
	}
}

// Money must never acquire a numeric type on the way through the generator.
func TestMoneyFieldsAreNumericStrings(t *testing.T) {
	spec, err := Load(specPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	money := map[string][]string{
		"Account":     {"balance", "availableBalance"},
		"Transaction": {"amount"},
	}

	for typeName, fields := range money {
		typ, ok := spec.TypeByName(typeName)
		if !ok {
			t.Fatalf("type %q missing", typeName)
		}
		for _, fieldName := range fields {
			found := false
			for _, f := range typ.Fields {
				if f.Name != fieldName {
					continue
				}
				found = true
				if f.Kind != KindNumericString {
					t.Errorf("%s.%s is %q, must be numericString", typeName, fieldName, f.Kind)
				}
			}
			if !found {
				t.Errorf("%s.%s is missing", typeName, fieldName)
			}
		}
	}
}

// Every declared prefix needs a naked form, because the protocol's stated
// fallback for an unknown subcode is the naked prefix. Without one, an
// unrecognized code would have nothing to degrade to.
func TestEveryPrefixHasANakedForm(t *testing.T) {
	spec, err := Load(specPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	naked := map[string]bool{}
	for _, c := range spec.ErrorCodes.Values {
		if c.IsNaked() {
			naked[c.Prefix()] = true
		}
	}
	for prefix := range spec.ErrorCodes.Prefixes {
		if !naked[prefix] {
			t.Errorf("prefix %q has no naked %q. form", prefix, prefix)
		}
	}
}

func TestErrorCodeValuesAreDeterministic(t *testing.T) {
	spec, err := Load(specPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	first := spec.ErrorCodeValues()
	second := spec.ErrorCodeValues()
	if len(first) != len(second) {
		t.Fatalf("length differs between calls")
	}
	for i := range first {
		if first[i].Code != second[i].Code {
			t.Fatalf("ordering differs between calls at %d", i)
		}
	}
}

// Validation must be loud. Each case here is a mistake that would otherwise
// produce a subtly wrong SDK rather than a build failure.
func TestValidationRejectsBadSpecs(t *testing.T) {
	base, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("reading spec: %v", err)
	}

	cases := []struct {
		name        string
		replace     [2]string
		wantMessage string
	}{
		{
			name:        "unknown field kind",
			replace:     [2]string{"kind: numericString\n        required: true\n        doc: The balance of the account", "kind: decimal\n        required: true\n        doc: The balance of the account"},
			wantMessage: "unknown kind",
		},
		{
			name:        "dangling type reference",
			replace:     [2]string{"kind: array:Transaction", "kind: array:Nonexistent"},
			wantMessage: "unknown type",
		},
		{
			name:        "snake_case field name",
			replace:     [2]string{"- name: availableBalance", "- name: available_balance"},
			wantMessage: "lowerCamelCase",
		},
		{
			name:        "unknown serialize rule",
			replace:     [2]string{"serialize: optInOne\n        doc: If true, no transaction data is returned.", "serialize: commaJoined\n        doc: If true, no transaction data is returned."},
			wantMessage: "unknown serialize rule",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mutated := strings.Replace(string(base), tc.replace[0], tc.replace[1], 1)
			if mutated == string(base) {
				t.Fatalf("test setup is stale: %q not found in the spec", tc.replace[0])
			}

			path := filepath.Join(t.TempDir(), "simplefin.yaml")
			if err := os.WriteFile(path, []byte(mutated), 0o644); err != nil {
				t.Fatalf("writing mutated spec: %v", err)
			}

			_, err := Load(path)
			if err == nil {
				t.Fatalf("Load accepted an invalid spec")
			}
			if !strings.Contains(err.Error(), tc.wantMessage) {
				t.Errorf("error = %v, want it to mention %q", err, tc.wantMessage)
			}
		})
	}
}

// An unrecognized key is a typo, not a comment. Accepting it silently would
// let a misspelled `requird: true` make a required field optional.
func TestUnknownKeysAreRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "simplefin.yaml")
	if err := os.WriteFile(path, []byte("protocol:\n  name: SimpleFIN\n  nonsense: true\n"), 0o644); err != nil {
		t.Fatalf("writing spec: %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("Load accepted an unknown key")
	}
}

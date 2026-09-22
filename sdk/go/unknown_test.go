package simplefin

import (
	"encoding/json"
	"testing"
)

// Real SimpleFIN servers send fields the specification never documented. A
// live beta-bridge response carried `holdings` on all 25 accounts (populated
// on 5) and `payee`, `memo` and `mcc` on all 142 transactions. Dropping them
// silently is the same failure mode as dropping v1 institution data, so they
// are captured instead of discarded.

const undocumentedFixture = `{
  "errlist": [],
  "connections": [{"conn_id": "CONN-1", "name": "Example", "org_id": "ORG-1", "sfin_url": "https://x/simplefin"}],
  "accounts": [
    {
      "id": "ACT-1", "name": "Brokerage", "conn_id": "CONN-1", "currency": "USD",
      "balance": "100.00", "balance-date": 1757635200,
      "holdings": [{"symbol": "VOO", "shares": "1.5"}],
      "transactions": [
        {"id": "TXN-1", "posted": 1757548800, "amount": "-1.00", "description": "SQ *COFFEE",
         "payee": "Blue Bottle", "memo": "", "mcc": "5814"}
      ]
    },
    {
      "id": "ACT-2", "name": "Checking", "conn_id": "CONN-1", "currency": "USD",
      "balance": "5.00", "balance-date": 1757635200,
      "holdings": []
    }
  ]
}`

func TestUndocumentedFieldsAreCaptured(t *testing.T) {
	set, err := ParseAccountSet([]byte(undocumentedFixture))
	if err != nil {
		t.Fatalf("ParseAccountSet: %v", err)
	}

	type holding struct {
		Symbol string `json:"symbol"`
		Shares string `json:"shares"`
	}

	brokerage, checking := set.Accounts[0], set.Accounts[1]

	holdings, ok := Field[[]holding](brokerage.Unknown, "holdings")
	if !ok {
		t.Fatal("holdings was not captured on the brokerage account")
	}
	if len(holdings) != 1 || holdings[0].Symbol != "VOO" {
		t.Errorf("holdings = %+v", holdings)
	}

	// An account whose holdings are empty still reports the key, so callers
	// can distinguish "no holdings" from "server never mentioned holdings".
	empty, ok := Field[[]holding](checking.Unknown, "holdings")
	if !ok {
		t.Error("holdings key missing on the checking account")
	}
	if len(empty) != 0 {
		t.Errorf("expected no holdings, got %+v", empty)
	}

	tx := brokerage.Transactions[0]
	if payee, ok := Field[string](tx.Unknown, "payee"); !ok || payee != "Blue Bottle" {
		t.Errorf("payee = %q ok=%v", payee, ok)
	}
	if mcc, ok := Field[string](tx.Unknown, "mcc"); !ok || mcc != "5814" {
		t.Errorf("mcc = %q ok=%v", mcc, ok)
	}
}

// Documented fields must not also appear in the bucket, or callers would have
// two sources of truth for the same value.
func TestDocumentedFieldsAreNotDuplicatedIntoUnknown(t *testing.T) {
	set, err := ParseAccountSet([]byte(undocumentedFixture))
	if err != nil {
		t.Fatalf("ParseAccountSet: %v", err)
	}

	account := set.Accounts[0]
	for _, documented := range []string{
		"id", "name", "conn_id", "currency", "balance", "balance-date", "transactions",
	} {
		if _, found := account.Unknown[documented]; found {
			t.Errorf("documented key %q leaked into Unknown", documented)
		}
	}

	tx := account.Transactions[0]
	for _, documented := range []string{"id", "posted", "amount", "description"} {
		if _, found := tx.Unknown[documented]; found {
			t.Errorf("documented key %q leaked into Unknown", documented)
		}
	}
}

// `org` is consumed by the v1 normalizer, so surfacing it as an undocumented
// extra would be duplication rather than discovery.
func TestV1OrgDoesNotLeakIntoUnknown(t *testing.T) {
	const v1 = `{
      "errors": [],
      "accounts": [{
        "org": {"domain": "bank.example", "name": "Bank", "sfin-url": "https://sfin.bank.example"},
        "id": "ACT-1", "name": "Checking", "currency": "USD",
        "balance": "1.00", "balance-date": 1757635200,
        "holdings": []
      }]
    }`

	set, err := ParseAccountSet([]byte(v1))
	if err != nil {
		t.Fatalf("ParseAccountSet: %v", err)
	}

	// The normalizer must still see org: one connection, and the account
	// pointing at it.
	if len(set.Connections) != 1 {
		t.Fatalf("connections = %d, want 1 — the org was not consumed", len(set.Connections))
	}
	if set.Accounts[0].ConnID != "bank.example" {
		t.Errorf("connId = %q, want bank.example", set.Accounts[0].ConnID)
	}
	if _, found := set.Accounts[0].Unknown["org"]; found {
		t.Error("org leaked into Unknown despite being consumed by the normalizer")
	}
	// But a genuinely undocumented sibling key is still captured.
	if _, found := set.Accounts[0].Unknown["holdings"]; !found {
		t.Error("holdings was not captured on a v1 account")
	}
}

// Field must not fail the whole response when an undocumented field turns out
// to be a different shape than the caller expected.
func TestFieldDegradesOnShapeMismatch(t *testing.T) {
	unknown := map[string]json.RawMessage{"holdings": json.RawMessage(`"not-an-array"`)}

	if _, ok := Field[[]string](unknown, "holdings"); ok {
		t.Error("Field accepted a value that does not fit the requested type")
	}
	if _, ok := Field[string](unknown, "absent"); ok {
		t.Error("Field reported success for a missing key")
	}
}

package gen

import "testing"

// Go's initialism convention is the difference between a generated SDK that
// reads as hand-written and one that trips every linter in the repo importing
// it. These cases are the ones the SimpleFIN spec actually produces.
func TestGoName(t *testing.T) {
	cases := map[string]string{
		"id":               "ID",
		"connId":           "ConnID",
		"orgId":            "OrgID",
		"accountId":        "AccountID",
		"orgUrl":           "OrgURL",
		"sfinUrl":          "SfinURL",
		"url":              "URL",
		"availableBalance": "AvailableBalance",
		"balanceDate":      "BalanceDate",
		"transactedAt":     "TransactedAt",
		"balancesOnly":     "BalancesOnly",
		"getAccounts":      "GetAccounts",
		"msg":              "Msg",
		"errlist":          "Errlist",
		"":                 "",
	}

	for input, want := range cases {
		if got := GoName(input); got != want {
			t.Errorf("GoName(%q) = %q, want %q", input, got, want)
		}
	}
}

// Error would shadow the JavaScript built-in inside every consuming module,
// so the TypeScript target renames it. Go has no such conflict.
func TestTSTypeName(t *testing.T) {
	cases := map[string]string{
		"Error":       "SimpleFinError",
		"Account":     "Account",
		"AccountSet":  "AccountSet",
		"Connection":  "Connection",
		"Transaction": "Transaction",
		"OrgV1":       "OrgV1",
	}

	for input, want := range cases {
		if got := TSTypeName(input); got != want {
			t.Errorf("TSTypeName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestTSUnion(t *testing.T) {
	t.Run("closed union lists only the literals", func(t *testing.T) {
		got := tsUnion([]string{"1", "2"}, false)
		want := `"1" | "2"`
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	// The open-ended form is required by the protocol: consumers must tolerate
	// subcodes this SDK has never seen. `(string & {})` keeps the known
	// literals in the editor's completion list while still accepting any
	// string, which a bare `string` would not do.
	t.Run("open-ended union keeps completions and accepts any string", func(t *testing.T) {
		got := tsUnion([]string{"gen.", "con."}, true)
		want := `"gen." | "con." | (string & {})`
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

func TestConstName(t *testing.T) {
	if got := ConstName("ErrCode", "ConnectionAuth"); got != "ErrCodeConnectionAuth" {
		t.Errorf("got %q", got)
	}
}

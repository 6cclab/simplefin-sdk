package simplefin

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// These tests run against a real httptest.Server rather than a mocked HTTP
// client. That is a deliberate constraint carried over from the SDKs this one
// replaces: a prior implementation's most expensive defect survived 52 green
// tests because every one of them mocked the transport, so the bug lived in
// the part no test ever executed.

const accountsFixture = `{
  "errlist": [{"code": "con.auth", "msg": "Example Bank needs re-authentication", "conn_id": "CONN-1"}],
  "connections": [
    {"conn_id": "CONN-1", "name": "Example Bank", "org_id": "ORG-1",
     "org_url": "https://examplebank.com", "sfin_url": "https://bridge.example/simplefin"}
  ],
  "accounts": [
    {"id": "ACT-001", "name": "Checking", "conn_id": "CONN-1", "currency": "USD",
     "balance": "1250.33", "available-balance": "1200.00", "balance-date": 1757635200,
     "transactions": [
       {"id": "TXN-001", "posted": 1757548800, "amount": "-33.45",
        "description": "SQ *BLUE BOTTLE COFF #47"},
       {"id": "TXN-002", "posted": 0, "transacted_at": 1757462400, "amount": "-120.00",
        "description": "ACH DEBIT - UTILITY", "pending": true}
     ]}
  ]
}`

// newTestClient wires a Client to a test server, preserving the userinfo that
// the Access URL format requires.
func newTestClient(t *testing.T, handler http.HandlerFunc, userinfo string) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parsing test server URL: %v", err)
	}
	accessURL := u.Scheme + "://" + userinfo + "@" + u.Host + "/simplefin"

	client, err := New(accessURL, WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client, srv
}

// TestCredentialsAreSentAsHeaderNotInURL is the central security property.
//
// The password is URL-encoded in the Access URL ("p%40ss" is "p@ss"), so this
// also pins that the credentials are percent-decoded before being base64'd —
// sending the still-encoded form would authenticate as a different password.
func TestCredentialsAreSentAsHeaderNotInURL(t *testing.T) {
	var gotAuth, gotURL, gotUserinfo string

	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotURL = r.URL.String()
		if r.URL.User != nil {
			gotUserinfo = r.URL.User.String()
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(accountsFixture))
	}, "user123:p%40ss")

	if _, err := client.GetAccounts(context.Background(), GetAccountsParams{}); err != nil {
		t.Fatalf("GetAccounts: %v", err)
	}

	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("user123:p@ss"))
	if gotAuth != wantAuth {
		t.Errorf("Authorization header = %q, want %q", gotAuth, wantAuth)
	}
	if gotUserinfo != "" {
		t.Errorf("request URL carried userinfo %q, want none", gotUserinfo)
	}
	for _, secret := range []string{"user123", "p@ss", "p%40ss"} {
		if strings.Contains(gotURL, secret) {
			t.Errorf("request URL %q contains credential %q", gotURL, secret)
		}
	}
}

// TestErrorsDoNotLeakCredentials checks the failure paths, which are where
// credentials most often escape: error strings get logged verbatim.
func TestErrorsDoNotLeakCredentials(t *testing.T) {
	const user, pass = "secretuser", "secretpass"

	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"server error", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}},
		{"forbidden", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}},
		{"non-JSON body", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html><body>" + user + ":" + pass + "</body></html>"))
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := newTestClient(t, tc.handler, user+":"+pass)

			_, err := client.GetAccounts(context.Background(), GetAccountsParams{})
			if err == nil {
				t.Fatal("expected an error")
			}
			msg := err.Error()
			if strings.Contains(msg, user) || strings.Contains(msg, pass) {
				t.Errorf("error message leaks credentials: %q", msg)
			}
		})
	}
}

// TestHTMLErrorPageIsNotAParseError pins the behaviour that motivated
// checking status before decoding: the SimpleFIN Bridge answers a rejected
// request with an HTML page, and reporting that as a JSON syntax error would
// send a caller hunting for a parser bug instead of an expired Access URL.
func TestHTMLErrorPageIsNotAParseError(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<!doctype html><html><body><h1>Forbidden</h1></body></html>"))
	}, "user:pass")

	_, err := client.GetAccounts(context.Background(), GetAccountsParams{})
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("got %v, want ErrAuth", err)
	}
	if strings.Contains(err.Error(), "JSON") || strings.Contains(err.Error(), "json") {
		t.Errorf("a 403 HTML page was reported as a parse problem: %q", err)
	}
}

func TestQueryParameters(t *testing.T) {
	ts := func(v int64) *int64 { return &v }

	cases := []struct {
		name   string
		params GetAccountsParams
		want   url.Values
	}{
		{
			name:   "zero value sends only the version",
			params: GetAccountsParams{},
			want:   url.Values{"version": {"2"}},
		},
		{
			name:   "pending is opt-in and sent as 1",
			params: GetAccountsParams{Pending: true},
			want:   url.Values{"version": {"2"}, "pending": {"1"}},
		},
		{
			// A false flag must be absent entirely. Sending "0" is not the
			// same thing to a server that checks only for presence.
			name:   "false flags are omitted entirely",
			params: GetAccountsParams{Pending: false, BalancesOnly: false},
			want:   url.Values{"version": {"2"}},
		},
		{
			name:   "dates are raw unix seconds",
			params: GetAccountsParams{StartDate: ts(1757462400), EndDate: ts(1757635200)},
			want: url.Values{
				"version":    {"2"},
				"start-date": {"1757462400"},
				"end-date":   {"1757635200"},
			},
		},
		{
			// Comma-joining would be read as one account id containing a comma.
			name:   "account filters repeat the key",
			params: GetAccountsParams{Accounts: []string{"ACT-1", "ACT-2"}},
			want:   url.Values{"version": {"2"}, "account": {"ACT-1", "ACT-2"}},
		},
		{
			name:   "explicit version overrides the default",
			params: GetAccountsParams{Version: ProtocolVersion1},
			want:   url.Values{"version": {"1"}},
		},
		{
			name:   "balances-only is opt-in and sent as 1",
			params: GetAccountsParams{BalancesOnly: true},
			want:   url.Values{"version": {"2"}, "balances-only": {"1"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got url.Values
			client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				got = r.URL.Query()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"errlist":[],"connections":[],"accounts":[]}`))
			}, "u:p")

			if _, err := client.GetAccounts(context.Background(), tc.params); err != nil {
				t.Fatalf("GetAccounts: %v", err)
			}
			if got.Encode() != tc.want.Encode() {
				t.Errorf("query = %q, want %q", got.Encode(), tc.want.Encode())
			}
		})
	}
}

func TestStatusMapping(t *testing.T) {
	cases := []struct {
		status int
		check  func(error) bool
		desc   string
	}{
		{http.StatusForbidden, func(e error) bool { return errors.Is(e, ErrAuth) }, "ErrAuth"},
		{http.StatusPaymentRequired, func(e error) bool { return errors.Is(e, ErrPaymentRequired) }, "ErrPaymentRequired"},
		{http.StatusInternalServerError, func(e error) bool {
			var he *HTTPError
			return errors.As(e, &he) && he.Status == 500
		}, "*HTTPError{500}"},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}, "u:p")

			_, err := client.GetAccounts(context.Background(), GetAccountsParams{})
			if err == nil || !tc.check(err) {
				t.Errorf("status %d gave %v, want %s", tc.status, err, tc.desc)
			}
		})
	}
}

func TestNewRejectsBadURLs(t *testing.T) {
	for _, bad := range []string{"", "not a url", "ftp://example.org/simplefin", "://", "https://"} {
		t.Run(bad, func(t *testing.T) {
			if _, err := New(bad); err == nil {
				t.Errorf("New(%q) succeeded, want an error", bad)
			}
		})
	}
}

// TestNewErrorDoesNotEchoInput guards the reason url.Parse errors are not
// wrapped: Go's parse errors quote the input, which is the Access URL.
func TestNewErrorDoesNotEchoInput(t *testing.T) {
	_, err := New("https://user:hunter2@exa mple.com/simplefin")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("error echoes the input credentials: %q", err)
	}
}

func TestParsesFixture(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(accountsFixture))
	}, "u:p")

	set, err := client.GetAccounts(context.Background(), GetAccountsParams{Pending: true})
	if err != nil {
		t.Fatalf("GetAccounts: %v", err)
	}

	if len(set.Accounts) != 1 || set.Accounts[0].ID != "ACT-001" {
		t.Fatalf("accounts = %+v", set.Accounts)
	}
	if set.Accounts[0].AvailableBalance == nil || *set.Accounts[0].AvailableBalance != "1200.00" {
		t.Errorf("availableBalance = %v, want 1200.00", set.Accounts[0].AvailableBalance)
	}
	// Money must survive as the exact string the server sent.
	if got := set.Accounts[0].Balance; got != "1250.33" {
		t.Errorf("balance = %q, want %q", got, "1250.33")
	}
	if len(set.Errlist) != 1 || set.Errlist[0].Code != ErrCodeConnectionAuth {
		t.Errorf("errlist = %+v", set.Errlist)
	}
	if set.Errlist[0].ConnID != "CONN-1" {
		t.Errorf("errlist[0].ConnID = %q, want CONN-1", set.Errlist[0].ConnID)
	}
}

func TestTransactionTimes(t *testing.T) {
	cases := []struct {
		name    string
		tx      Transaction
		wantSet bool
		wantSec int64
	}{
		{"posted wins", Transaction{Posted: 1757548800, TransactedAt: 1757462400}, true, 1757548800},
		{"pending falls back to transacted_at", Transaction{Posted: 0, TransactedAt: 1757462400}, true, 1757462400},
		{"neither set is nil", Transaction{}, false, 0},
		{"negative is treated as absent", Transaction{Posted: -1}, false, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.tx.PostedTime()
			if !tc.wantSet {
				if got != nil {
					t.Fatalf("PostedTime() = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("PostedTime() = nil, want a time")
			}
			if got.Unix() != tc.wantSec {
				t.Errorf("PostedTime().Unix() = %d, want %d", got.Unix(), tc.wantSec)
			}
			if got.Location() != timeUTC() {
				t.Errorf("PostedTime() is not UTC: %v", got.Location())
			}
		})
	}
}

func TestInfo(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/simplefin/info" {
			t.Errorf("path = %q, want /simplefin/info", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"versions":["1","2"]}`))
	}, "u:p")

	info, err := client.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if len(info.Versions) != 2 || info.Versions[0] != "1" || info.Versions[1] != "2" {
		t.Errorf("versions = %v", info.Versions)
	}
}

// TestResolveCurrencyDoesNotSendCredentials matters because a custom currency
// URL is chosen by the server and points at an arbitrary third-party host.
// Attaching the Access URL's credentials would hand them to that host.
func TestResolveCurrencyDoesNotSendCredentials(t *testing.T) {
	var gotAuth string
	currencySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"Example Airline Miles","abbr":"miles"}`))
	}))
	t.Cleanup(currencySrv.Close)

	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {}, "secretuser:secretpass")
	client.httpClient = currencySrv.Client()

	currency, err := client.ResolveCurrency(context.Background(), currencySrv.URL+"/flight-miles")
	if err != nil {
		t.Fatalf("ResolveCurrency: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("credentials sent to third-party currency host: %q", gotAuth)
	}
	if currency.Abbr != "miles" {
		t.Errorf("abbr = %q, want miles", currency.Abbr)
	}
}

func TestIsCustomCurrency(t *testing.T) {
	cases := map[string]bool{
		"USD":                                  false,
		"ZMW":                                  false,
		"":                                     false,
		"https://www.example.com/flight-miles": true,
		"http://example.com/points":            true,
	}
	for input, want := range cases {
		if got := IsCustomCurrency(input); got != want {
			t.Errorf("IsCustomCurrency(%q) = %v, want %v", input, got, want)
		}
	}
}

// timeUTC returns the UTC location, used to assert that every time the SDK
// returns is normalized rather than carrying the host's zone.
func timeUTC() *time.Location { return time.UTC }

package simplefin

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeSetupToken(t *testing.T) {
	const claimURL = "https://bridge.example/simplefin/claim/abc123"
	token := base64.StdEncoding.EncodeToString([]byte(claimURL))

	t.Run("decodes", func(t *testing.T) {
		got, err := DecodeSetupToken(token)
		if err != nil {
			t.Fatalf("DecodeSetupToken: %v", err)
		}
		if got != claimURL {
			t.Errorf("got %q, want %q", got, claimURL)
		}
	})

	// Users paste these by hand out of a browser, so stray whitespace is the
	// normal case rather than the exceptional one.
	t.Run("tolerates surrounding whitespace", func(t *testing.T) {
		got, err := DecodeSetupToken("  \n\t" + token + "\n  ")
		if err != nil {
			t.Fatalf("DecodeSetupToken: %v", err)
		}
		if got != claimURL {
			t.Errorf("got %q, want %q", got, claimURL)
		}
	})

	t.Run("rejects bad input", func(t *testing.T) {
		bad := map[string]string{
			"empty":               "",
			"whitespace only":     "   ",
			"not base64":          "!!!not base64!!!",
			"base64 of nonsense":  base64.StdEncoding.EncodeToString([]byte("hello there")),
			"base64 of a non-URL": base64.StdEncoding.EncodeToString([]byte("ftp://example.org/claim")),
		}
		for name, input := range bad {
			t.Run(name, func(t *testing.T) {
				if _, err := DecodeSetupToken(input); err == nil {
					t.Errorf("DecodeSetupToken(%q) succeeded, want an error", input)
				}
			})
		}
	})
}

func TestClaim(t *testing.T) {
	const accessURL = "https://user:pass@bridge.example/simplefin"

	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		// A trailing newline is common from servers that echo the URL.
		_, _ = w.Write([]byte(accessURL + "\n"))
	}))
	t.Cleanup(srv.Close)

	token := base64.StdEncoding.EncodeToString([]byte(srv.URL + "/claim/abc"))
	got, err := Claim(context.Background(), token, WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if got != accessURL {
		t.Errorf("access URL = %q, want %q", got, accessURL)
	}
}

func TestClaimErrors(t *testing.T) {
	t.Run("403 maps to ErrAuth", func(t *testing.T) {
		// Per the protocol checklist, a 403 here can mean the token was
		// already claimed by someone else and the user's data is compromised,
		// so it must be distinguishable from any other failure.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		t.Cleanup(srv.Close)

		token := base64.StdEncoding.EncodeToString([]byte(srv.URL + "/claim/abc"))
		_, err := Claim(context.Background(), token, WithHTTPClient(srv.Client()))
		if !errors.Is(err, ErrAuth) {
			t.Errorf("got %v, want ErrAuth", err)
		}
	})

	t.Run("non-URL body is rejected without echoing it", func(t *testing.T) {
		const secret = "oops-this-might-be-a-credential"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(secret))
		}))
		t.Cleanup(srv.Close)

		token := base64.StdEncoding.EncodeToString([]byte(srv.URL + "/claim/abc"))
		_, err := Claim(context.Background(), token, WithHTTPClient(srv.Client()))
		if err == nil {
			t.Fatal("expected an error")
		}
		if !strings.Contains(err.Error(), "access URL") {
			t.Errorf("error = %q, want it to mention the access URL", err)
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error echoes the response body: %q", err)
		}
	})
}

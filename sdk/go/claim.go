package simplefin

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// maxClaimBodyBytes caps the claim response read. The body is a single URL.
const maxClaimBodyBytes = 64 << 10 // 64 KiB

// DecodeSetupToken decodes a SimpleFIN Token into the claim URL it points at.
//
// A SimpleFIN Token is a Base64-encoded URL. Surrounding whitespace is
// tolerated because users paste these by hand out of a browser.
func DecodeSetupToken(setupToken string) (string, error) {
	trimmed := strings.TrimSpace(setupToken)
	if trimmed == "" {
		return "", fmt.Errorf("simplefin: setup token is empty")
	}

	decoded, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		return "", fmt.Errorf("simplefin: setup token is not valid base64")
	}

	claimURL := strings.TrimSpace(string(decoded))
	if !strings.HasPrefix(claimURL, "http://") && !strings.HasPrefix(claimURL, "https://") {
		return "", fmt.Errorf("simplefin: setup token did not decode to a URL")
	}
	return claimURL, nil
}

// Claim exchanges a SimpleFIN Token for an Access URL.
//
// This is one-shot. The server consumes the token whether or not the caller
// successfully stores the result, so a failure here means a new setup token
// must be generated — never retry with the same one.
//
// A 403 means the token either does not exist or has already been claimed by
// someone else. The protocol's application checklist requires notifying the
// user in that case, because it can mean their transaction information has
// been compromised.
//
// Store the returned Access URL at least as securely as the financial data
// itself: it embeds Basic Auth credentials and is the only thing standing
// between a reader and the user's accounts.
func Claim(ctx context.Context, setupToken string, opts ...Option) (string, error) {
	claimURL, err := DecodeSetupToken(setupToken)
	if err != nil {
		return "", err
	}

	c := &Client{httpClient: defaultHTTPClient()}
	for _, opt := range opts {
		opt(c)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, claimURL, nil)
	if err != nil {
		return "", fmt.Errorf("simplefin: could not build claim request")
	}
	req.Header.Set("Content-Length", "0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Not wrapping err: transport errors embed the claim URL, which is a
		// one-time secret.
		return "", fmt.Errorf("simplefin: claim request failed")
	}
	defer resp.Body.Close()

	if err := statusError(resp.StatusCode, "POST claim"); err != nil {
		return "", err
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxClaimBodyBytes))
	if err != nil {
		return "", fmt.Errorf("simplefin: could not read claim response")
	}

	accessURL := strings.TrimSpace(string(body))
	if !strings.HasPrefix(accessURL, "http://") && !strings.HasPrefix(accessURL, "https://") {
		// Deliberately not echoing the body: a partial or garbled response can
		// still contain the credentials it was meant to deliver.
		return "", fmt.Errorf("simplefin: claim response was not an access URL")
	}
	return accessURL, nil
}

func defaultHTTPClient() *http.Client {
	return &http.Client{Timeout: DefaultTimeout}
}

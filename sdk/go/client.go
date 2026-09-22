// Package simplefin is a client for the SimpleFIN protocol
// (https://www.simplefin.org/protocol.html).
//
// It speaks protocol types only and knows nothing about any particular
// application's domain: no cents, no database, no budgets. Amounts are exposed
// exactly as the server sent them — as strings — because SimpleFIN transmits
// them that way precisely to avoid the precision loss of a binary float.
//
// The data types, error codes, query parameter serialization and the v1/v2
// normalizer in this package are generated from spec/simplefin.yaml. Only the
// HTTP plumbing in this file, claim.go, and their tests are hand-written.
package simplefin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultTimeout bounds a single SimpleFIN request.
const DefaultTimeout = 60 * time.Second

// maxBodyBytes caps how much of a response body is read. SimpleFIN responses
// are small; an unbounded read would let a misbehaving or hostile server
// exhaust memory.
const maxBodyBytes = 32 << 20 // 32 MiB

// Client talks to one SimpleFIN Access URL.
type Client struct {
	baseURL    string
	authHeader string
	httpClient *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient overrides the HTTP client, primarily for tests.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.httpClient = h }
}

// New builds a Client from a SimpleFIN Access URL.
//
// An Access URL embeds Basic Auth credentials
// (https://user:pass@host/simplefin). Those credentials are split out of the
// URL and sent as an Authorization header instead of being left in the request
// URL, so they cannot leak into request logs, redirect targets, or error
// strings. Node's fetch rejects userinfo in a URL outright, so doing this
// keeps every language target behaving identically as well.
func New(accessURL string, opts ...Option) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(accessURL))
	if err != nil {
		// Deliberately not wrapping err: url.Parse errors quote the input,
		// which would put the credentials into the message.
		return nil, fmt.Errorf("simplefin: access URL is not a valid URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("simplefin: access URL must be http or https")
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("simplefin: access URL has no host")
	}

	var authHeader string
	if u := parsed.User; u != nil {
		password, _ := u.Password()
		raw := u.Username() + ":" + password
		authHeader = "Basic " + base64.StdEncoding.EncodeToString([]byte(raw))
	}
	parsed.User = nil

	c := &Client{
		baseURL:    strings.TrimSuffix(parsed.String(), "/"),
		authHeader: authHeader,
		httpClient: &http.Client{Timeout: DefaultTimeout},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// GetAccounts fetches accounts, balances, and transactions.
//
// Pass Pending: true to receive pending transactions; the server omits them by
// default. A 403 means authentication failed or access was revoked — SimpleFIN
// auth is all-or-nothing per Access URL, so it is the signal to re-claim the
// whole Access URL rather than relink a single connection.
func (c *Client) GetAccounts(ctx context.Context, params GetAccountsParams) (*AccountSet, error) {
	endpoint, err := url.Parse(c.baseURL + "/accounts")
	if err != nil {
		return nil, fmt.Errorf("simplefin: could not build accounts URL")
	}
	endpoint.RawQuery = params.Encode().Encode()

	var raw rawAccountSet
	if err := c.getJSON(ctx, endpoint.String(), "GET /accounts", true, &raw); err != nil {
		return nil, err
	}
	return normalizeAccountSet(&raw), nil
}

// Info reports which protocol versions the server supports.
//
// Note that a server is not obliged to make this useful: the SimpleFIN Bridge
// currently redirects /info to its marketing homepage rather than returning
// JSON, which surfaces here as a decode error.
func (c *Client) Info(ctx context.Context) (*Info, error) {
	var info Info
	if err := c.getJSON(ctx, c.baseURL+"/info", "GET /info", false, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// ResolveCurrency fetches a custom currency definition.
//
// When an Account's Currency is a URL rather than an ISO 4217 code, it
// identifies a custom currency such as frequent flyer miles or reward points.
// All strings returned here must be sanitized before being displayed.
func (c *Client) ResolveCurrency(ctx context.Context, currencyURL string) (*Currency, error) {
	parsed, err := url.Parse(strings.TrimSpace(currencyURL))
	if err != nil {
		return nil, fmt.Errorf("simplefin: currency URL is not a valid URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("simplefin: currency URL must be http or https")
	}

	var currency Currency
	// A custom currency URL points at an arbitrary third-party host, so the
	// Access URL credentials must not be attached to it.
	if err := c.getJSON(ctx, parsed.String(), "GET currency", false, &currency); err != nil {
		return nil, err
	}
	return &currency, nil
}

// IsCustomCurrency reports whether an Account's Currency is a custom currency
// URL rather than an ISO 4217 code, and so needs ResolveCurrency.
func IsCustomCurrency(currency string) bool {
	c := strings.TrimSpace(currency)
	return strings.HasPrefix(c, "https://") || strings.HasPrefix(c, "http://")
}

// getJSON performs a GET and decodes a JSON body into out.
//
// The status is checked before any decode is attempted. The SimpleFIN Bridge
// serves HTML error pages, so decoding first would turn a plain 403 into a
// confusing JSON syntax error and hide the real cause.
func (c *Client) getJSON(ctx context.Context, endpoint, op string, auth bool, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("simplefin: could not build %s request", op)
	}
	req.Header.Set("Accept", "application/json")
	if auth && c.authHeader != "" {
		req.Header.Set("Authorization", c.authHeader)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Not wrapping err: transport errors embed the request URL, which for
		// an authenticated call is the Access URL.
		return fmt.Errorf("simplefin: %s request failed", op)
	}
	defer resp.Body.Close()

	if err := statusError(resp.StatusCode, op); err != nil {
		return err
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return fmt.Errorf("simplefin: could not read %s response", op)
	}
	if err := json.Unmarshal(body, out); err != nil {
		// Report the content type rather than the body: the body of a
		// misrouted authenticated request can echo back credentials.
		return fmt.Errorf("simplefin: %s returned a body that is not JSON (content-type %q)",
			op, resp.Header.Get("Content-Type"))
	}
	return nil
}

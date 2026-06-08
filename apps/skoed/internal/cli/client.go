package cli

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is the auth-aware HTTP client used by every authenticated
// subcommand. Wraps net/http with Basic Auth + a sensible timeout.
type Client struct {
	creds Credentials
	hc    *http.Client
}

// NewClient builds a Client from resolved credentials.
func NewClient(creds Credentials) *Client {
	return &Client{
		creds: creds,
		hc: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				// M4.6 HTTPS uses a self-signed cert; the CLI talks to
				// the same operator's box so InsecureSkipVerify is the
				// pragmatic choice. Operators with public-cert deployments
				// would override the HTTP client; revisit when needed.
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec — operator-tools posture
			},
		},
	}
}

// GetJSON issues GET <api>/<path> and decodes the JSON body into v.
func (c *Client) GetJSON(path string, v any) error {
	return c.do(http.MethodGet, path, nil, v)
}

// PostJSON issues POST <api>/<path> with body and decodes into v.
func (c *Client) PostJSON(path string, body, v any) error {
	return c.do(http.MethodPost, path, body, v)
}

func (c *Client) do(method, path string, body, out any) error {
	url := strings.TrimRight(c.creds.APIURL, "/") + path
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.creds.Username != "" {
		req.SetBasicAuth(c.creds.Username, c.creds.Password)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	buf, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s %s: status %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(buf)))
	}
	if out != nil {
		if err := json.Unmarshal(buf, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// APIURL returns the resolved API base URL — useful for echoing in
// command output (e.g. token-create shows the leader address).
func (c *Client) APIURL() string { return c.creds.APIURL }

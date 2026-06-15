package cli

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Client is the auth-aware HTTP client used by every authenticated
// subcommand. Exchanges username+password for a session Bearer token on
// the first authenticated request, then reuses it for the session lifetime.
type Client struct {
	creds Credentials
	hc    *http.Client

	tokenOnce sync.Once
	token     string
	tokenErr  error
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

// login calls POST /api/v1/auth/login and caches the session token.
func (c *Client) login() error {
	c.tokenOnce.Do(func() {
		if c.creds.Username == "" {
			return
		}
		payload, err := json.Marshal(map[string]string{
			"username": c.creds.Username,
			"password": c.creds.Password,
		})
		if err != nil {
			c.tokenErr = err
			return
		}
		url := strings.TrimRight(c.creds.APIURL, "/") + "/api/v1/auth/login"
		req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(payload)))
		if err != nil {
			c.tokenErr = err
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.hc.Do(req)
		if err != nil {
			c.tokenErr = err
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			buf, _ := io.ReadAll(resp.Body)
			c.tokenErr = fmt.Errorf("login failed (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(buf)))
			return
		}
		var body struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			c.tokenErr = fmt.Errorf("decode login response: %w", err)
			return
		}
		c.token = body.Token
	})
	return c.tokenErr
}

func (c *Client) do(method, path string, body, out any) error {
	if err := c.login(); err != nil {
		return err
	}
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
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
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

// Acceptance tests for M7 API bearer token authentication.
//
// FSIDs covered:
//   FS-TokenMintReturnsValueOnce, FS-TokenMintRequiresClusterAdminScope,
//   FS-TokenListHidesRawValue, FS-TokenRevocationRejectsAuth,
//   FS-TokenBearerAuthenticatesRequest, FS-TokenReadScopeBlocksWrites,
//   FS-TokenWriteScopeAllowsMutations, FS-TokenClusterAdminScopeRequiredForMint,
//   FS-TokenExpiredRejected, FS-TokenPatchUpdatesLabel, FS-TokenAuditEntryRecordsTokenId,
//   FS-TokenDefaultScopeReadWrite, FS-TokenUnknownScopeRejected

package acceptance

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

// ── helpers ───────────────────────────────────────────────────────────────────

type mintResp struct {
	ID        string     `json:"id"`
	Token     string     `json:"token"`
	Label     string     `json:"label"`
	Scopes    []string   `json:"scopes"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at"`
}

type tokenEntry struct {
	ID         string     `json:"id"`
	Label      string     `json:"label"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
}

// mintToken creates a bearer token via Basic Auth and returns the mint response.
func mintToken(t *testing.T, n *Node, label string, scopes []string) mintResp {
	t.Helper()
	body := map[string]any{"label": label}
	if len(scopes) > 0 {
		body["scopes"] = scopes
	}
	resp := n.apiDo(t, "POST", "/api/v1/tokens", mustJSON(t, body))
	defer resp.Body.Close()
	raw := drainBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("mint token: expected 201, got %d: %s", resp.StatusCode, raw)
	}
	var m mintResp
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("decode mint response: %v", err)
	}
	return m
}

// drainBody reads and returns the response body without closing it (leaves
// that to the caller's defer). Used in helpers that decode or inspect the body
// before returning the full text for error messages.
func drainBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// FS-TokenMintReturnsValueOnce
// POST /api/v1/tokens returns the raw token in the 201 body exactly once.
// A subsequent GET /api/v1/tokens list must NOT expose the raw value.
func TestTokenMintReturnsValueOnce(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})

	m := mintToken(t, n, "ci-token", []string{"read", "write"})

	if m.Token == "" {
		t.Fatal("mint response must include a non-empty token field")
	}
	if m.ID == "" {
		t.Fatal("mint response must include a non-empty id field")
	}

	// List must not expose any raw token value.
	listResp := n.apiDo(t, "GET", "/api/v1/tokens", "")
	defer listResp.Body.Close()
	assertStatus(t, listResp, http.StatusOK)

	var entries []tokenEntry
	if err := json.NewDecoder(listResp.Body).Decode(&entries); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.ID == m.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("minted token %s not found in list", m.ID)
	}
	// Raw token must not appear anywhere in the JSON representation.
	raw := mustJSON(t, entries)
	if strContains(raw, m.Token) {
		t.Fatal("list response must not contain the raw token value")
	}
}

// FS-TokenBearerAuthenticatesRequest
// A request authenticated with a valid Bearer token returns 200.
func TestTokenBearerAuthenticatesRequest(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})

	m := mintToken(t, n, "bearer-test", []string{"read", "write"})

	resp := n.apiDoBearer(t, "GET", "/api/v1/blocklists", "", m.Token)
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusOK)
}

// FS-TokenRevocationRejectsAuth
// After DELETE /api/v1/tokens/{id}, the revoked token returns 401.
func TestTokenRevocationRejectsAuth(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})

	m := mintToken(t, n, "revoke-me", []string{"read", "write", "cluster:admin"})

	// Confirm it works before revocation.
	resp := n.apiDoBearer(t, "GET", "/api/v1/blocklists", "", m.Token)
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusOK)

	// Revoke.
	delResp := n.apiDo(t, "DELETE", "/api/v1/tokens/"+m.ID, "")
	defer delResp.Body.Close()
	assertStatus(t, delResp, http.StatusNoContent)

	// Must now return 401.
	resp2 := n.apiDoBearer(t, "GET", "/api/v1/blocklists", "", m.Token)
	defer resp2.Body.Close()
	assertStatus(t, resp2, http.StatusUnauthorized)
}

// FS-TokenReadScopeBlocksWrites
// A token with only the "read" scope must get 403 on mutating endpoints.
func TestTokenReadScopeBlocksWrites(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})

	m := mintToken(t, n, "read-only", []string{"read"})

	// GET is allowed.
	getResp := n.apiDoBearer(t, "GET", "/api/v1/blocklists", "", m.Token)
	defer getResp.Body.Close()
	assertStatus(t, getResp, http.StatusOK)

	// POST must be forbidden.
	body := mustJSON(t, map[string]any{
		"name": "test", "url": "https://example.com/list.txt", "enabled": true,
	})
	postResp := n.apiDoBearer(t, "POST", "/api/v1/blocklists", body, m.Token)
	defer postResp.Body.Close()
	assertStatus(t, postResp, http.StatusForbidden)
}

// FS-TokenWriteScopeAllowsMutations
// A token with "write" scope can successfully call mutating endpoints.
func TestTokenWriteScopeAllowsMutations(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})

	m := mintToken(t, n, "writer", []string{"read", "write"})

	body := mustJSON(t, map[string]any{
		"name": "scopetest", "url": "https://example.com/list.txt", "enabled": false,
	})
	resp := n.apiDoBearer(t, "POST", "/api/v1/blocklists", body, m.Token)
	defer resp.Body.Close()
	// 201 Created or 409 Conflict are both fine; 403 would indicate a scope bug.
	if resp.StatusCode == http.StatusForbidden {
		t.Fatalf("write-scoped token must not get 403 on POST /api/v1/blocklists")
	}
}

// FS-TokenClusterAdminScopeRequiredForMint
// A token without cluster:admin scope must get 403 when trying to mint tokens.
func TestTokenClusterAdminScopeRequiredForMint(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})

	// Mint a read+write token (no cluster:admin).
	m := mintToken(t, n, "no-admin", []string{"read", "write"})

	// Attempt to mint another token — must be forbidden.
	body := mustJSON(t, map[string]any{"label": "sub-token"})
	resp := n.apiDoBearer(t, "POST", "/api/v1/tokens", body, m.Token)
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusForbidden)
}

// FS-TokenDefaultScopeReadWrite
// When no scopes are specified on mint, the token is given read+write by default.
func TestTokenDefaultScopeReadWrite(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})

	// Mint with no explicit scopes.
	resp := n.apiDo(t, "POST", "/api/v1/tokens", mustJSON(t, map[string]any{"label": "default-scope"}))
	defer resp.Body.Close()
	raw := drainBody(t, resp)
	assertStatus(t, resp, http.StatusCreated)

	var m mintResp
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !containsScope(m.Scopes, "read") || !containsScope(m.Scopes, "write") {
		t.Fatalf("default scopes must be [read write], got %v", m.Scopes)
	}
}

// FS-TokenUnknownScopeRejected
// Minting with an unknown scope returns 400.
func TestTokenUnknownScopeRejected(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})

	body := mustJSON(t, map[string]any{"label": "bad-scope", "scopes": []string{"super:root"}})
	resp := n.apiDo(t, "POST", "/api/v1/tokens", body)
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusBadRequest)
}

// FS-TokenPatchUpdatesLabel
// PATCH /api/v1/tokens/{id} with {"label":"new"} updates the label without
// changing scopes, and the token is still functional.
func TestTokenPatchUpdatesLabel(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})

	m := mintToken(t, n, "old-label", []string{"read", "write", "cluster:admin"})

	body := mustJSON(t, map[string]any{"label": "new-label"})
	patchResp := n.apiDo(t, "PATCH", "/api/v1/tokens/"+m.ID, body)
	defer patchResp.Body.Close()
	patchRaw := drainBody(t, patchResp)
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("patch token: expected 200, got %d: %s", patchResp.StatusCode, patchRaw)
	}
	var updated tokenEntry
	if err := json.Unmarshal([]byte(patchRaw), &updated); err != nil {
		t.Fatalf("decode patch response: %v", err)
	}
	if updated.Label != "new-label" {
		t.Fatalf("expected label=new-label, got %q", updated.Label)
	}
	if !containsScope(updated.Scopes, "write") {
		t.Fatalf("scopes must not change on PATCH: got %v", updated.Scopes)
	}

	// Token must still work after the label patch.
	authResp := n.apiDoBearer(t, "GET", "/api/v1/blocklists", "", m.Token)
	defer authResp.Body.Close()
	assertStatus(t, authResp, http.StatusOK)
}

// FS-TokenExpiredRejected
// A token with an already-past ExpiresAt is rejected with 401.
func TestTokenExpiredRejected(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})

	past := time.Now().UTC().Add(-time.Hour)
	body := mustJSON(t, map[string]any{
		"label":      "expired",
		"scopes":     []string{"read", "write"},
		"expires_at": past,
	})
	resp := n.apiDo(t, "POST", "/api/v1/tokens", body)
	defer resp.Body.Close()
	raw2 := drainBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("mint expired token: expected 201, got %d: %s", resp.StatusCode, raw2)
	}
	var m mintResp
	if err := json.Unmarshal([]byte(raw2), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}

	authResp := n.apiDoBearer(t, "GET", "/api/v1/blocklists", "", m.Token)
	defer authResp.Body.Close()
	assertStatus(t, authResp, http.StatusUnauthorized)
}

// FS-TokenAuditEntryRecordsTokenId
// Creating a token via Basic Auth and then a write via Bearer token both
// appear in the audit log with the correct actor prefix.
func TestTokenAuditEntryRecordsTokenId(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})

	m := mintToken(t, n, "audit-test", []string{"read", "write", "cluster:admin"})

	// Perform a write with the Bearer token so it appears in the audit log.
	body := mustJSON(t, map[string]any{
		"name": "audit-bl", "url": "https://example.com/audit.txt", "enabled": false,
	})
	writeResp := n.apiDoBearer(t, "POST", "/api/v1/blocklists", body, m.Token)
	defer writeResp.Body.Close()
	// 201 or 409 is fine here.

	// Fetch the audit log.
	auditResp := n.apiDo(t, "GET", "/api/v1/audit", "")
	assertStatus(t, auditResp, http.StatusOK)
	auditBody := readBody(t, auditResp) // readBody closes the body

	// The audit log must reference the token ID in the actor field.
	if !strContains(auditBody, m.ID) {
		t.Fatalf("audit log must contain token ID %s as actor, got: %s", m.ID, auditBody)
	}
	// The audit log must use the "token:" prefix.
	if !strContains(auditBody, "token:"+m.ID) {
		t.Fatalf("audit log actor must be token:%s, got: %s", m.ID, auditBody)
	}
}

// ── Small helpers ─────────────────────────────────────────────────────────────

// strContains reports whether string s contains substring sub.
func strContains(s, sub string) bool {
	if len(sub) == 0 || len(s) < len(sub) {
		return false
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func containsScope(scopes []string, s string) bool {
	for _, sc := range scopes {
		if sc == s {
			return true
		}
	}
	return false
}

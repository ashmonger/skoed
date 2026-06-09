package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/skoed/skoed/internal/cluster"
)

// TokensAPI handles POST/GET/DELETE/PATCH on /api/v1/tokens.
// The App interface is the same *api.App passed to all handlers;
// we use a narrow interface here to keep the handler package clean.
// Audit entries are emitted by the auditMiddleware layer; handlers must not
// call any audit method directly.
type TokensAPI struct {
	app tokenApp
}

type tokenApp interface {
	GetCluster() *cluster.Cluster
	LookupAPITokenByID(id string) (*cluster.APIToken, bool)
}

// NewTokensAPI constructs the handler.
func NewTokensAPI(app tokenApp) *TokensAPI { return &TokensAPI{app: app} }

// tokenMintResponse is the 201 body — only minting includes the raw token.
type tokenMintResponse struct {
	ID        string     `json:"id"`
	Token     string     `json:"token"` // raw value; shown once only
	Label     string     `json:"label"`
	Scopes    []string   `json:"scopes"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// tokenListEntry is the list/detail shape — no raw token ever.
type tokenListEntry struct {
	ID         string     `json:"id"`
	Label      string     `json:"label"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

func toListEntry(t cluster.APIToken) tokenListEntry {
	return tokenListEntry{
		ID:         t.ID,
		Label:      t.Label,
		Scopes:     t.Scopes,
		CreatedAt:  t.CreatedAt,
		LastUsedAt: t.LastUsedAt,
		ExpiresAt:  t.ExpiresAt,
	}
}

// Create — POST /api/v1/tokens
func (h *TokensAPI) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Label     string     `json:"label"`
		Scopes    []string   `json:"scopes"`
		ExpiresAt *time.Time `json:"expires_at,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.Label == "" || len(req.Label) > 64 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "label must be 1–64 characters"})
		return
	}
	// Default scopes: read + write.
	scopes := req.Scopes
	if len(scopes) == 0 {
		scopes = []string{"read", "write"}
	}
	if err := validateScopes(scopes); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	rawToken, id, hash, err := generateToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token generation failed"})
		return
	}

	now := time.Now().UTC()
	tok := cluster.APIToken{
		ID:        id,
		Hash:      hash,
		Label:     req.Label,
		Scopes:    scopes,
		CreatedAt: now,
		ExpiresAt: req.ExpiresAt,
	}
	if err := h.app.GetCluster().UpsertAPIToken(tok); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to store token"})
		return
	}
	writeJSON(w, http.StatusCreated, tokenMintResponse{
		ID:        id,
		Token:     rawToken,
		Label:     req.Label,
		Scopes:    scopes,
		CreatedAt: now,
		ExpiresAt: req.ExpiresAt,
	})
}

// List — GET /api/v1/tokens
func (h *TokensAPI) List(w http.ResponseWriter, r *http.Request) {
	tokens, err := h.app.GetCluster().APITokens()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list tokens"})
		return
	}
	out := make([]tokenListEntry, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, toListEntry(t))
	}
	writeJSON(w, http.StatusOK, out)
}

// Delete — DELETE /api/v1/tokens/{id}
func (h *TokensAPI) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := h.app.LookupAPITokenByID(id); !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "token not found"})
		return
	}
	if err := h.app.GetCluster().DeleteAPIToken(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete token"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Patch — PATCH /api/v1/tokens/{id}
func (h *TokensAPI) Patch(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, ok := h.app.LookupAPITokenByID(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "token not found"})
		return
	}

	var req map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if _, hasScopes := req["scopes"]; hasScopes {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "scopes cannot be changed after minting",
		})
		return
	}

	updated := *existing
	if v, ok := req["label"]; ok {
		var label string
		if err := json.Unmarshal(v, &label); err != nil || label == "" || len(label) > 64 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "label must be 1–64 characters"})
			return
		}
		updated.Label = label
	}
	if v, ok := req["expires_at"]; ok {
		if string(v) == "null" {
			updated.ExpiresAt = nil
		} else {
			var exp time.Time
			if err := json.Unmarshal(v, &exp); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid expires_at"})
				return
			}
			updated.ExpiresAt = &exp
		}
	}

	if err := h.app.GetCluster().UpsertAPIToken(updated); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update token"})
		return
	}
	writeJSON(w, http.StatusOK, toListEntry(updated))
}

// ── Helpers ───────────────────────────────────────────────────────────────

var validScopes = map[string]bool{
	"read": true, "write": true, "cluster:admin": true,
}

func validateScopes(scopes []string) error {
	for _, s := range scopes {
		if !validScopes[s] {
			return fmt.Errorf("unknown scope %q; valid: read, write, cluster:admin", s)
		}
	}
	return nil
}

// generateToken returns (rawToken, id, sha256hash, error).
// rawToken = "skoed_<48 hex>" (24 random bytes)
// id       = "tok_<12 hex>" (6 random bytes)
func generateToken() (rawToken, id, hash string, err error) {
	secret := make([]byte, 24)
	if _, err = rand.Read(secret); err != nil {
		return
	}
	idBytes := make([]byte, 6)
	if _, err = rand.Read(idBytes); err != nil {
		return
	}
	rawToken = "skoed_" + hex.EncodeToString(secret)
	id = "tok_" + hex.EncodeToString(idBytes)
	sum := sha256.Sum256([]byte(rawToken))
	hash = hex.EncodeToString(sum[:])
	return
}

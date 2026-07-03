package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/skoed/skoed/internal/config"
)

// WebhookApp is the narrow interface the webhook handlers need from the App.
type WebhookApp interface {
	GetWebhooks() []config.WebhookEndpoint
	UpdateWebhooks(endpoints []config.WebhookEndpoint) error
	FireWebhookTest(endpointID string) error
}

// WebhookHandlers bundles HTTP handlers that manage webhook endpoints.
type WebhookHandlers struct{ app WebhookApp }

// NewWebhookHandlers creates a WebhookHandlers for the given app.
func NewWebhookHandlers(app WebhookApp) *WebhookHandlers { return &WebhookHandlers{app: app} }

// ListWebhooks handles GET /api/v1/webhooks. The HMAC signing secret is
// redacted from the response — it is a credential that would let a reader
// forge signed payloads to the receiver, so it is never echoed back.
func (h *WebhookHandlers) ListWebhooks(w http.ResponseWriter, r *http.Request) {
	eps := h.app.GetWebhooks()
	out := make([]config.WebhookEndpoint, len(eps))
	copy(out, eps)
	for i := range out {
		out[i].Secret = ""
	}
	writeJSON(w, http.StatusOK, out)
}

// UpsertWebhook handles POST /api/v1/webhooks. If the body contains an existing
// id the endpoint is updated in-place; otherwise a new endpoint is appended.
func (h *WebhookHandlers) UpsertWebhook(w http.ResponseWriter, r *http.Request) {
	var ep config.WebhookEndpoint
	if !decodeJSON(w, r, &ep) {
		return
	}
	if ep.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	if ep.ID == "" {
		ep.ID = newWebhookID()
	}
	// Default to enabled when creating a new endpoint.
	if !ep.Enabled {
		ep.Enabled = true
	}

	eps := h.app.GetWebhooks()
	found := false
	for i, e := range eps {
		if e.ID == ep.ID {
			// Preserve the existing secret when the caller omits it (the UI
			// never receives the secret back via ListWebhooks, so a plain edit
			// would otherwise wipe it).
			if ep.Secret == "" {
				ep.Secret = e.Secret
			}
			eps[i] = ep
			found = true
			break
		}
	}
	if !found {
		eps = append(eps, ep)
	}

	if err := h.app.UpdateWebhooks(eps); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Redact the secret in the response too.
	resp := ep
	resp.Secret = ""
	writeJSON(w, http.StatusCreated, resp)
}

// DeleteWebhook handles DELETE /api/v1/webhooks/{id}.
func (h *WebhookHandlers) DeleteWebhook(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	eps := h.app.GetWebhooks()
	updated := eps[:0]
	found := false
	for _, e := range eps {
		if e.ID == id {
			found = true
		} else {
			updated = append(updated, e)
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "webhook not found")
		return
	}
	if err := h.app.UpdateWebhooks(updated); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// TestWebhook handles POST /api/v1/webhooks/{id}/test. Fires a test event
// directly to the named endpoint (bypasses the event filter).
func (h *WebhookHandlers) TestWebhook(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.app.FireWebhookTest(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"fired": true})
}

// newWebhookID returns a random 16-hex-character ID for a new webhook endpoint.
func newWebhookID() string {
	b := make([]byte, 8)
	rand.Read(b) //nolint:errcheck — crypto/rand.Read never returns an error
	return hex.EncodeToString(b)
}

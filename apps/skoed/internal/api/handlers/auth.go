package handlers

import (
	"net/http"
	"strings"
)

// Login handles POST /api/v1/auth/login. It validates username+password and
// issues a node-local session Bearer token (8 h TTL). No auth middleware is
// required on this endpoint — it IS the auth entry point.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !h.app.GetAuth().Verify(req.Username, req.Password) {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	rawToken, _, _, err := generateToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token generation failed")
		return
	}
	h.app.CreateSession(rawToken, req.Username)
	writeJSON(w, http.StatusOK, map[string]string{"token": rawToken})
}

// Logout handles DELETE /api/v1/auth/session. Revokes the session token
// carried in the Authorization: Bearer header, if present.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	hdr := r.Header.Get("Authorization")
	if strings.HasPrefix(hdr, "Bearer ") {
		h.app.DeleteSession(strings.TrimPrefix(hdr, "Bearer "))
	}
	w.WriteHeader(http.StatusNoContent)
}

// authSetupRequest is the body accepted by POST /api/v1/auth/setup.
type authSetupRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// AuthSetup handles POST /api/v1/auth/setup.
// This endpoint is always public (no auth required) so first-run credentials
// can be established. It returns 409 if credentials are already configured.
func (h *Handler) AuthSetup(w http.ResponseWriter, r *http.Request) {
	if h.app.GetAuth().IsConfigured() {
		writeError(w, http.StatusConflict, "credentials already configured")
		return
	}

	var req authSetupRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Username == "" {
		writeError(w, http.StatusBadRequest, "username is required")
		return
	}
	if req.Password == "" {
		writeError(w, http.StatusBadRequest, "password is required")
		return
	}

	if err := h.app.GetAuth().SetPassword(req.Username, req.Password); err != nil {
		writeError(w, http.StatusInternalServerError, "set password: "+err.Error())
		return
	}
	if err := h.app.UpdateAuthConfig(); err != nil {
		writeError(w, http.StatusInternalServerError, "save config: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusCreated)
}

// authChangePasswordRequest is the body accepted by PUT /api/v1/auth/password.
type authChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// AuthChangePassword handles PUT /api/v1/auth/password.
// Requires valid Basic Auth credentials (enforced by middleware).
func (h *Handler) AuthChangePassword(w http.ResponseWriter, r *http.Request) {
	var req authChangePasswordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.CurrentPassword == "" {
		writeError(w, http.StatusBadRequest, "current_password is required")
		return
	}
	if req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "new_password is required")
		return
	}

	if err := h.app.GetAuth().ChangePassword(req.CurrentPassword, req.NewPassword); err != nil {
		writeError(w, http.StatusUnauthorized, "password change failed: "+err.Error())
		return
	}
	if err := h.app.UpdateAuthConfig(); err != nil {
		writeError(w, http.StatusInternalServerError, "save config: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

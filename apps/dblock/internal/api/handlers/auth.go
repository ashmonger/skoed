package handlers

import (
	"net/http"
)

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

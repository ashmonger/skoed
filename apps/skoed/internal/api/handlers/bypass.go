package handlers

import (
	"net/http"
	"time"

	"github.com/skoed/skoed/internal/config"
)

// BypassApp is the subset of api.App required by the bypass handler.
type BypassApp interface {
	GetCfg() *config.Config
	SetProfilePause(id string, resumesAt time.Time, reason string, clientIPs []string) error
	GetProfilePause(id string) *config.PauseState
}

// BypassHandlers groups the M33 bypass HTTP handlers.
type BypassHandlers struct {
	app BypassApp
}

// NewBypassHandlers constructs the handler set.
func NewBypassHandlers(app BypassApp) *BypassHandlers {
	return &BypassHandlers{app: app}
}

// bypassRequest is the body accepted by POST /api/v1/bypass.
type bypassRequest struct {
	Passcode        string `json:"passcode"`
	DurationMinutes int    `json:"duration_minutes"`
	ClientIP        string `json:"client_ip"`
}

// bypassResponse is the body returned on a successful bypass.
type bypassResponse struct {
	ProfileID string    `json:"profile_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// CreateBypass handles POST /api/v1/bypass.
// It looks up the profile for the given client_ip, verifies the bypass_passcode,
// and sets a time-bounded profile pause so all filtering is suspended for that
// profile for the requested duration.
func (h *BypassHandlers) CreateBypass(w http.ResponseWriter, r *http.Request) {
	var req bypassRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.ClientIP == "" {
		writeError(w, http.StatusBadRequest, "client_ip is required")
		return
	}
	if req.DurationMinutes <= 0 {
		writeError(w, http.StatusBadRequest, "duration_minutes must be > 0")
		return
	}

	cfg := h.app.GetCfg()
	profile := findProfileByClientIP(cfg, req.ClientIP)
	if profile == nil {
		writeError(w, http.StatusNotFound, "no profile found for client_ip")
		return
	}

	if profile.BlockPage == nil || profile.BlockPage.BypassPasscode == "" {
		writeError(w, http.StatusNotFound, "this profile does not have a bypass passcode configured")
		return
	}

	if req.Passcode != profile.BlockPage.BypassPasscode {
		writeError(w, http.StatusForbidden, "incorrect bypass passcode")
		return
	}

	resumesAt := time.Now().Add(time.Duration(req.DurationMinutes) * time.Minute)
	if err := h.app.SetProfilePause(profile.ID, resumesAt, "bypass", nil); err != nil {
		writeError(w, http.StatusInternalServerError, "set profile pause: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, bypassResponse{
		ProfileID: profile.ID,
		ExpiresAt: resumesAt,
	})
}

// findProfileByClientIP returns the first profile that contains clientIP in its
// ClientIPs list, or nil if no match is found.
func findProfileByClientIP(cfg *config.Config, clientIP string) *config.Profile {
	if cfg == nil {
		return nil
	}
	for i := range cfg.Profiles {
		p := &cfg.Profiles[i]
		for _, ip := range p.ClientIPs {
			if ip == clientIP {
				return p
			}
		}
	}
	return nil
}

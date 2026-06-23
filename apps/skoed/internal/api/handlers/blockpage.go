package handlers

import (
	"encoding/json"
	"net"
	"net/http"

	"github.com/skoed/skoed/internal/config"
)

// blockPageResponse is the JSON shape for GET/PATCH /api/v1/blockpage.
type blockPageResponse struct {
	IP           string `json:"ip,omitempty"`
	Port         int    `json:"port,omitempty"`
	Title        string `json:"title,omitempty"`
	Message      string `json:"message,omitempty"`
	ContactEmail string `json:"contact_email,omitempty"`
}

func blockPageFromCfg(bp config.BlockPageConfig) blockPageResponse {
	return blockPageResponse{
		IP:           bp.IP,
		Port:         bp.Port,
		Title:        bp.Title,
		Message:      bp.Message,
		ContactEmail: bp.ContactEmail,
	}
}

// GetBlockPage handles GET /api/v1/blockpage.
func (h *Handler) GetBlockPage(w http.ResponseWriter, r *http.Request) {
	cfg := h.app.GetCfg()
	writeJSON(w, http.StatusOK, blockPageFromCfg(cfg.Filtering.BlockPage))
}

// blockPagePatch is the body accepted by PATCH /api/v1/blockpage.
type blockPagePatch struct {
	IP           *string `json:"ip"`
	Port         *int    `json:"port"`
	Title        *string `json:"title"`
	Message      *string `json:"message"`
	ContactEmail *string `json:"contact_email"`
}

// UpdateBlockPage handles PATCH /api/v1/blockpage.
func (h *Handler) UpdateBlockPage(w http.ResponseWriter, r *http.Request) {
	var patch blockPagePatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if err := h.app.WithWriteLock(func(cfg *config.Config) error {
		bp := &cfg.Filtering.BlockPage
		if patch.IP != nil {
			if *patch.IP != "" {
				if net.ParseIP(*patch.IP) == nil {
					return &validationError{"block_page.ip must be a valid IP address"}
				}
				if net.ParseIP(*patch.IP).To4() == nil {
					return &validationError{"block_page.ip must be an IPv4 address"}
				}
			}
			bp.IP = *patch.IP
		}
		if patch.Port != nil && *patch.Port != 0 {
			if *patch.Port < 1 || *patch.Port > 65535 {
				return &validationError{"block_page.port must be 1–65535"}
			}
			bp.Port = *patch.Port
		}
		if patch.Title != nil {
			bp.Title = *patch.Title
		}
		if patch.Message != nil {
			bp.Message = *patch.Message
		}
		if patch.ContactEmail != nil {
			bp.ContactEmail = *patch.ContactEmail
		}
		return nil
	}); err != nil {
		if ve, ok := err.(*validationError); ok {
			writeError(w, http.StatusBadRequest, ve.msg)
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	if err := h.app.SaveConfig(); err != nil {
		writeError(w, http.StatusInternalServerError, "save config: "+err.Error())
		return
	}

	// Notify the app that block page config changed so it can restart the server.
	if bp, ok := h.app.(BlockPageUpdater); ok {
		bp.RestartBlockPageServer()
	}

	cfg := h.app.GetCfg()
	writeJSON(w, http.StatusOK, blockPageFromCfg(cfg.Filtering.BlockPage))
}

// BlockPageUpdater is implemented by api.App to react to block page config changes.
type BlockPageUpdater interface {
	RestartBlockPageServer()
}

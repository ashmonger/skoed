package handlers

import (
	"net/http"

	"github.com/skoed/skoed/internal/filter"
)

// customRulesResponse is the JSON body for GET and PUT /api/v1/custom-rules.
type customRulesResponse struct {
	Rules string `json:"rules"`
}

// GetCustomRules handles GET /api/v1/custom-rules.
func (h *Handler) GetCustomRules(w http.ResponseWriter, r *http.Request) {
	cfg := h.app.GetCfg()
	rules := ""
	if cfg != nil {
		rules = cfg.Filtering.CustomRules
	}
	writeJSON(w, http.StatusOK, customRulesResponse{Rules: rules})
}

// PutCustomRules handles PUT /api/v1/custom-rules.
// It validates the submitted text as a complete rule set before committing.
func (h *Handler) PutCustomRules(w http.ResponseWriter, r *http.Request) {
	var req customRulesResponse
	if !decodeJSON(w, r, &req) {
		return
	}
	// Validate before applying via Raft so the cluster never stores broken rules.
	if _, err := filter.ParseCustomRules(req.Rules); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid rule: "+err.Error())
		return
	}
	if err := h.app.SetCustomRules(req.Rules); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, customRulesResponse{Rules: req.Rules})
}

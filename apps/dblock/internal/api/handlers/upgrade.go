package handlers

import (
	"net/http"

	"github.com/dblock/dblock/internal/upgrade"
)

// UpgradeChecker is the subset of *upgrade.Checker that handlers need.
// Held by the App; nil when no FeedURL was configured.
type UpgradeChecker interface {
	Latest() upgrade.CheckResult
}

// UpgradeCheck handles GET /api/v1/upgrade/check.
func (h *Handler) UpgradeCheck(w http.ResponseWriter, r *http.Request) {
	chk := h.app.GetUpgradeChecker()
	if chk == nil {
		writeJSON(w, http.StatusOK, upgrade.CheckResult{
			CurrentVersion: "",
		})
		return
	}
	writeJSON(w, http.StatusOK, chk.Latest())
}

// UpgradeStart handles POST /api/v1/upgrade/start.
//
// M5.6 v1: validates the request reached the leader (LeaderForward
// wrapper handles that) and returns 202. The actual binary swap is
// gated behind node.upgrade.enable_swap (default false) — that branch
// lands in M5.6.1 once M5.5 packaging + M5.7 multi-arch builds give
// us the asset matrix to verify against.
func (h *Handler) UpgradeStart(w http.ResponseWriter, r *http.Request) {
	chk := h.app.GetUpgradeChecker()
	if chk == nil {
		writeError(w, http.StatusServiceUnavailable, "upgrade feed is not configured")
		return
	}
	res := chk.Latest()
	if !res.UpgradeAvailable {
		writeError(w, http.StatusConflict, "no upgrade available (running latest)")
		return
	}
	// v1 stub: accept the request, audit middleware records who
	// triggered it. The swap pipeline lands in M5.6.1.
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted":          true,
		"target_version":    res.AvailableVersion,
		"swap_implemented":  false,
		"message":           "upgrade.start recorded; binary swap lands in M5.6.1 (node.upgrade.enable_swap)",
	})
}

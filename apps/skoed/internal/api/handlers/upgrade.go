package handlers

import (
	"net/http"
	"os"
	"time"

	"github.com/skoed/skoed/internal/upgrade"
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
		writeJSON(w, http.StatusOK, upgrade.CheckResult{})
		return
	}
	writeJSON(w, http.StatusOK, chk.Latest())
}

// UpgradeStart handles POST /api/v1/upgrade/start.
// Requires the request to have reached the leader (LeaderForward wrapper).
// Downloads the binary for the current arch, atomically replaces the
// running executable, responds 202, then schedules os.Exit(0) so the
// supervisor (systemd / OpenRC) restarts the process with the new binary.
// In SKOED_TEST_MODE=1 the exit is skipped so the test process survives.
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

	assetKey := upgrade.AssetKey()
	assetURL := res.Assets[assetKey]
	if assetURL == "" {
		writeError(w, http.StatusUnprocessableEntity, "no asset for "+assetKey+" in feed")
		return
	}

	exePath, err := os.Executable()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "resolve exe: "+err.Error())
		return
	}
	// SKOED_TEST_SWAP_DEST redirects the swap to a temp path so acceptance
	// tests don't overwrite the binary used by the rest of the test suite.
	if dest := os.Getenv("SKOED_TEST_SWAP_DEST"); dest != "" {
		exePath = dest
	}

	if err := upgrade.Swap(assetURL, exePath); err != nil {
		writeError(w, http.StatusInternalServerError, "swap failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted":       true,
		"target_version": res.AvailableVersion,
		"message":        "binary swap initiated; process will restart",
	})

	// Give the response time to flush, then exit so the supervisor
	// restarts with the new binary. Skipped in test mode.
	if os.Getenv("SKOED_TEST_MODE") != "1" {
		go func() {
			time.Sleep(200 * time.Millisecond)
			os.Exit(0)
		}()
	}
}

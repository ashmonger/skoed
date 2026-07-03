package handlers

import (
	"net/http"
	"os"
	"time"

	"github.com/skoed/skoed/internal/upgrade"
)

// NodeUpgradeStart handles POST /api/v1/upgrade/node-start.
// Cluster-internal only: authenticated by X-Cluster-Secret, NOT wrapped in
// WriteForwardMiddleware. Used by the rolling upgrade goroutine to trigger
// a local binary swap on each peer node without forwarding to the leader.
func (h *Handler) NodeUpgradeStart(w http.ResponseWriter, r *http.Request) {
	cl := h.app.GetCluster()
	if cl == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster not available")
		return
	}
	if !cl.ValidateClusterSecret(r.Header.Get("X-Cluster-Secret")) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var body struct {
		URL    string `json:"url"`
		SHA256 string `json:"sha256"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	if body.SHA256 == "" {
		writeError(w, http.StatusBadRequest, "sha256 is required")
		return
	}

	exePath, err := os.Executable()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "resolve exe: "+err.Error())
		return
	}
	if dest := os.Getenv("SKOED_TEST_SWAP_DEST"); dest != "" {
		exePath = dest
	}

	if err := upgrade.Swap(body.URL, exePath, body.SHA256); err != nil {
		writeError(w, http.StatusInternalServerError, "swap failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted": true,
		"message":  "binary swap initiated; process will restart",
	})

	if os.Getenv("SKOED_TEST_MODE") != "1" {
		go func() {
			time.Sleep(200 * time.Millisecond)
			os.Exit(1)
		}()
	}
}

// UpgradeChecker is the subset of *upgrade.Checker that handlers need.
// Held by the App; nil when no FeedURL was configured.
type UpgradeChecker interface {
	Latest() upgrade.CheckResult
	Refresh() upgrade.CheckResult
}

// UpgradeCheck handles GET /api/v1/upgrade/check.
// When ?force=true is set it triggers a live fetch instead of returning
// the cached snapshot — used by the manual "Check for updates" button.
func (h *Handler) UpgradeCheck(w http.ResponseWriter, r *http.Request) {
	chk := h.app.GetUpgradeChecker()
	if chk == nil {
		writeJSON(w, http.StatusOK, upgrade.CheckResult{})
		return
	}
	if r.URL.Query().Get("force") == "true" {
		writeJSON(w, http.StatusOK, chk.Refresh())
		return
	}
	writeJSON(w, http.StatusOK, chk.Latest())
}

// UpgradeStart handles POST /api/v1/upgrade/start.
// Requires the request to have reached the leader (LeaderForward wrapper).
// Downloads the binary for the current arch, atomically replaces the
// running executable, responds 202, then schedules os.Exit(1) so the
// supervisor (systemd / OpenRC) restarts the process with the new binary.
// In SKOED_TEST_MODE=1 the exit is skipped so the test process survives.
//
// When the request body contains {"url": "..."}, that URL is used directly
// (rolling-upgrade path, M18 TS-RollingUpgrade). Otherwise the configured
// upgrade feed is consulted (M5.6 path).
func (h *Handler) UpgradeStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL    string `json:"url"`
		SHA256 string `json:"sha256"`
	}
	_ = decodeJSONOptional(r, &body)

	var assetURL, targetVersion, sha string
	if body.URL != "" {
		// Direct URL supplied (M18 rolling-upgrade path). Skip feed check.
		// A checksum is mandatory: swapping the binary with unverified bytes
		// from an arbitrary URL would be remote code execution.
		if body.SHA256 == "" {
			writeError(w, http.StatusBadRequest, "sha256 is required when supplying a direct url")
			return
		}
		assetURL = body.URL
		sha = body.SHA256
	} else {
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
		assetURL = res.Assets[assetKey]
		if assetURL == "" {
			writeError(w, http.StatusUnprocessableEntity, "no asset for "+assetKey+" in feed")
			return
		}
		sha = res.Checksums[assetKey]
		if sha == "" {
			writeError(w, http.StatusUnprocessableEntity, "release feed provides no checksum for "+assetKey+"; refusing unverified upgrade")
			return
		}
		targetVersion = res.AvailableVersion
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

	if err := upgrade.Swap(assetURL, exePath, sha); err != nil {
		writeError(w, http.StatusInternalServerError, "swap failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted":       true,
		"target_version": targetVersion,
		"message":        "binary swap initiated; process will restart",
	})

	// Give the response time to flush, then exit so the supervisor (systemd
	// Restart=on-failure) restarts with the new binary. Exit 1 is required:
	// Restart=on-failure only triggers on non-zero exits. Skipped in test mode.
	if os.Getenv("SKOED_TEST_MODE") != "1" {
		go func() {
			time.Sleep(200 * time.Millisecond)
			os.Exit(1)
		}()
	}
}

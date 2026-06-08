// doh_resolvers.go — M6 TS-DohResolverDb HTTP surface.
//
// Three endpoints, two read-only and public, one admin-only mutator:
//
//   GET  /api/v1/doh-resolvers                — list current snapshot
//   GET  /api/v1/doh-resolvers/snapshot.json  — raw export for jq pipelines
//   POST /api/v1/doh-resolvers/refresh        — nudge the leader scheduler
//
// All three read directly from the *cluster.Cluster snapshot cache
// (refreshed by api.App's onApply hook). The refresh endpoint forwards
// to the leader via the standard middleware.forward path and pokes the
// scheduler's Nudge() so the next loop iteration runs the cycle without
// waiting for the 24h timer.

package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/skoed/skoed/internal/dohresolvers"
)

// DohResolverScheduler is the subset of dohresolvers.Scheduler the
// handler needs. Kept as an interface so the api package can wire the
// concrete scheduler without dragging dohresolvers into AppState.
type DohResolverScheduler interface {
	Nudge()
	CurrentSnapshotID() string
}

// AppDohResolvers is the slice of api.App the doh-resolvers handlers
// touch. Implemented by *api.App; declared here to avoid circular
// imports.
type AppDohResolvers interface {
	GetDohResolverSnapshot() (*dohresolvers.Snapshot, error)
	GetDohResolverScheduler() DohResolverScheduler
	DohResolverStaleAfter() time.Duration
}

// dohResolverEntryDTO is the wire-form of one resolver row.
type dohResolverEntryDTO struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	IPv4      []string `json:"ipv4"`
	IPv6      []string `json:"ipv6"`
	SourceURL string   `json:"source_url"`
}

// dohSnapshotDTO is the wire-form of the snapshot summary returned by
// GET /api/v1/doh-resolvers. Same body shape is reused for the
// /snapshot.json export.
type dohSnapshotDTO struct {
	SnapshotID       string                `json:"snapshot_id"`
	SourceURL        string                `json:"source_url"`
	FetchedAt        string                `json:"fetched_at"`
	Stale            bool                  `json:"stale"`
	LastRefreshError string                `json:"last_refresh_error"`
	Resolvers        []dohResolverEntryDTO `json:"resolvers"`
}

func snapshotToDTO(snap *dohresolvers.Snapshot, staleAfter time.Duration) dohSnapshotDTO {
	out := dohSnapshotDTO{
		SnapshotID:       snap.SnapshotID,
		SourceURL:        snap.SourceURL,
		FetchedAt:        snap.FetchedAt,
		LastRefreshError: snap.LastRefreshError,
		Resolvers:        make([]dohResolverEntryDTO, len(snap.Resolvers)),
	}
	for i, e := range snap.Resolvers {
		// Normalise nil slices to empty arrays for stable JSON.
		v4 := e.IPv4
		if v4 == nil {
			v4 = []string{}
		}
		v6 := e.IPv6
		if v6 == nil {
			v6 = []string{}
		}
		out.Resolvers[i] = dohResolverEntryDTO{
			ID:        e.ID,
			Name:      e.Name,
			IPv4:      v4,
			IPv6:      v6,
			SourceURL: e.SourceURL,
		}
	}
	if fetched, err := time.Parse(time.RFC3339, snap.FetchedAt); err == nil {
		if staleAfter > 0 && time.Since(fetched) > staleAfter {
			out.Stale = true
		}
	}
	return out
}

// ListDohResolvers handles GET /api/v1/doh-resolvers.
func ListDohResolvers(app AppDohResolvers) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snap, err := app.GetDohResolverSnapshot()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if snap == nil || snap.SnapshotID == "" {
			writeError(w, http.StatusNotFound, "no snapshot yet")
			return
		}
		writeJSON(w, http.StatusOK, snapshotToDTO(snap, app.DohResolverStaleAfter()))
	}
}

// SnapshotJSON handles GET /api/v1/doh-resolvers/snapshot.json. Same body
// as the list endpoint but with explicit content-disposition for `curl |
// jq` pipelines (FS-DohResolverDbSnapshotJsonExport).
func SnapshotJSON(app AppDohResolvers) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snap, err := app.GetDohResolverSnapshot()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if snap == nil || snap.SnapshotID == "" {
			writeError(w, http.StatusNotFound, "no snapshot yet")
			return
		}
		dto := snapshotToDTO(snap, app.DohResolverStaleAfter())
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `inline; filename="doh-resolvers.json"`)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(dto)
	}
}

// ForceRefresh handles POST /api/v1/doh-resolvers/refresh. The actual
// refresh is performed by the scheduler goroutine; this endpoint only
// nudges it (FS-DohResolverDbAdminForceRefresh). Returns 202 with the
// snapshot_id that was current at queue time so polling callers can
// detect the new snapshot.
func ForceRefresh(app AppDohResolvers) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sched := app.GetDohResolverScheduler()
		if sched == nil {
			writeError(w, http.StatusServiceUnavailable, "scheduler unavailable")
			return
		}
		curr := sched.CurrentSnapshotID()
		sched.Nudge()
		writeJSON(w, http.StatusAccepted, map[string]any{
			"queued":              true,
			"current_snapshot_id": curr,
		})
	}
}

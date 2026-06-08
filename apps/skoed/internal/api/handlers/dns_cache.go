package handlers

import (
	"net/http"
)

// GetDNSCacheStats returns the cache snapshot.
//
// FSID: FS-CacheStatsEndpoint.
func (h *Handler) GetDNSCacheStats(w http.ResponseWriter, r *http.Request) {
	cache := h.app.GetDNSCache()
	if cache == nil {
		// Cache disabled in config — still surface a zero snapshot so
		// the UI doesn't have to special-case the disabled state.
		writeJSON(w, http.StatusOK, map[string]any{
			"size": 0, "max_entries": 0, "hits": 0, "misses": 0, "evictions": 0,
		})
		return
	}
	writeJSON(w, http.StatusOK, cache.Snapshot())
}

// PurgeDNSCache handles POST /api/v1/dns/cache/purge.
// Without ?domain= it empties the whole cache.
// With ?domain=<fqdn> it drops every entry matching that name across
// all qtypes.
//
// FSIDs: FS-CachePurgeAll, FS-CachePurgeOneDomain.
func (h *Handler) PurgeDNSCache(w http.ResponseWriter, r *http.Request) {
	cache := h.app.GetDNSCache()
	if cache == nil {
		writeJSON(w, http.StatusOK, map[string]int{"purged": 0})
		return
	}
	domain := r.URL.Query().Get("domain")
	var purged int
	if domain == "" {
		purged = cache.PurgeAll()
	} else {
		purged = cache.PurgeDomain(domain)
	}
	writeJSON(w, http.StatusOK, map[string]int{"purged": purged})
}

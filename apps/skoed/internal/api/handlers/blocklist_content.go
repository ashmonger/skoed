package handlers

import (
	"compress/gzip"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
)

// sha256Hex matches the sha256 query parameter of the content endpoint.
var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ClusterInternalBlocklistContent handles
// GET /api/v1/cluster/_internal/blocklists/{id}/content?sha256=<hex>
// (M50, TS-BlocklistContentDistribution §5.1). Followers call it on the
// leader to copy the downloaded content of a URL list. Authenticated by the
// replicated cluster secret, like the other /_internal/ routes.
//
//   - 404: unknown list, or this node holds no copy;
//   - 409: this node holds another version than the one requested;
//   - 200: the copy, streamed from the file, gzip-encoded when accepted.
func (h *Handler) ClusterInternalBlocklistContent(w http.ResponseWriter, r *http.Request) {
	c := h.requireCluster(w)
	if c == nil {
		return
	}
	if !c.ValidateClusterSecret(r.Header.Get("X-Cluster-Secret")) {
		writeError(w, http.StatusUnauthorized, "invalid cluster secret")
		return
	}
	want := r.URL.Query().Get("sha256")
	if !sha256Hex.MatchString(want) {
		writeError(w, http.StatusBadRequest, "sha256 must be 64 lower-case hex digits")
		return
	}

	// The ID is matched against the replicated list IDs before any file
	// path is built from it.
	id := urlParam(r, "id")
	known := false
	for _, bl := range h.app.GetCfg().Filtering.Blocklists {
		if bl.ID == id {
			known = true
			break
		}
	}
	cache := h.app.BlocklistContent()
	if !known || cache == nil {
		writeError(w, http.StatusNotFound, "blocklist not found")
		return
	}
	f, e, err := cache.OpenFile(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "no copy of this blocklist on this node")
		return
	}
	defer f.Close()
	if e.SHA256 != want {
		writeError(w, http.StatusConflict, "this node holds another version of this blocklist")
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Skoed-Content-SHA256", e.SHA256)
	var out io.Writer = w
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		defer gz.Close()
		out = gz
	}
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(out, f); err != nil {
		log.Printf("blocklist content %q: send: %v", id, err)
	}
}

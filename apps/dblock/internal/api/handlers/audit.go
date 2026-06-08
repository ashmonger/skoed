package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/dblock/dblock/internal/cluster"
)

// auditEntryResp is the on-wire shape returned by /api/v1/audit. The
// time field is RFC3339 (the bbolt row stores unix seconds).
type auditEntryResp struct {
	ID        string `json:"id"`
	Seq       uint64 `json:"seq"`
	Timestamp string `json:"timestamp"`
	Actor     string `json:"actor"`
	Action    string `json:"action"`
	Target    string `json:"target,omitempty"`
	Result    string `json:"result"`
	Error     string `json:"error,omitempty"`
	Diff      string `json:"diff,omitempty"`
	NodeID    string `json:"node_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

type auditPageResp struct {
	Entries []auditEntryResp `json:"entries"`
	Total   int              `json:"total"`
	Limit   int              `json:"limit"`
	Offset  int              `json:"offset"`
}

// AuditList handles GET /api/v1/audit. Newest-first; filters by actor,
// action prefix, and result. Pagination with limit + offset.
func (h *Handler) AuditList(w http.ResponseWriter, r *http.Request) {
	c := h.app.GetCluster()
	if c == nil {
		writeError(w, http.StatusServiceUnavailable, "cluster unavailable")
		return
	}
	q := cluster.AuditQuery{
		Actor:        r.URL.Query().Get("actor"),
		ActionPrefix: r.URL.Query().Get("action"),
		Result:       r.URL.Query().Get("result"),
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			q.Limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			q.Offset = n
		}
	}
	rows, total, err := c.Store().AuditList(q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "audit list: "+err.Error())
		return
	}
	resp := auditPageResp{
		Entries: make([]auditEntryResp, 0, len(rows)),
		Total:   total,
		Limit:   q.Limit,
		Offset:  q.Offset,
	}
	if resp.Limit <= 0 {
		resp.Limit = 50
	}
	for _, row := range rows {
		resp.Entries = append(resp.Entries, auditEntryResp{
			ID:        row.ID,
			Seq:       row.Seq,
			Timestamp: time.Unix(row.TimeUnix, 0).UTC().Format(time.RFC3339),
			Actor:     row.Actor,
			Action:    row.Action,
			Target:    row.Target,
			Result:    row.Result,
			Error:     row.Error,
			Diff:      row.Diff,
			NodeID:    row.NodeID,
			RequestID: row.RequestID,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

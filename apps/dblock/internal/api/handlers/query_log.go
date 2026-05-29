package handlers

import (
	"net/http"
	"strconv"
)

// queryLogResponse is the JSON shape returned by GET /api/v1/query-log.
type queryLogResponse struct {
	Entries []any `json:"entries"`
	Total   int   `json:"total"`
}

// GetQueryLog handles GET /api/v1/query-log.
// Supports optional query params: client=, outcome=, limit=, offset=
func (h *Handler) GetQueryLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	client := q.Get("client")
	outcome := q.Get("outcome")

	limit := 100
	if l := q.Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}
	offset := 0
	if o := q.Get("offset"); o != "" {
		if v, err := strconv.Atoi(o); err == nil && v >= 0 {
			offset = v
		}
	}

	ql := h.app.GetQueryLog()
	entries, total := ql.Query(client, outcome, limit, offset)

	result := make([]any, len(entries))
	for i, e := range entries {
		result[i] = map[string]any{
			"id":           e.ID,
			"timestamp":    e.Timestamp,
			"client":       e.Client,
			"domain":       e.Domain,
			"query_type":   e.QueryType,
			"outcome":      string(e.Outcome),
			"blocklist_id": e.BlocklistID,
		}
	}

	writeJSON(w, http.StatusOK, queryLogResponse{
		Entries: result,
		Total:   total,
	})
}

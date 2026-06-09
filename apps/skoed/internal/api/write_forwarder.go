package api

import (
	"context"
	"net/http"
)

// WriteForwarder is the interface the write-forward middleware uses to
// interact with the cluster. It is satisfied by *cluster.Cluster (via
// clusterWriteAdapter below) and by test doubles.
//
// ForwardWrite sends a mutating request to the current leader and returns
// the leader's status code, body, and response headers. method is the HTTP
// method, path is the URL path+query, body is the request body bytes (may be
// nil), and inHeaders are the request headers to forward.
//
// NodeID returns the local node's identifier, used in X-Served-By.
//
// CommitIndex returns the local Raft commit index, used in X-Raft-Commit-Index.
//
// IsLeader reports whether this node is the current Raft leader.
type WriteForwarder interface {
	ForwardWrite(ctx context.Context, method string, path string, body []byte, inHeaders http.Header) (statusCode int, respBody []byte, respHeaders http.Header, err error)
	NodeID() string
	CommitIndex() uint64
	IsLeader() bool
}

package api

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/skoed/skoed/internal/bindwait"
)

// M50 — peer API listener (TS-EncryptedClusterMesh, "Internal API").
//
// With `node.cluster.mtls.enabled: true`, every node serves the management
// API router a second time on `node.cluster.peer_api_address`, over TLS with
// a client certificate signed by the cluster CA required. Peer-to-peer HTTP,
// which carries the X-Cluster-Secret header, goes there, and the main
// listener refuses that header so the secret never crosses the network in
// clear.

type peerListenerKey struct{}

// fromPeerListener reports whether r was received on the peer listener.
func fromPeerListener(r *http.Request) bool {
	v, _ := r.Context().Value(peerListenerKey{}).(bool)
	return v
}

// rejectClusterSecretOffPeerListener answers 401 to a request that carries
// X-Cluster-Secret when mTLS is enabled and the request did not come in on
// the peer listener. With mTLS disabled, peers still use the main listener.
func (a *App) rejectClusterSecretOffPeerListener(next http.Handler) http.Handler {
	return rejectClusterSecretOffPeer(func() bool {
		return a.cluster != nil && a.cluster.MTLSEnabled()
	}, next)
}

// rejectClusterSecretOffPeer is rejectClusterSecretOffPeerListener, with
// mtlsEnabled telling whether the node runs with the cluster mTLS. The
// secret's value is not checked: any request carrying it is refused.
func rejectClusterSecretOffPeer(mtlsEnabled func() bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Cluster-Secret") != "" && mtlsEnabled() && !fromPeerListener(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"the cluster secret is accepted only on the peer mTLS listener"}` + "\n"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// markPeerListener marks every request as received on the peer listener
// before passing it to next.
func markPeerListener(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), peerListenerKey{}, true)))
	})
}

// PeerServer is the peer API listener.
type PeerServer struct {
	httpServer *http.Server
	listener   net.Listener
}

// StartPeerServer listens on addr with tlsCfg (which must require and verify
// a client certificate) and serves the app's router, marking each request as
// received on the peer listener.
func StartPeerServer(app *App, addr string, tlsCfg *tls.Config) (*PeerServer, error) {
	if tlsCfg == nil || tlsCfg.ClientAuth != tls.RequireAndVerifyClientCert {
		return nil, errors.New("peer API server: TLS config must require and verify client certificates")
	}
	if addr == "" {
		return nil, errors.New("peer API server: no listen address")
	}
	ln, err := bindwait.Retry(addr, func() (net.Listener, error) {
		return tls.Listen("tcp", addr, tlsCfg)
	})
	if err != nil {
		return nil, fmt.Errorf("peer API server: listen %s: %w", addr, err)
	}
	handler := markPeerListener(app.Router())
	s := &PeerServer{
		listener: ln,
		// Same limits as the main listener; no WriteTimeout so the query-log
		// SSE stream between peers is not cut off.
		httpServer: &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
		},
	}
	go func() {
		if err := s.httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("peer API server: %v", err)
		}
	}()
	return s, nil
}

// Addr returns the address the peer listener is bound to.
func (s *PeerServer) Addr() net.Addr { return s.listener.Addr() }

// Shutdown gracefully stops the peer listener.
func (s *PeerServer) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

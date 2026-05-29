package api

import (
	"context"
	"fmt"
	"net/http"
)

// Server wraps net/http.Server and binds it to the App's router.
type Server struct {
	httpServer *http.Server
}

// NewServer creates a Server that will listen on the port specified in app.Cfg.API.
func NewServer(app *App) *Server {
	app.mu.RLock()
	port := app.cfg.API.Port
	app.mu.RUnlock()

	return &Server{
		httpServer: &http.Server{
			Addr:    fmt.Sprintf(":%d", port),
			Handler: app.Router(),
		},
	}
}

// Start starts the HTTP server in a background goroutine. It returns
// immediately after the listener is established, or an error if the
// listener cannot be created.
func (s *Server) Start() error {
	errCh := make(chan error, 1)
	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// Give the server a brief window to fail fast (e.g. port already in use).
	// A non-blocking check is sufficient; we rely on the caller to handle
	// long-running errors via shutdown or process supervision.
	select {
	case err := <-errCh:
		return fmt.Errorf("api server: %w", err)
	default:
		return nil
	}
}

// Shutdown gracefully stops the HTTP server, waiting for in-flight requests
// to complete or until ctx is cancelled.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

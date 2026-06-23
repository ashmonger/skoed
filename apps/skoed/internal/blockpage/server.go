// Package blockpage implements the M26 redirect block page HTTP server.
// When filtering.block_policy is "redirect", this server listens on a
// configurable port and serves a self-contained HTML page for every request.
package blockpage

import (
	"context"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const htmlTmpl = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}}</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:system-ui,-apple-system,sans-serif;background:#0f172a;color:#e2e8f0;min-height:100vh;display:flex;align-items:center;justify-content:center;padding:2rem}
.card{background:#1e293b;border:1px solid #334155;border-radius:1rem;padding:2.5rem 3rem;max-width:480px;width:100%;text-align:center;box-shadow:0 25px 50px rgba(0,0,0,.5)}
.icon{font-size:3rem;margin-bottom:1.25rem}
h1{font-size:1.5rem;font-weight:700;color:#f1f5f9;margin-bottom:.75rem}
p{color:#94a3b8;line-height:1.6;margin-bottom:.75rem}
.contact{margin-top:1.5rem;font-size:.875rem;color:#64748b}
.contact a{color:#38bdf8;text-decoration:none}
.contact a:hover{text-decoration:underline}
</style>
</head>
<body>
<div class="card">
  <div class="icon">🚫</div>
  <h1>{{.Title}}</h1>
  <p>{{.Message}}</p>
  {{if .ContactEmail}}<div class="contact">Need access? Contact <a href="mailto:{{.ContactEmail}}">{{.ContactEmail}}</a></div>{{end}}
</div>
</body>
</html>`

var pageTmpl = template.Must(template.New("block").Parse(htmlTmpl))

// Config holds the rendered content for the block page.
type Config struct {
	Title        string
	Message      string
	ContactEmail string
}

// DefaultConfig returns the default block page content.
func DefaultConfig() Config {
	return Config{
		Title:   "Access Blocked",
		Message: "This website has been blocked by your network administrator.",
	}
}

// Server is a minimal HTTP server that serves the block page.
type Server struct {
	mu     sync.Mutex
	cfg    Config
	srv    *http.Server
	ln     net.Listener
}

// New creates a Server with the given initial config. Call Start to bind.
func New(cfg Config) *Server {
	s := &Server{cfg: cfg}
	return s
}

// Start binds the server on addr ("host:port") and begins serving in a
// background goroutine. Returns an error if the listen fails.
func (s *Server) Start(addr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("block page server: listen %s: %w", addr, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handle)

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	s.ln = ln
	s.srv = srv

	go srv.Serve(ln) //nolint:errcheck
	return nil
}

// Stop gracefully shuts down the HTTP server. Safe to call when not running.
func (s *Server) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = s.srv.Shutdown(ctx)
	s.srv = nil
	s.ln = nil
}

// UpdateConfig replaces the page content. Thread-safe; takes effect on the
// next request (no restart needed for content-only changes).
func (s *Server) UpdateConfig(cfg Config) {
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
}

// IsRunning reports whether the server is currently listening.
func (s *Server) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.srv != nil
}

func (s *Server) handle(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()

	if cfg.Title == "" {
		cfg.Title = "Access Blocked"
	}
	if cfg.Message == "" {
		cfg.Message = "This website has been blocked by your network administrator."
	}

	var buf strings.Builder
	if err := pageTmpl.Execute(&buf, cfg); err != nil {
		http.Error(w, "block page unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(buf.String()))
}

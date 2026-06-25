// Package blockpage implements the M26 redirect block page HTTP server.
// When filtering.block_policy is "redirect", this server listens on a
// configurable port and serves a self-contained HTML page for every request.
package blockpage

import (
	"context"
	"fmt"
	"html/template"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

var jokes = []string{
	"Why did the DNS query cross the road? It got blocked before it could find out.",
	"I tried to visit a blocked site. Now I have a SERVFAIL relationship with the internet.",
	"What do you call a domain that keeps getting blocked? A repeat NXDOMAINder.",
	"My DNS resolver walks into a bar. The bartender says: sorry, you're on the list.",
	"Why don't DNS admins tell secrets? Because every query leaves a log.",
	"A UDP packet walks into a bar. The bartender ignores it.",
	"Why did the sysadmin break up with the firewall? Too many blocked connections.",
	"What's a DNS server's favourite song? 'Hello, is it me you're looking for?' — No. NXDOMAIN.",
	"How many sysadmins does it take to block a domain? Just one, but they'll cache the result for 86400 seconds.",
	"Why is the internet like a blocked drain? Because of all the garbage going through port 80.",
	"I asked my DNS server for directions. It said: that destination does not exist.",
	"What did the router say to the doctor? It hurts when I ping.",
}

const htmlTmpl = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}}</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:system-ui,-apple-system,sans-serif;background:#0f1117;color:#e2e8f0;min-height:100vh;display:flex;flex-direction:column;align-items:center;justify-content:center;padding:2rem;gap:1rem}
.card{background:#1a1d27;border:1px solid #2a2d3a;border-radius:1rem;padding:2.5rem 3rem;max-width:500px;width:100%;text-align:center;box-shadow:0 25px 60px rgba(0,0,0,.6)}
.logo{display:flex;align-items:center;justify-content:center;gap:.6rem;margin-bottom:1.5rem}
.logo-mark{width:40px;height:40px;background:linear-gradient(135deg,#4f9cf9,#818cf8);border-radius:.6rem;display:flex;align-items:center;justify-content:center;font-size:1.2rem;font-weight:800;color:#fff;font-family:monospace;letter-spacing:-.05em;flex-shrink:0}
.logo-name{font-size:1.1rem;font-weight:700;color:#e2e8f0;letter-spacing:.04em}
.divider{width:3rem;height:2px;background:#2a2d3a;margin:.25rem auto 1.5rem}
.stop{font-size:2.5rem;margin-bottom:1rem}
h1{font-size:1.4rem;font-weight:700;color:#f1f5f9;margin-bottom:.6rem}
.msg{color:#94a3b8;line-height:1.65;margin-bottom:.5rem;font-size:.95rem}
.joke{margin-top:1.25rem;padding:.75rem 1rem;background:#0d1117;border:1px solid #2a2d3a;border-radius:.5rem;font-size:.82rem;color:#64748b;font-style:italic;line-height:1.5}
.joke span{color:#4f9cf9}
.contact{margin-top:1.25rem;font-size:.85rem;color:#64748b}
.contact a{color:#4f9cf9;text-decoration:none}
.contact a:hover{text-decoration:underline}
footer{font-size:.75rem;color:#334155;margin-top:.5rem}
footer a{color:#334155;text-decoration:none}
footer a:hover{color:#64748b}
</style>
</head>
<body>
<div class="card">
  <div class="logo">
    <div class="logo-mark">sk</div>
    <div class="logo-name">skoed</div>
  </div>
  <div class="divider"></div>
  <div class="stop">🚫</div>
  <h1>{{.Title}}</h1>
  <p class="msg">{{.Message}}</p>
  <div class="joke"><span>DNS joke of the day:</span> {{.Joke}}</div>
  {{if .ContactEmail}}<div class="contact">Need access? Contact <a href="mailto:{{.ContactEmail}}">{{.ContactEmail}}</a></div>{{end}}
</div>
<footer>Protected by <a href="#">skoed</a> · self-hosted DNS filtering</footer>
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

// ProfileConfig holds per-profile block page overrides (M33). Fields that
// are non-empty override the corresponding global Config fields.
type ProfileConfig struct {
	Title        string
	Message      string
	ContactEmail string
	ProfileID    string
}

// Server is a minimal HTTP server that serves the block page.
type Server struct {
	mu             sync.Mutex
	cfg            Config
	customTemplate string // M33: raw HTML template string; "" means use built-in
	srv            *http.Server
	ln             net.Listener

	// M33: optional per-profile config lookup. When non-nil, the server
	// reads the client_ip query parameter and calls this function to get
	// profile-specific overrides. nil disables the feature.
	profileConfigFn func(clientIP string) *ProfileConfig
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

// SetCustomTemplate stores a raw HTML template string that replaces the
// built-in template on every subsequent request. Pass "" to revert to the
// built-in. Thread-safe.
func (s *Server) SetCustomTemplate(html string) {
	s.mu.Lock()
	s.customTemplate = html
	s.mu.Unlock()
}

// ClearCustomTemplate reverts the server to the built-in default template.
func (s *Server) ClearCustomTemplate() {
	s.mu.Lock()
	s.customTemplate = ""
	s.mu.Unlock()
}

// SetProfileConfigFn wires the M33 per-profile config lookup. Replaces any
// previously wired function. Thread-safe.
func (s *Server) SetProfileConfigFn(fn func(clientIP string) *ProfileConfig) {
	s.mu.Lock()
	s.profileConfigFn = fn
	s.mu.Unlock()
}

// IsRunning reports whether the server is currently listening.
func (s *Server) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.srv != nil
}

type pageData struct {
	Config
	Joke      string
	Domain    string
	Profile   string
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	cfg := s.cfg
	customTmpl := s.customTemplate
	profileFn := s.profileConfigFn
	s.mu.Unlock()

	// M33: merge per-profile overrides when available.
	profileID := ""
	if profileFn != nil {
		clientIP := r.URL.Query().Get("client_ip")
		if clientIP == "" {
			// Fall back to the request's remote address.
			clientIP, _, _ = net.SplitHostPort(r.RemoteAddr)
		}
		if clientIP != "" {
			if pcfg := profileFn(clientIP); pcfg != nil {
				profileID = pcfg.ProfileID
				if pcfg.Title != "" {
					cfg.Title = pcfg.Title
				}
				if pcfg.Message != "" {
					cfg.Message = pcfg.Message
				}
				if pcfg.ContactEmail != "" {
					cfg.ContactEmail = pcfg.ContactEmail
				}
			}
		}
	}

	if cfg.Title == "" {
		cfg.Title = "Access Blocked"
	}
	if cfg.Message == "" {
		cfg.Message = "This website has been blocked by your network administrator."
	}

	domain := r.URL.Query().Get("domain")

	data := pageData{
		Config:  cfg,
		Joke:    jokes[rand.Intn(len(jokes))],
		Domain:  domain,
		Profile: profileID,
	}

	// M33: use custom template when one has been stored; fall back to built-in.
	tmpl := pageTmpl
	if customTmpl != "" {
		parsed, err := template.New("custom").Parse(customTmpl)
		if err != nil {
			http.Error(w, "block page template error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		tmpl = parsed
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		http.Error(w, "block page unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(buf.String()))
}

package api

import (
	"context"
	"net/http"

	"github.com/skoed/skoed/internal/cluster"
)

type principalKey struct{}

// Principal holds the authenticated identity and the set of scopes it carries.
type Principal struct {
	// Kind is "user" for Basic Auth, "token" for Bearer tokens.
	Kind string
	// ID is the username (for "user") or the token ID (for "token").
	ID string
	// Scopes granted to this principal. Basic Auth has all scopes.
	Scopes []string
	// Token is non-nil when Kind == "token".
	Token *cluster.APIToken
}

// HasScope returns true when the principal carries the requested scope.
// Basic Auth (Kind == "user") implicitly carries all scopes.
func (p *Principal) HasScope(s string) bool {
	if p.Kind == "user" {
		return true
	}
	for _, sc := range p.Scopes {
		if sc == s {
			return true
		}
	}
	return false
}

// ActorString returns the audit-log actor string ("user:<id>" or "token:<id>").
func (p *Principal) ActorString() string {
	return p.Kind + ":" + p.ID
}

func withPrincipal(r *http.Request, p *Principal) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), principalKey{}, p))
}

// PrincipalFrom extracts the authenticated principal from the request
// context. Returns nil when no principal is present (unauthenticated).
func PrincipalFrom(r *http.Request) *Principal {
	p, _ := r.Context().Value(principalKey{}).(*Principal)
	return p
}

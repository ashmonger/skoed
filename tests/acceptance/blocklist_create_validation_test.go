// Acceptance tests for the validation of POST /api/v1/blocklists.
//
// These tests carry the behavior contract in place of a functional
// specification, under the Rule 14 exception recorded in
// decisions/20260917-DirectFixForValidationDefects.md. They have no FSID
// until the retroactive functional spec is written, so they do not appear
// in the traceability matrix.
//
// The behavior they pin, one test per outcome:
//
//	TestCreateBlocklistWithoutSourceIsRefused
//	    A create carrying no source is refused; nothing is stored.
//	TestCreateBlocklistWithUnknownSourceTypeIsRefused
//	    A create whose source.type is not url or inline is refused, even
//	    when it carries domains.
//	TestCreateBlocklistInlineWithoutDomainsIsRefused
//	    A create of an inline blocklist with no domains is refused.
//	TestCreateBlocklistInlineWithDomainsIsAccepted
//	    The valid inline path still works and counts its domains.
//	TestCreateBlocklistFromUrlIsAccepted
//	    The valid url path still works and counts the downloaded domains.
//	TestCreateBlocklistWithoutIdGetsGeneratedId
//	    A create with no id still succeeds and receives a generated one.
//
// The first three defend against the fail-open measured on a 3-node
// cluster running v0.4.3: a blocklist with no usable source was committed
// enabled with zero domains, and then read as active through the API and
// the web UI while filtering nothing.

package acceptance

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// createdBlocklist is the subset of the create response these tests assert on.
type createdBlocklist struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Enabled     bool   `json:"enabled"`
	DomainCount int    `json:"domain_count"`
	Source      struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"source"`
}

// postBlocklist sends a raw create body and returns the status and the body.
// The body is passed as a string rather than built from a map so each test
// shows the exact JSON an operator would send.
func postBlocklist(t *testing.T, n *Node, body string) (int, string) {
	t.Helper()
	resp := n.apiDo(t, "POST", "/api/v1/blocklists", body)
	return resp.StatusCode, readBody(t, resp)
}

// assertRefused fails unless the create was refused with 400 and an error
// message naming the field that was wrong. A refusal that does not say what
// to fix is no better than a silent one.
func assertRefused(t *testing.T, status int, body, wantField string) {
	t.Helper()
	if status != http.StatusBadRequest {
		t.Fatalf("create must be refused with 400, got %d: %s", status, body)
	}
	if !strings.Contains(body, wantField) {
		t.Errorf("error message must name %q, got: %s", wantField, body)
	}
}

// assertNoBlocklistNamed fails if any stored blocklist carries that name.
// A refused create must leave nothing behind: the fail-open is precisely a
// list that reads as active afterwards.
func assertNoBlocklistNamed(t *testing.T, n *Node, name string) {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/blocklists", "")
	body := readBody(t, resp)
	var lists []createdBlocklist
	if err := json.Unmarshal([]byte(body), &lists); err != nil {
		t.Fatalf("decode blocklists: %v: %s", err, body)
	}
	for _, bl := range lists {
		if bl.Name == name {
			t.Fatalf("refused create was stored anyway: %+v", bl)
		}
	}
}

// assertBlocklistAbsent fails if the id can be fetched back.
func assertBlocklistAbsent(t *testing.T, n *Node, id string) {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/blocklists/"+id, "")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("refused create left %s readable (status %d): %s", id, resp.StatusCode, body)
	}
}

// startBlocklistFileServer serves a fixed hosts-format list over HTTP so the
// url path is exercised without reaching the public internet.
func startBlocklistFileServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A create carrying no source at all is refused.
//
// This is the body measured against the v0.4.3 cluster: it names neither an
// id nor a source, and its "format" and "entries" keys are not part of the
// documented schema. It returned 201 with domain_count 0, which is the
// fail-open. It must now return 400 and store nothing.
func TestCreateBlocklistWithoutSourceIsRefused(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})

	status, body := postBlocklist(t, n,
		`{"name":"no-id-no-source","format":"inline","entries":["x.example"]}`)

	assertRefused(t, status, body, "source.type")
	assertNoBlocklistNamed(t, n, "no-id-no-source")
}

// A create whose source.type is neither url nor inline is refused.
//
// Domains are supplied on purpose: the refusal is about the unrecognized
// type, not about an empty domain list. Only url and inline can be served,
// so any other value leaves the daemon with no way to load the list.
func TestCreateBlocklistWithUnknownSourceTypeIsRefused(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})

	status, body := postBlocklist(t, n,
		`{"id":"unknown-type-bl","name":"unknown type","source":{"type":"manual"},"domains":["blocked.example"]}`)

	assertRefused(t, status, body, "source.type")
	assertBlocklistAbsent(t, n, "unknown-type-bl")
}

// A create of an inline blocklist with no domains is refused.
//
// An inline list holds exactly the domains given at creation, and no route
// adds more afterwards. Accepting an empty one commits a blocklist that
// reads as enabled and can never match a query.
func TestCreateBlocklistInlineWithoutDomainsIsRefused(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})

	status, body := postBlocklist(t, n,
		`{"id":"inline-empty-bl","name":"inline empty","enabled":true,"source":{"type":"inline"}}`)

	assertRefused(t, status, body, "domains")
	assertBlocklistAbsent(t, n, "inline-empty-bl")
}

// An inline create carrying domains is accepted and counts them.
func TestCreateBlocklistInlineWithDomainsIsAccepted(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})

	status, body := postBlocklist(t, n,
		`{"id":"inline-ok-bl","name":"inline ok","source":{"type":"inline"},"domains":["ads.example","tracker.example"]}`)
	if status != http.StatusCreated {
		t.Fatalf("valid inline create: want 201, got %d: %s", status, body)
	}

	var created createdBlocklist
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("decode create response: %v: %s", err, body)
	}
	if created.DomainCount != 2 {
		t.Errorf("domain_count: want 2, got %d", created.DomainCount)
	}
	if !created.Enabled {
		t.Errorf("a blocklist created without an explicit enabled flag must be enabled")
	}
	if created.Source.Type != "inline" {
		t.Errorf("source.type: want inline, got %q", created.Source.Type)
	}
}

// A url create is accepted and counts the domains it downloaded.
func TestCreateBlocklistFromUrlIsAccepted(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})
	srv := startBlocklistFileServer(t, "0.0.0.0 ads.example\n0.0.0.0 tracker.example\n")

	status, body := postBlocklist(t, n, mustJSON(t, map[string]any{
		"id":   "url-ok-bl",
		"name": "url ok",
		"source": map[string]string{
			"type":   "url",
			"url":    srv.URL,
			"format": "hosts",
		},
	}))
	if status != http.StatusCreated {
		t.Fatalf("valid url create: want 201, got %d: %s", status, body)
	}

	var created createdBlocklist
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("decode create response: %v: %s", err, body)
	}
	if created.DomainCount != 2 {
		t.Errorf("domain_count: want 2 downloaded domains, got %d", created.DomainCount)
	}
	if created.Source.URL != srv.URL {
		t.Errorf("source.url: want %q, got %q", srv.URL, created.Source.URL)
	}
}

// A create with no id is accepted and receives a generated id.
//
// The web UI omits id unless the operator types one, so generating it stays
// part of the contract; the OpenAPI schema no longer requires it.
func TestCreateBlocklistWithoutIdGetsGeneratedId(t *testing.T) {
	t.Parallel()
	n := startNode(t, NodeConfig{})

	status, body := postBlocklist(t, n,
		`{"name":"generated id","source":{"type":"inline"},"domains":["gen.example"]}`)
	if status != http.StatusCreated {
		t.Fatalf("create without id: want 201, got %d: %s", status, body)
	}

	var created createdBlocklist
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("decode create response: %v: %s", err, body)
	}
	if created.ID == "" {
		t.Fatalf("create without id must return a generated id, got none: %s", body)
	}

	// The generated id must address the blocklist afterwards.
	resp := n.apiDo(t, "GET", "/api/v1/blocklists/"+created.ID, "")
	readBack := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("generated id %q is not addressable (status %d): %s", created.ID, resp.StatusCode, readBack)
	}
}

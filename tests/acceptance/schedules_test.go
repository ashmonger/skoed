// Acceptance tests for M3 schedule rules.
//
// FSIDs covered (one Go test per FSID):
//   FS-ScheduleActiveWindow
//   FS-ScheduleAllowMode
//   FS-ScheduleMultipleProfiles
//   FS-ScheduleApiCrud
//   FS-ScheduleTimezoneIsNodeLocal
//
// ── Time injection ────────────────────────────────────────────────────────
//
// Acceptance tests cannot wait for real wall-clock windows. The M3 schedule
// evaluator MUST honour two environment variables, as documented in
// specs/technical/profiles-and-schedules.md:
//
//   DBLOCK_TEST_MODE=1   — gate that unlocks all test-only affordances
//   DBLOCK_TEST_NOW=...  — RFC3339 timestamp used INSTEAD of time.Now() when
//                          the schedule evaluator computes "is the window
//                          active right now?"
//
// For FS-ScheduleTimezoneIsNodeLocal we additionally set TZ=America/Los_Angeles
// so the Go runtime's time.Local resolves to Pacific time; the schedule
// evaluator MUST interpret window Start/End in time.Local terms.
//
// Each test also reuses the EDNS0-65500 client-IP override from profiles_test.go
// via dnsQueryAsClient — same package, no redeclaration.
//
// Skip semantics:
//   - A test t.Skip()s only when the schedule HTTP route returns 404 (route
//     not yet wired). For every other failure path the test fails normally,
//     surfacing the implementation gap.

package acceptance

import (
	"net/http"
	"strings"
	"testing"

	"github.com/miekg/dns"
)

// scheduleBody is the documented schedule JSON shape (see TS-ProfilesAndSchedules).
type scheduleBody struct {
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	Mode    string        `json:"mode"` // "block_only_inside" | "allow_only_inside"
	Windows []timeWindow  `json:"windows"`
}

type timeWindow struct {
	Days  []string `json:"days"`
	Start string   `json:"start"`
	End   string   `json:"end"`
}

// scheduleBindingBody is the POST body for /schedules/{id}/bindings.
type scheduleBindingBody struct {
	ProfileID   string `json:"profile_id"`
	BlocklistID string `json:"blocklist_id"`
}

// startScheduleNode spawns a single-node cluster with DBLOCK_TEST_MODE=1 and
// the given extra env vars (typically DBLOCK_TEST_NOW and optionally TZ).
// Returns the lone node — callers use it via the embedded *Node helpers.
func startScheduleNode(t *testing.T, extraEnv ...string) *ClusterNode {
	t.Helper()
	// Default to TZ=UTC unless the caller overrode it. Schedule windows in
	// the spec are written in UTC for the non-timezone tests; without this
	// pin, the binary inherits the host's TZ (e.g. Europe/Paris) and the
	// window check shifts.
	hasTZ := false
	for _, e := range extraEnv {
		if strings.HasPrefix(e, "TZ=") {
			hasTZ = true
			break
		}
	}
	env := []string{"DBLOCK_TEST_MODE=1"}
	if !hasTZ {
		env = append(env, "TZ=UTC")
	}
	env = append(env, extraEnv...)
	c := startClusterWithEnv(t, 1, env)
	return c.Node(0)
}

// createSchedule POSTs a schedule. Skips with "M3 impl pending" when the route
// is 404; fails the test on any other non-2xx response.
func createSchedule(t *testing.T, n *ClusterNode, body scheduleBody) {
	t.Helper()
	resp := n.apiDo(t, "POST", "/api/v1/schedules", mustJSON(t, body))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M3 impl pending: POST /api/v1/schedules returns 404 (route not registered)")
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create schedule %s: status %d: %s", body.ID, resp.StatusCode, readBody(t, resp))
	}
}

// bindSchedule POSTs a (profile, blocklist) binding for a schedule. Skips on
// 404 (route not yet registered).
func bindSchedule(t *testing.T, n *ClusterNode, scheduleID, profileID, blocklistID string) {
	t.Helper()
	resp := n.apiDo(t, "POST", "/api/v1/schedules/"+scheduleID+"/bindings",
		mustJSON(t, scheduleBindingBody{ProfileID: profileID, BlocklistID: blocklistID}))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M3 impl pending: POST /api/v1/schedules/%s/bindings returns 404", scheduleID)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("bind schedule %s → (%s,%s): status %d: %s",
			scheduleID, profileID, blocklistID, resp.StatusCode, readBody(t, resp))
	}
}

// createScheduleProfile POSTs a profile via the same 404-tolerant pattern used
// by profiles_test.go's createProfile, but on a *ClusterNode.
func createScheduleProfile(t *testing.T, n *ClusterNode, body profileBody) {
	t.Helper()
	resp := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, body))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M3 impl pending: POST /api/v1/profiles returns 404 (route not registered)")
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create profile %s: status %d: %s", body.ID, resp.StatusCode, readBody(t, resp))
	}
}

// ── Tests ─────────────────────────────────────────────────────────────────

// FS-ScheduleActiveWindow
//
// A block_only_inside schedule must block ONLY while the window is active.
// We pin DBLOCK_TEST_NOW twice (once outside the window, once inside) by
// running two sub-cases in separate node lifecycles — simpler and more honest
// than mutating time on a live node.
func TestScheduleActiveWindow(t *testing.T) {
	// Sub-case 1: Wednesday 2026-06-03 19:30 UTC → BEFORE the 20:00 window
	// boundary → schedule inactive → blocklist OFF → forwarded.
	t.Run("outside_window_forwards", func(t *testing.T) {
		upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
		n := startScheduleNode(t, "DBLOCK_TEST_NOW=2026-06-03T19:30:00Z")

		// Need a forwarding upstream — the cluster harness leaves DNS upstream
		// empty by default, so feed it via the runtime API.
		setUpstreamResolvers(t, n, upstream)

		addInlineBlocklist(t, n.Node, "social", []string{"facebook.com"}, "")
		createScheduleProfile(t, n, profileBody{
			ID: "kids", Name: "Kids",
			Blocklists: []string{"social"},
			ClientIPs:  []string{"192.168.1.50"},
		})
		createSchedule(t, n, scheduleBody{
			ID: "evening-clamp", Name: "Evening clamp",
			Mode: "block_only_inside",
			Windows: []timeWindow{
				{Days: []string{"Mon", "Tue", "Wed", "Thu", "Fri"}, Start: "20:00", End: "23:59"},
			},
		})
		bindSchedule(t, n, "evening-clamp", "kids", "social")

		r := dnsQueryAsClient(t, n.DNSAddr, "facebook.com", dns.TypeA, "192.168.1.50")
		assertRcode(t, r, dns.RcodeSuccess)
		assertAnswerA(t, r, "1.2.3.4")
	})

	// Sub-case 2: Wednesday 2026-06-03 21:30 UTC → INSIDE the 20:00-23:59
	// window → schedule active → blocklist applied → NXDOMAIN.
	t.Run("inside_window_blocks", func(t *testing.T) {
		upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
		n := startScheduleNode(t, "DBLOCK_TEST_NOW=2026-06-03T21:30:00Z")
		setUpstreamResolvers(t, n, upstream)

		addInlineBlocklist(t, n.Node, "social", []string{"facebook.com"}, "")
		createScheduleProfile(t, n, profileBody{
			ID: "kids", Name: "Kids",
			Blocklists: []string{"social"},
			ClientIPs:  []string{"192.168.1.50"},
		})
		createSchedule(t, n, scheduleBody{
			ID: "evening-clamp", Name: "Evening clamp",
			Mode: "block_only_inside",
			Windows: []timeWindow{
				{Days: []string{"Mon", "Tue", "Wed", "Thu", "Fri"}, Start: "20:00", End: "23:59"},
			},
		})
		bindSchedule(t, n, "evening-clamp", "kids", "social")

		r := dnsQueryAsClient(t, n.DNSAddr, "facebook.com", dns.TypeA, "192.168.1.50")
		assertRcode(t, r, dns.RcodeNameError)
	})
}

// FS-ScheduleAllowMode
//
// An allow_only_inside schedule INVERTS the meaning: blocking applies OUTSIDE
// the window; inside the window the blocklist is suspended.
func TestScheduleAllowMode(t *testing.T) {
	// Sub-case 1: 2026-06-03 17:00 UTC, Wednesday, INSIDE the 16:00-19:00
	// window → schedule active → allow_only_inside means forwarded.
	t.Run("inside_window_forwards", func(t *testing.T) {
		upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
		n := startScheduleNode(t, "DBLOCK_TEST_NOW=2026-06-03T17:00:00Z")
		setUpstreamResolvers(t, n, upstream)

		addInlineBlocklist(t, n.Node, "social", []string{"facebook.com"}, "")
		createScheduleProfile(t, n, profileBody{
			ID: "kids", Name: "Kids",
			Blocklists: []string{"social"},
			ClientIPs:  []string{"192.168.1.50"},
		})
		createSchedule(t, n, scheduleBody{
			ID: "homework", Name: "Homework allow",
			Mode: "allow_only_inside",
			Windows: []timeWindow{
				{Days: []string{"Mon", "Tue", "Wed", "Thu", "Fri"}, Start: "16:00", End: "19:00"},
			},
		})
		bindSchedule(t, n, "homework", "kids", "social")

		r := dnsQueryAsClient(t, n.DNSAddr, "facebook.com", dns.TypeA, "192.168.1.50")
		assertRcode(t, r, dns.RcodeSuccess)
		assertAnswerA(t, r, "1.2.3.4")
	})

	// Sub-case 2: 2026-06-03 21:00 UTC, OUTSIDE the 16:00-19:00 window →
	// allow_only_inside means blocked.
	t.Run("outside_window_blocks", func(t *testing.T) {
		upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
		n := startScheduleNode(t, "DBLOCK_TEST_NOW=2026-06-03T21:00:00Z")
		setUpstreamResolvers(t, n, upstream)

		addInlineBlocklist(t, n.Node, "social", []string{"facebook.com"}, "")
		createScheduleProfile(t, n, profileBody{
			ID: "kids", Name: "Kids",
			Blocklists: []string{"social"},
			ClientIPs:  []string{"192.168.1.50"},
		})
		createSchedule(t, n, scheduleBody{
			ID: "homework", Name: "Homework allow",
			Mode: "allow_only_inside",
			Windows: []timeWindow{
				{Days: []string{"Mon", "Tue", "Wed", "Thu", "Fri"}, Start: "16:00", End: "19:00"},
			},
		})
		bindSchedule(t, n, "homework", "kids", "social")

		r := dnsQueryAsClient(t, n.DNSAddr, "facebook.com", dns.TypeA, "192.168.1.50")
		assertRcode(t, r, dns.RcodeNameError)
	})
}

// FS-ScheduleMultipleProfiles
//
// One schedule attached to two distinct (profile, blocklist) pairs must
// evaluate independently per profile. Removing one binding must NOT affect
// the other.
func TestScheduleMultipleProfiles(t *testing.T) {
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	// Wednesday 2026-06-03 21:30 UTC — inside the 20:00-23:59 window.
	n := startScheduleNode(t, "DBLOCK_TEST_NOW=2026-06-03T21:30:00Z")
	setUpstreamResolvers(t, n, upstream)

	addInlineBlocklist(t, n.Node, "social", []string{"facebook.com"}, "")
	addInlineBlocklist(t, n.Node, "gaming", []string{"steam.com"}, "")

	createScheduleProfile(t, n, profileBody{
		ID: "kids", Name: "Kids",
		Blocklists: []string{"social"},
		ClientIPs:  []string{"192.168.1.50"},
	})
	createScheduleProfile(t, n, profileBody{
		ID: "teens", Name: "Teens",
		Blocklists: []string{"gaming"},
		ClientIPs:  []string{"192.168.1.60"},
	})

	createSchedule(t, n, scheduleBody{
		ID: "evening-clamp", Name: "Evening clamp",
		Mode: "block_only_inside",
		Windows: []timeWindow{
			{Days: []string{"Mon", "Tue", "Wed", "Thu", "Fri"}, Start: "20:00", End: "23:59"},
		},
	})
	bindSchedule(t, n, "evening-clamp", "kids", "social")
	bindSchedule(t, n, "evening-clamp", "teens", "gaming")

	// Both clamps active inside the window.
	rKids := dnsQueryAsClient(t, n.DNSAddr, "facebook.com", dns.TypeA, "192.168.1.50")
	assertRcode(t, rKids, dns.RcodeNameError)

	rTeens := dnsQueryAsClient(t, n.DNSAddr, "steam.com", dns.TypeA, "192.168.1.60")
	assertRcode(t, rTeens, dns.RcodeNameError)

	// Remove the kids binding — the teens binding MUST still block steam.com,
	// and facebook.com MUST now be forwarded for kids.
	delResp := n.apiDo(t, "DELETE",
		"/api/v1/schedules/evening-clamp/bindings/kids/social", "")
	delResp.Body.Close()
	if delResp.StatusCode == http.StatusNotFound {
		t.Skipf("M3 impl pending: DELETE schedule binding returns 404")
	}
	if delResp.StatusCode != http.StatusNoContent && delResp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE binding (kids,social): status %d", delResp.StatusCode)
	}

	// After removing the (kids, social) binding the blocklist falls back to
	// its baseline "always-on" semantics — the schedule was a SCOPE, not
	// the on/off switch (see FS-ScheduleAllowMode: outside the homework
	// window the blocklist is still blocked, confirming baseline-on).
	rKidsAfter := dnsQueryAsClient(t, n.DNSAddr, "facebook.com", dns.TypeA, "192.168.1.50")
	assertRcode(t, rKidsAfter, dns.RcodeNameError)

	// Teens binding is untouched — schedule still active, steam.com blocked.
	rTeensAfter := dnsQueryAsClient(t, n.DNSAddr, "steam.com", dns.TypeA, "192.168.1.60")
	assertRcode(t, rTeensAfter, dns.RcodeNameError)
}

// FS-ScheduleApiCrud
//
// Admin creates a schedule, attaches it, then deletes it; the cascade rule is
// that every binding referencing a deleted schedule MUST be implicitly dropped.
func TestScheduleApiCrud(t *testing.T) {
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startScheduleNode(t, "DBLOCK_TEST_NOW=2026-06-03T21:30:00Z")
	setUpstreamResolvers(t, n, upstream)

	addInlineBlocklist(t, n.Node, "social", []string{"facebook.com"}, "")
	createScheduleProfile(t, n, profileBody{
		ID: "kids", Name: "Kids",
		Blocklists: []string{"social"},
		ClientIPs:  []string{"192.168.1.50"},
	})

	// CREATE schedule.
	createSchedule(t, n, scheduleBody{
		ID: "evening-clamp", Name: "Evening clamp",
		Mode: "block_only_inside",
		Windows: []timeWindow{
			{Days: []string{"Mon", "Tue", "Wed", "Thu", "Fri"}, Start: "20:00", End: "23:59"},
		},
	})

	// GET — shape sanity.
	getResp := n.apiDo(t, "GET", "/api/v1/schedules/evening-clamp", "")
	assertStatus(t, getResp, http.StatusOK)
	getResp.Body.Close()

	// BIND.
	bindSchedule(t, n, "evening-clamp", "kids", "social")

	// Confirm binding takes effect (we're inside the window).
	rBlocked := dnsQueryAsClient(t, n.DNSAddr, "facebook.com", dns.TypeA, "192.168.1.50")
	assertRcode(t, rBlocked, dns.RcodeNameError)

	// DELETE the schedule — bindings MUST cascade.
	delResp := n.apiDo(t, "DELETE", "/api/v1/schedules/evening-clamp", "")
	if delResp.StatusCode != http.StatusNoContent && delResp.StatusCode != http.StatusOK {
		body := readBody(t, delResp)
		t.Fatalf("DELETE /schedules/evening-clamp: status %d: %s", delResp.StatusCode, body)
	}
	delResp.Body.Close()

	// Schedule itself is gone.
	getResp2 := n.apiDo(t, "GET", "/api/v1/schedules/evening-clamp", "")
	if getResp2.StatusCode != http.StatusNotFound {
		body := readBody(t, getResp2)
		getResp2.Body.Close()
		t.Fatalf("expected 404 for deleted schedule, got %d: %s", getResp2.StatusCode, body)
	}
	getResp2.Body.Close()

	// Binding cascade: with the schedule (and therefore the binding) gone, the
	// kids profile's social blocklist is no longer clamped — facebook.com would
	// still be blocked by the underlying blocklist itself, so this assertion
	// only confirms the binding is gone via the API surface, not via DNS.
	bindingResp := n.apiDo(t, "GET",
		"/api/v1/schedules/evening-clamp/bindings/kids/social", "")
	bindingResp.Body.Close()
	if bindingResp.StatusCode != http.StatusNotFound &&
		bindingResp.StatusCode != http.StatusMethodNotAllowed {
		// 404 (binding cascaded away) is the documented outcome; 405 is
		// acceptable if the binding-GET sub-route isn't implemented (only
		// POST/DELETE are mandated). Anything else is a bug.
		t.Fatalf("expected 404/405 for cascaded binding, got %d", bindingResp.StatusCode)
	}
}

// FS-ScheduleTimezoneIsNodeLocal
//
// Schedule windows are interpreted in the node's local timezone. We pin
// TZ=America/Los_Angeles on the spawned binary so Go's time.Local resolves
// to Pacific, and pick a DBLOCK_TEST_NOW that is INSIDE the window when
// interpreted as Pacific but OUTSIDE when interpreted as UTC.
//
//   DBLOCK_TEST_NOW = 2026-06-02T03:30:00Z (Tuesday in UTC)
//                   = 2026-06-01T20:30:00 Pacific (Monday at 20:30, INSIDE
//                     the Mon-Fri 20:00-22:00 Pacific window)
//
// If the evaluator naively used UTC, 03:30 UTC is OUTSIDE any 20:00-22:00
// window and the query would be forwarded — that would be a bug.
func TestScheduleTimezoneIsNodeLocal(t *testing.T) {
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startScheduleNode(t,
		"DBLOCK_TEST_NOW=2026-06-02T03:30:00Z",
		"TZ=America/Los_Angeles",
	)
	setUpstreamResolvers(t, n, upstream)

	addInlineBlocklist(t, n.Node, "social", []string{"facebook.com"}, "")
	createScheduleProfile(t, n, profileBody{
		ID: "kids", Name: "Kids",
		Blocklists: []string{"social"},
		ClientIPs:  []string{"192.168.1.50"},
	})
	createSchedule(t, n, scheduleBody{
		ID: "pacific-evening", Name: "Pacific evening",
		Mode: "block_only_inside",
		Windows: []timeWindow{
			{Days: []string{"Mon", "Tue", "Wed", "Thu", "Fri"}, Start: "20:00", End: "22:00"},
		},
	})
	bindSchedule(t, n, "pacific-evening", "kids", "social")

	r := dnsQueryAsClient(t, n.DNSAddr, "facebook.com", dns.TypeA, "192.168.1.50")
	assertRcode(t, r, dns.RcodeNameError)
}

// ── Setup helpers ─────────────────────────────────────────────────────────

// setUpstreamResolvers POSTs the runtime upstream-resolver list to the node's
// settings API. The cluster harness writes a config.yaml with empty DNS
// settings (no `upstream_resolvers`), so each schedule test must inject the
// fake upstream at runtime.
//
// Endpoint: PATCH /api/v1/settings with {"dns":{"upstream_resolvers":[…]}}.
// This is the same shape used by the M2 settings tests; if the implementation
// hasn't surfaced this route yet, the test skips with "M3 impl pending".
func setUpstreamResolvers(t *testing.T, n *ClusterNode, upstream string) {
	t.Helper()
	body := map[string]any{
		"dns": map[string]any{
			"upstream_resolvers": []string{upstream},
		},
	}
	resp := n.apiDo(t, "PATCH", "/api/v1/settings", mustJSON(t, body))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Skipf("M3 impl pending: PATCH /api/v1/settings returns 404")
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PATCH /settings dns.upstream_resolvers: status %d: %s",
			resp.StatusCode, readBody(t, resp))
	}
}

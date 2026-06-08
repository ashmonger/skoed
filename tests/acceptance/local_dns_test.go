// Acceptance tests for local DNS entry management.
//
// FSIDs covered:
//   FS-LocalDnsEntryAddA, FS-LocalDnsEntryAddAAAA, FS-LocalDnsEntryAddCNAME,
//   FS-LocalDnsEntryPriorityOverUpstream, FS-LocalDnsEntryPriorityOverBlocklist,
//   FS-LocalDnsEntryUpdate, FS-LocalDnsEntryDelete,
//   FS-LocalDnsEntryNxdomainWhenNoUpstream

package acceptance

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/miekg/dns"
)

// ── Helpers ───────────────────────────────────────────────────────────────

// localDNSEntry is used to decode the JSON response from POST/PUT /api/v1/local-dns.
type localDNSEntry struct {
	ID       string `json:"id"`
	Hostname string `json:"hostname"`
	Type     string `json:"type"`
	Value    string `json:"value"`
	TTL      int    `json:"ttl"`
}

// addLocalA adds a local A record via the API and returns the created entry.
func addLocalA(t *testing.T, n *Node, hostname, ip string) localDNSEntry {
	t.Helper()
	body := mustJSON(t, map[string]any{
		"hostname": hostname,
		"type":     "A",
		"value":    ip,
		"ttl":      300,
	})
	resp := n.apiDo(t, "POST", "/api/v1/local-dns", body)
	assertStatus(t, resp, http.StatusCreated)
	defer resp.Body.Close()

	var entry localDNSEntry
	if err := json.NewDecoder(resp.Body).Decode(&entry); err != nil {
		t.Fatalf("decode local DNS entry: %v", err)
	}
	return entry
}

// addLocalAAAA adds a local AAAA record via the API and returns the created entry.
func addLocalAAAA(t *testing.T, n *Node, hostname, ip string) localDNSEntry {
	t.Helper()
	body := mustJSON(t, map[string]any{
		"hostname": hostname,
		"type":     "AAAA",
		"value":    ip,
		"ttl":      300,
	})
	resp := n.apiDo(t, "POST", "/api/v1/local-dns", body)
	assertStatus(t, resp, http.StatusCreated)
	defer resp.Body.Close()

	var entry localDNSEntry
	if err := json.NewDecoder(resp.Body).Decode(&entry); err != nil {
		t.Fatalf("decode local DNS entry: %v", err)
	}
	return entry
}

// ── Tests ─────────────────────────────────────────────────────────────────

// FS-LocalDnsEntryAddA
// Admin adds an A record for an internal hostname; client resolves it correctly.
func TestLocalDnsEntryAddA(t *testing.T) {
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	addLocalA(t, n, "nas.home", "192.168.1.50")

	r := dnsQuery(t, n.DNSAddr, "nas.home", dns.TypeA)
	assertRcode(t, r, dns.RcodeSuccess)
	assertAnswerA(t, r, "192.168.1.50")

	// Verify TTL is 300
	for _, rr := range r.Answer {
		if a, ok := rr.(*dns.A); ok {
			if a.Hdr.Ttl != 300 {
				t.Fatalf("expected TTL 300, got %d", a.Hdr.Ttl)
			}
		}
	}
}

// FS-LocalDnsEntryAddAAAA
// Admin adds an AAAA record; client resolves it via AAAA query.
func TestLocalDnsEntryAddAAAA(t *testing.T) {
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	addLocalAAAA(t, n, "nas.home", "fd00::50")

	r := dnsQuery(t, n.DNSAddr, "nas.home", dns.TypeAAAA)
	assertRcode(t, r, dns.RcodeSuccess)
	assertAnswerAAAA(t, r, "fd00::50")
}

// FS-LocalDnsEntryAddCNAME
// Admin adds a CNAME record pointing to another hostname that has a local A record.
// Client receives the CNAME record in the answer section.
func TestLocalDnsEntryAddCNAME(t *testing.T) {
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	// Add the target A record first
	addLocalA(t, n, "nas.home", "192.168.1.50")

	// Add the CNAME
	body := mustJSON(t, map[string]any{
		"hostname": "files.home",
		"type":     "CNAME",
		"value":    "nas.home",
		"ttl":      300,
	})
	resp := n.apiDo(t, "POST", "/api/v1/local-dns", body)
	assertStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	r := dnsQuery(t, n.DNSAddr, "files.home", dns.TypeA)
	assertRcode(t, r, dns.RcodeSuccess)

	// Verify a CNAME record is present in the answer
	hasCNAME := false
	for _, rr := range r.Answer {
		if cname, ok := rr.(*dns.CNAME); ok {
			hasCNAME = true
			if cname.Target != dns.Fqdn("nas.home") {
				t.Fatalf("expected CNAME target nas.home., got %s", cname.Target)
			}
		}
	}
	if !hasCNAME {
		t.Fatalf("expected CNAME record in answer section, got: %v", r.Answer)
	}

	// The A record for nas.home should be present in answer or additional section
	hasA := false
	for _, rr := range append(r.Answer, r.Extra...) {
		if a, ok := rr.(*dns.A); ok {
			if a.A.String() == "192.168.1.50" {
				hasA = true
				break
			}
		}
	}
	if !hasA {
		t.Fatalf("expected A record 192.168.1.50 for nas.home in answer or additional section, got answer=%v extra=%v", r.Answer, r.Extra)
	}
}

// FS-LocalDnsEntryPriorityOverUpstream
// A local entry is returned without contacting any upstream resolver.
func TestLocalDnsEntryPriorityOverUpstream(t *testing.T) {
	upstreamContacted := false
	upstream := startFakeUpstream(t, func(w dns.ResponseWriter, r *dns.Msg) {
		upstreamContacted = true
		m := new(dns.Msg)
		m.SetReply(r)
		w.WriteMsg(m) //nolint:errcheck
	})

	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	addLocalA(t, n, "nas.home", "192.168.1.50")

	r := dnsQuery(t, n.DNSAddr, "nas.home", dns.TypeA)
	assertRcode(t, r, dns.RcodeSuccess)
	assertAnswerA(t, r, "192.168.1.50")

	if upstreamContacted {
		t.Fatal("skoed contacted the upstream resolver for a local DNS entry")
	}
}

// FS-LocalDnsEntryPriorityOverBlocklist
// A local entry is served even when the hostname appears on an active blocklist.
func TestLocalDnsEntryPriorityOverBlocklist(t *testing.T) {
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
	})

	// Block the hostname first
	addInlineBlocklist(t, n, "internal", []string{"intranet.home"}, "")

	// Confirm it is blocked before adding the local entry
	rBlocked := dnsQuery(t, n.DNSAddr, "intranet.home", dns.TypeA)
	assertRcode(t, rBlocked, dns.RcodeNameError)

	// Now add the local A record
	addLocalA(t, n, "intranet.home", "10.0.0.1")

	// Local record must be returned, not the block response
	r := dnsQuery(t, n.DNSAddr, "intranet.home", dns.TypeA)
	assertRcode(t, r, dns.RcodeSuccess)
	assertAnswerA(t, r, "10.0.0.1")
}

// FS-LocalDnsEntryUpdate
// Admin updates an existing local entry; client receives the new address.
func TestLocalDnsEntryUpdate(t *testing.T) {
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	entry := addLocalA(t, n, "nas.home", "192.168.1.50")

	// Update the address
	updateBody := mustJSON(t, map[string]any{
		"hostname": "nas.home",
		"type":     "A",
		"value":    "192.168.1.51",
		"ttl":      300,
	})
	resp := n.apiDo(t, "PUT", "/api/v1/local-dns/"+entry.ID, updateBody)
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Client should now receive the updated address
	r := dnsQuery(t, n.DNSAddr, "nas.home", dns.TypeA)
	assertRcode(t, r, dns.RcodeSuccess)
	assertAnswerA(t, r, "192.168.1.51")
}

// FS-LocalDnsEntryDelete
// Admin deletes a local entry; subsequent query is forwarded to upstream.
func TestLocalDnsEntryDelete(t *testing.T) {
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("93.184.216.34"))
	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	entry := addLocalA(t, n, "nas.home", "192.168.1.50")

	// Confirm local record is served before deletion
	r1 := dnsQuery(t, n.DNSAddr, "nas.home", dns.TypeA)
	assertRcode(t, r1, dns.RcodeSuccess)
	assertAnswerA(t, r1, "192.168.1.50")

	// Delete the entry
	resp := n.apiDo(t, "DELETE", "/api/v1/local-dns/"+entry.ID, "")
	assertStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()

	// Query should now hit upstream and return its answer
	r2 := dnsQuery(t, n.DNSAddr, "nas.home", dns.TypeA)
	assertRcode(t, r2, dns.RcodeSuccess)
	assertAnswerA(t, r2, "93.184.216.34")
}

// FS-LocalDnsEntryNxdomainWhenNoUpstream
// After deleting a local entry for a domain that does not exist in upstream DNS,
// the response is NXDOMAIN.
func TestLocalDnsEntryNxdomainWhenNoUpstream(t *testing.T) {
	// Upstream that knows nothing about internal.home
	upstream := startFakeUpstream(t, func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		m.SetRcode(r, dns.RcodeNameError)
		w.WriteMsg(m) //nolint:errcheck
	})

	n := startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
	})

	entry := addLocalA(t, n, "internal.home", "10.0.0.2")

	// Delete the local entry
	resp := n.apiDo(t, "DELETE", "/api/v1/local-dns/"+entry.ID, "")
	assertStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()

	// Upstream returns NXDOMAIN; skoed must propagate it
	r := dnsQuery(t, n.DNSAddr, "internal.home", dns.TypeA)
	assertRcode(t, r, dns.RcodeNameError)
}

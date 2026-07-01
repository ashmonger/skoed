// Acceptance tests for M35.5 Named Device Registry.
//
// Covers FSIDs:
//
//	FS-DeviceRegistryCreate
//	FS-DeviceRegistryUpdate
//	FS-DeviceRegistryDelete
//	FS-DeviceRegistryNameUnique
//	FS-DeviceProfileMatchExclusive
//	FS-DeviceMultiNicSingleConfig
//	FS-DeviceMatchPriorityHighestTier
//	FS-DeviceQueryLogEnrichment
package acceptance

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/miekg/dns"
)

// deviceBody mirrors the Device JSON schema.
type deviceBody struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	ProfileID string   `json:"profile_id"`
	MACs      []string `json:"macs"`
	IPs       []string `json:"ips"`
	Hostnames []string `json:"hostnames"`
	ClientIDs []string `json:"client_ids"`
}

// createDevice POSTs to /api/v1/devices and returns the created device body.
func createDevice(t *testing.T, n *ClusterNode, payload map[string]any) deviceBody {
	t.Helper()
	resp := n.apiDo(t, "POST", "/api/v1/devices", mustJSON(t, payload))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/v1/devices: expected 201, got %d: %s", resp.StatusCode, readBody(t, resp))
	}
	var dev deviceBody
	if err := json.NewDecoder(resp.Body).Decode(&dev); err != nil {
		t.Fatalf("decode device: %v", err)
	}
	return dev
}

// getDevice fetches a device by ID. Returns the device and HTTP status.
func getDevice(t *testing.T, n *ClusterNode, id string) (deviceBody, int) {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/devices/"+id, "")
	defer resp.Body.Close()
	var dev deviceBody
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&dev); err != nil {
			t.Fatalf("decode device: %v", err)
		}
	}
	return dev, resp.StatusCode
}

// listDevices fetches all registered devices.
func listDevices(t *testing.T, n *ClusterNode) []deviceBody {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/devices", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/devices: expected 200, got %d: %s", resp.StatusCode, readBody(t, resp))
	}
	var devs []deviceBody
	if err := json.NewDecoder(resp.Body).Decode(&devs); err != nil {
		t.Fatalf("decode devices list: %v", err)
	}
	return devs
}

// patchDevice sends a PATCH to /api/v1/devices/{id}.
func patchDevice(t *testing.T, n *ClusterNode, id string, patch map[string]any) (deviceBody, int) {
	t.Helper()
	resp := n.apiDo(t, "PATCH", "/api/v1/devices/"+id, mustJSON(t, patch))
	defer resp.Body.Close()
	var dev deviceBody
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&dev); err != nil {
			t.Fatalf("decode patched device: %v", err)
		}
	}
	return dev, resp.StatusCode
}

// deleteDevice sends DELETE /api/v1/devices/{id} and returns the status code.
func deleteDevice(t *testing.T, n *ClusterNode, id string) int {
	t.Helper()
	resp := n.apiDo(t, "DELETE", "/api/v1/devices/"+id, "")
	defer resp.Body.Close()
	return resp.StatusCode
}

// containsMAC reports whether any entry in macs equals mac.
func containsMAC(macs []string, mac string) bool {
	for _, m := range macs {
		if m == mac {
			return true
		}
	}
	return false
}

// ensureDefaultProfile creates a "default" profile if it does not already exist.
func ensureDefaultProfile(t *testing.T, n *ClusterNode) {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/profiles/default", "")
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return
	}
	r2 := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":   "default",
		"name": "default",
	}))
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusCreated && r2.StatusCode != http.StatusOK {
		t.Fatalf("create default profile: %d: %s", r2.StatusCode, readBody(t, r2))
	}
}

// createInlineBlocklist creates a blocklist with a single inline domain and returns its ID.
func createInlineBlocklist(t *testing.T, n *ClusterNode, id, domain string) string {
	t.Helper()
	resp := n.apiDo(t, "POST", "/api/v1/blocklists", mustJSON(t, map[string]any{
		"id":      id,
		"name":    id,
		"enabled": true,
		"source":  map[string]string{"type": "inline"},
		"domains": []string{domain},
	}))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("create blocklist %s: %d", id, resp.StatusCode)
	}
	return id
}

// createProfileWithBlocklist creates a profile that has one blocklist active.
func createProfileWithBlocklist(t *testing.T, n *ClusterNode, id, name, blocklistID string) {
	t.Helper()
	resp := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":         id,
		"name":       name,
		"blocklists": []string{blocklistID},
	}))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("create profile %s: %d: %s", id, resp.StatusCode, readBody(t, resp))
	}
}

// createProfileWithCIDRAndBlocklist creates a profile with a CIDR selector and one blocklist.
func createProfileWithCIDRAndBlocklist(t *testing.T, n *ClusterNode, id, name, cidr, blocklistID string) {
	t.Helper()
	resp := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":           id,
		"name":         name,
		"client_cidrs": []string{cidr},
		"blocklists":   []string{blocklistID},
	}))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("create profile %s with CIDR: %d: %s", id, resp.StatusCode, readBody(t, resp))
	}
}

// createProfileWithMACAndBlocklist creates a profile with a MAC selector and one blocklist.
func createProfileWithMACAndBlocklist(t *testing.T, n *ClusterNode, id, name, mac, blocklistID string) {
	t.Helper()
	resp := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":          id,
		"name":        name,
		"client_macs": []string{mac},
		"blocklists":  []string{blocklistID},
	}))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("create profile %s with MAC: %d: %s", id, resp.StatusCode, readBody(t, resp))
	}
}

// ─── Single-node helpers (for DNS filtering tests) ───────────────────────────

// nodeCreateInlineBlocklist is the *Node variant of createInlineBlocklist.
func nodeCreateInlineBlocklist(t *testing.T, n *Node, id, domain string) string {
	t.Helper()
	resp := n.apiDo(t, "POST", "/api/v1/blocklists", mustJSON(t, map[string]any{
		"id":      id,
		"name":    id,
		"enabled": true,
		"source":  map[string]string{"type": "inline"},
		"domains": []string{domain},
	}))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("create blocklist %s: %d", id, resp.StatusCode)
	}
	return id
}

func nodeCreateProfileWithBlocklist(t *testing.T, n *Node, id, name, blocklistID string) {
	t.Helper()
	resp := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":         id,
		"name":       name,
		"blocklists": []string{blocklistID},
	}))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("create profile %s: %d", id, resp.StatusCode)
	}
}

func nodeCreateProfileWithCIDRAndBlocklist(t *testing.T, n *Node, id, name, cidr, blocklistID string) {
	t.Helper()
	resp := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":           id,
		"name":         name,
		"client_cidrs": []string{cidr},
		"blocklists":   []string{blocklistID},
	}))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("create profile %s with CIDR: %d", id, resp.StatusCode)
	}
}

func nodeCreateProfileWithMACAndBlocklist(t *testing.T, n *Node, id, name, mac, blocklistID string) {
	t.Helper()
	resp := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":          id,
		"name":        name,
		"client_macs": []string{mac},
		"blocklists":  []string{blocklistID},
	}))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("create profile %s with MAC: %d", id, resp.StatusCode)
	}
}

func nodeCreateDevice(t *testing.T, n *Node, payload map[string]any) deviceBody {
	t.Helper()
	resp := n.apiDo(t, "POST", "/api/v1/devices", mustJSON(t, payload))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/v1/devices: expected 201, got %d: %s", resp.StatusCode, readBody(t, resp))
	}
	var dev deviceBody
	if err := json.NewDecoder(resp.Body).Decode(&dev); err != nil {
		t.Fatalf("decode device: %v", err)
	}
	return dev
}

// startDeviceTestNode starts a forwarding single-node with SKOED_TEST_MODE=1.
func startDeviceTestNode(t *testing.T, upstream string) *Node {
	t.Helper()
	return startNode(t, NodeConfig{
		Mode:              "forwarding",
		UpstreamResolvers: []string{upstream},
		BlockPolicy:       "nxdomain",
		Env:               []string{"SKOED_TEST_MODE=1"},
	})
}

func nodeEnsureDefaultProfile(t *testing.T, n *Node) {
	t.Helper()
	resp := n.apiDo(t, "GET", "/api/v1/profiles/default", "")
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return
	}
	r2 := n.apiDo(t, "POST", "/api/v1/profiles", mustJSON(t, map[string]any{
		"id":   "default",
		"name": "default",
	}))
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusCreated && r2.StatusCode != http.StatusOK {
		t.Fatalf("create default profile: %d: %s", r2.StatusCode, readBody(t, r2))
	}
}

// dnsQueryAsMAC sends a DNS query with EDNS0 code 65501 carrying the source MAC.
// When SKOED_TEST_MODE=1, the engine uses this MAC as the primary identifier for
// device-registry lookup.
func dnsQueryAsMAC(t *testing.T, server, name string, qtype uint16, mac string) *dns.Msg {
	t.Helper()
	c := &dns.Client{Net: "udp", Timeout: dnsQueryTimeout}
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	m.SetEdns0(4096, false)
	opt := m.IsEdns0()
	if opt == nil {
		t.Fatal("dnsQueryAsMAC: SetEdns0 did not attach an OPT RR")
	}
	opt.Option = append(opt.Option, &dns.EDNS0_LOCAL{
		Code: 65501, // private-use code for MAC-based client identification
		Data: []byte(mac),
	})
	r, _, err := c.Exchange(m, server)
	if err != nil {
		t.Fatalf("DNS query as MAC %s %s %s @%s: %v", mac, name, dns.TypeToString[qtype], server, err)
	}
	return r
}

// ────────────────────────────────────────────────────────────
// FS-DeviceRegistryCreate
// ────────────────────────────────────────────────────────────

func TestDeviceRegistryCreate(t *testing.T) {
	// FSID: FS-DeviceRegistryCreate
	c := startCluster(t, 1)
	n := c.Node(0)
	ensureDefaultProfile(t, n)

	dev := createDevice(t, n, map[string]any{
		"name":       "workstation-01",
		"profile_id": "default",
		"macs":       []string{"aa:bb:cc:dd:ee:01", "aa:bb:cc:dd:ee:02"},
		"ips":        []string{"192.168.1.10"},
		"hostnames":  []string{"workstation-01.lan"},
	})

	if dev.Name != "workstation-01" {
		t.Errorf("name: got %q, want %q", dev.Name, "workstation-01")
	}
	if dev.ID == "" {
		t.Error("expected non-empty device ID")
	}
	if !containsMAC(dev.MACs, "aa:bb:cc:dd:ee:01") || !containsMAC(dev.MACs, "aa:bb:cc:dd:ee:02") {
		t.Errorf("MACs: got %v, want both MACs present", dev.MACs)
	}

	devs := listDevices(t, n)
	found := false
	for _, d := range devs {
		if d.Name == "workstation-01" {
			found = true
		}
	}
	if !found {
		t.Error("created device not found in GET /api/v1/devices")
	}
}

// ────────────────────────────────────────────────────────────
// FS-DeviceRegistryUpdate
// ────────────────────────────────────────────────────────────

func TestDeviceRegistryUpdate(t *testing.T) {
	// FSID: FS-DeviceRegistryUpdate
	c := startCluster(t, 1)
	n := c.Node(0)
	ensureDefaultProfile(t, n)

	dev := createDevice(t, n, map[string]any{
		"name":       "workstation-update",
		"profile_id": "default",
		"macs":       []string{"aa:bb:cc:dd:ee:01"},
	})

	updated, status := patchDevice(t, n, dev.ID, map[string]any{
		"macs": []string{"aa:bb:cc:dd:ee:01", "aa:bb:cc:dd:ee:02"},
	})
	if status != http.StatusOK {
		t.Fatalf("PATCH device: expected 200, got %d", status)
	}
	if !containsMAC(updated.MACs, "aa:bb:cc:dd:ee:02") {
		t.Errorf("expected second MAC after patch, got %v", updated.MACs)
	}
}

// ────────────────────────────────────────────────────────────
// FS-DeviceRegistryDelete
// ────────────────────────────────────────────────────────────

func TestDeviceRegistryDelete(t *testing.T) {
	// FSID: FS-DeviceRegistryDelete
	c := startCluster(t, 1)
	n := c.Node(0)
	ensureDefaultProfile(t, n)

	dev := createDevice(t, n, map[string]any{
		"name":       "workstation-delete",
		"profile_id": "default",
		"macs":       []string{"aa:bb:cc:dd:ee:ff"},
	})

	status := deleteDevice(t, n, dev.ID)
	if status != http.StatusNoContent {
		t.Fatalf("DELETE device: expected 204, got %d", status)
	}

	_, getStatus := getDevice(t, n, dev.ID)
	if getStatus != http.StatusNotFound {
		t.Errorf("after delete, GET returned %d, expected 404", getStatus)
	}

	devs := listDevices(t, n)
	for _, d := range devs {
		if d.Name == "workstation-delete" {
			t.Error("deleted device still appears in GET /api/v1/devices")
		}
	}
}

// ────────────────────────────────────────────────────────────
// FS-DeviceRegistryNameUnique
// ────────────────────────────────────────────────────────────

func TestDeviceRegistryNameUnique(t *testing.T) {
	// FSID: FS-DeviceRegistryNameUnique
	c := startCluster(t, 1)
	n := c.Node(0)
	ensureDefaultProfile(t, n)

	createDevice(t, n, map[string]any{
		"name":       "unique-device",
		"profile_id": "default",
	})

	resp := n.apiDo(t, "POST", "/api/v1/devices", mustJSON(t, map[string]any{
		"name":       "unique-device",
		"profile_id": "default",
	}))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("duplicate device name: expected 409, got %d", resp.StatusCode)
	}
}

// ────────────────────────────────────────────────────────────
// FS-DeviceProfileMatchExclusive
// ────────────────────────────────────────────────────────────

func TestDeviceProfileMatchExclusive(t *testing.T) {
	// FSID: FS-DeviceProfileMatchExclusive
	// A device match must short-circuit; the Default profile with a CIDR that
	// covers the same IP must NOT also apply.
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startDeviceTestNode(t, upstream)

	kidsBlocklist := nodeCreateInlineBlocklist(t, n, "device-kids-bl", "blocked-by-kids.example")
	nodeCreateProfileWithBlocklist(t, n, "kids-profile", "kids-profile", kidsBlocklist)

	defaultBlocklist := nodeCreateInlineBlocklist(t, n, "device-default-bl", "blocked-by-default.example")
	nodeCreateProfileWithCIDRAndBlocklist(t, n, "default", "default", "192.168.55.0/24", defaultBlocklist)

	nodeCreateDevice(t, n, map[string]any{
		"name":       "exclusive-test-device",
		"profile_id": "kids-profile",
		"ips":        []string{"192.168.55.10"},
	})

	addr := n.DNSAddr

	// blocked-by-default.example: default profile has it, kids-profile does not.
	// With device match, only kids-profile applies → should resolve (success).
	r := dnsQueryAsClient(t, addr, "blocked-by-default.example.", dns.TypeA, "192.168.55.10")
	assertRcode(t, r, dns.RcodeSuccess)

	// blocked-by-kids.example: kids-profile blocks it → should be NXDOMAIN.
	r2 := dnsQueryAsClient(t, addr, "blocked-by-kids.example.", dns.TypeA, "192.168.55.10")
	assertRcode(t, r2, dns.RcodeNameError)
}

// ────────────────────────────────────────────────────────────
// FS-DeviceMultiNicSingleConfig
// ────────────────────────────────────────────────────────────

func TestDeviceMultiNicSingleConfig(t *testing.T) {
	// FSID: FS-DeviceMultiNicSingleConfig
	// Both MACs on a single device must yield the same profile for DNS queries.
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startDeviceTestNode(t, upstream)

	blocklist := nodeCreateInlineBlocklist(t, n, "multi-nic-bl", "blocked-multi-nic.example")
	nodeCreateProfileWithBlocklist(t, n, "servers-profile", "servers-profile", blocklist)

	nodeCreateDevice(t, n, map[string]any{
		"name":       "dual-nic-server",
		"profile_id": "servers-profile",
		"macs":       []string{"bb:bb:bb:bb:bb:01", "bb:bb:bb:bb:bb:02"},
	})

	addr := n.DNSAddr
	for _, mac := range []string{"bb:bb:bb:bb:bb:01", "bb:bb:bb:bb:bb:02"} {
		r := dnsQueryAsMAC(t, addr, "blocked-multi-nic.example.", dns.TypeA, mac)
		assertRcode(t, r, dns.RcodeNameError)
	}
}

// ────────────────────────────────────────────────────────────
// FS-DeviceMatchPriorityHighestTier
// ────────────────────────────────────────────────────────────

func TestDeviceMatchPriorityHighestTier(t *testing.T) {
	// FSID: FS-DeviceMatchPriorityHighestTier
	// Device registry must outrank the profile MAC selector.
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startDeviceTestNode(t, upstream)

	// "trusted-profile" does NOT block blocked-by-restricted.example.
	nodeCreateProfileWithBlocklist(t, n, "trusted-profile", "trusted-profile", "nonexistent-bl")

	restrictedBl := nodeCreateInlineBlocklist(t, n, "restricted-bl", "blocked-by-restricted.example")
	// "restricted-profile" blocks it AND has MAC "cc:cc:cc:cc:cc:cc" in its selector.
	nodeCreateProfileWithMACAndBlocklist(t, n, "restricted-profile", "restricted-profile", "cc:cc:cc:cc:cc:cc", restrictedBl)

	// Device registry maps "cc:cc:cc:cc:cc:cc" → trusted-profile (must win).
	nodeCreateDevice(t, n, map[string]any{
		"name":       "priority-test-device",
		"profile_id": "trusted-profile",
		"macs":       []string{"cc:cc:cc:cc:cc:cc"},
	})

	addr := n.DNSAddr
	r := dnsQueryAsMAC(t, addr, "blocked-by-restricted.example.", dns.TypeA, "cc:cc:cc:cc:cc:cc")
	// trusted-profile does not block → expect success (not NXDOMAIN).
	assertRcode(t, r, dns.RcodeSuccess)
}

// ────────────────────────────────────────────────────────────
// FS-DeviceQueryLogEnrichment
// ────────────────────────────────────────────────────────────

func TestDeviceQueryLogEnrichment(t *testing.T) {
	// FSID: FS-DeviceQueryLogEnrichment
	upstream := startFakeUpstream(t, fakeUpstreamReturnsA("1.2.3.4"))
	n := startDeviceTestNode(t, upstream)
	nodeEnsureDefaultProfile(t, n)

	nodeCreateDevice(t, n, map[string]any{
		"name":       "log-test-device",
		"profile_id": "default",
		"ips":        []string{"192.168.99.10"},
	})

	addr := n.DNSAddr
	dnsQueryAsClient(t, addr, "example.com.", dns.TypeA, "192.168.99.10")

	resp := n.apiDo(t, "GET", "/api/v1/query-log", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/query-log: %d: %s", resp.StatusCode, readBody(t, resp))
	}
	var qlResp struct {
		Entries []map[string]any `json:"entries"`
		Total   int              `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&qlResp); err != nil {
		t.Fatalf("decode query log: %v", err)
	}

	found := false
	for _, e := range qlResp.Entries {
		ip, _ := e["client"].(string)
		if ip != "192.168.99.10" {
			continue
		}
		found = true
		name, _ := e["device_name"].(string)
		if name != "log-test-device" {
			t.Errorf("query log entry for 192.168.99.10: device_name=%q, want %q", name, "log-test-device")
		}
	}
	if !found {
		t.Error("no query log entry found for 192.168.99.10")
	}
}

// ────────────────────────────────────────────────────────────
// Replication (FS-DeviceRegistryCreate — cluster clause)
// ────────────────────────────────────────────────────────────

func TestDeviceRegistryReplicatedAcrossCluster(t *testing.T) {
	// FSID: FS-DeviceRegistryCreate
	c := startCluster(t, 3)
	leader := c.Leader(t)
	ensureDefaultProfile(t, leader)

	dev := createDevice(t, leader, map[string]any{
		"name":       "replicated-device",
		"profile_id": "default",
		"macs":       []string{"dd:dd:dd:dd:dd:01"},
	})

	c.WaitConverged(t)

	for i := 0; i < c.Size(); i++ {
		n := c.Node(i)
		devs := listDevices(t, n)
		found := false
		for _, d := range devs {
			if d.ID == dev.ID {
				found = true
			}
		}
		if !found {
			t.Errorf("node %d: replicated-device not found after cluster convergence", i)
		}
	}
}

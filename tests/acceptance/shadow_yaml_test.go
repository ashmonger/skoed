// Acceptance tests for the on-disk shadow YAML.
//
// Covers FSIDs:
//   FS-ConfigShadowYamlPresentOnDisk
//   FS-ConfigShadowYamlUpdatesAfterWrite
//   FS-ConfigShadowYamlAtomicWrite
//   FS-ConfigShadowYamlIgnoredOnRead
//   FS-ConfigShadowYamlExcludesNodeLocal
//   FS-ConfigShadowYamlRebuiltOnBoot
//   FS-ConfigShadowYamlRoundTrips
package acceptance

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// FS-ConfigShadowYamlPresentOnDisk
func TestConfigShadowYamlPresentOnDisk(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	leader := c.Leader(t)

	c.MustCreateBlocklist(t, leader, "ads", "tracker.example.com")

	// Wait the debounce window plus a small margin.
	time.Sleep(2 * time.Second)

	data := readShadowYAML(t, leader)
	if len(data) == 0 {
		t.Fatalf("shadow YAML is empty")
	}
	if !strings.Contains(string(data), "tracker.example.com") {
		t.Fatalf("shadow YAML missing blocklist domain:\n%s", string(data))
	}
}

// FS-ConfigShadowYamlUpdatesAfterWrite
func TestConfigShadowYamlUpdatesAfterWrite(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 2)
	leader := c.Leader(t)
	follower := c.Followers(t)[0]

	c.MustCreateBlocklist(t, leader, "ads-update", "ads-update.example.com")

	// Within 5 seconds, BOTH nodes' shadow YAML must reflect the write.
	deadline := time.Now().Add(5 * time.Second)
	leaderOK, followerOK := false, false
	for time.Now().Before(deadline) && (!leaderOK || !followerOK) {
		if !leaderOK && strings.Contains(string(readShadowYAML(t, leader)), "ads-update.example.com") {
			leaderOK = true
		}
		if !followerOK && strings.Contains(string(readShadowYAML(t, follower)), "ads-update.example.com") {
			followerOK = true
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !leaderOK {
		t.Fatalf("leader's shadow YAML did not update within 5s")
	}
	if !followerOK {
		t.Fatalf("follower's shadow YAML did not update within 5s")
	}
}

// FS-ConfigShadowYamlAtomicWrite
//
// Exercise the atomic-rename invariant with TRUE concurrency: a writer
// goroutine fires rapid blocklist creations (no WaitConverged between them)
// while a reader goroutine tight-loops reading and decoding the shadow YAML.
// Any decode failure on the reader side is the torn-write signature.
func TestConfigShadowYamlAtomicWrite(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	leader := c.Leader(t)
	shadowPath := filepath.Join(leader.DataDir, "config.yaml")

	const writes = 30
	var wg sync.WaitGroup
	done := make(chan struct{})

	// Writer: fire-and-forget POSTs straight at the leader, no convergence wait.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(done)
		for i := 0; i < writes; i++ {
			id := fmt.Sprintf("burst-%03d", i)
			body := fmt.Sprintf(
				`{"id":%q,"name":%q,"enabled":true,"domains":[%q],"source":{"type":"","format":"domainlist"}}`,
				id, "Test "+id, id+".example.com",
			)
			resp, err := leader.apiDoNonFatal("POST", "/api/v1/blocklists", body)
			if err == nil {
				resp.Body.Close()
			}
			// Ignore transient errors (e.g. busy); keep firing.
		}
	}()

	// Reader: tight loop, fail immediately on the first decode failure.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			data, err := os.ReadFile(shadowPath)
			if err != nil {
				// File momentarily missing during rename is acceptable on some
				// filesystems; only an actual decode failure signals a tear.
				continue
			}
			var m map[string]any
			if err := yaml.Unmarshal(data, &m); err != nil {
				t.Errorf("shadow YAML decode failure (torn write):\nerr=%v\ncontent=%s", err, string(data))
				return
			}
		}
	}()

	wg.Wait()

	// One more read after the writer has settled — must still parse cleanly.
	time.Sleep(100 * time.Millisecond)
	final, err := os.ReadFile(shadowPath)
	if err != nil {
		t.Fatalf("final read of shadow YAML: %v", err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(final, &m); err != nil {
		t.Fatalf("final shadow YAML did not decode: %v\n%s", err, string(final))
	}
}

// FS-ConfigShadowYamlIgnoredOnRead
func TestConfigShadowYamlIgnoredOnRead(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	leader := c.Leader(t)

	c.MustCreateBlocklist(t, leader, "baseline", "baseline.example.com")
	time.Sleep(2 * time.Second)

	// Tamper with the YAML on disk.
	shadowPath := filepath.Join(leader.DataDir, "config.yaml")
	original, err := os.ReadFile(shadowPath)
	if err != nil {
		t.Fatalf("read shadow YAML: %v", err)
	}
	tampered := string(original) + "\n# manual tampering by test\n"
	if err := os.WriteFile(shadowPath, []byte(tampered), 0600); err != nil {
		t.Fatalf("tamper write: %v", err)
	}

	// API still reports baseline state — manual edits are ignored.
	resp := leader.apiDo(t, "GET", "/api/v1/blocklists", "")
	body := readBody(t, resp)
	if !strings.Contains(body, "baseline") {
		t.Fatalf("running cluster lost the baseline blocklist after YAML tampering: %s", body)
	}

	// A subsequent Raft commit overwrites the tampered content.
	c.MustCreateBlocklist(t, leader, "after-tamper", "after.example.com")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current, _ := os.ReadFile(shadowPath)
		if !strings.Contains(string(current), "manual tampering") &&
			strings.Contains(string(current), "after.example.com") {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("tampered YAML was not overwritten by subsequent commit")
}

// FS-ConfigShadowYamlExcludesNodeLocal
//
// Decode the shadow YAML and assert structurally that operational state
// (Raft membership, hourly stats) and internal secrets never leak into the
// on-disk file. Under the merged single-config.yaml design, the file
// legitimately carries the per-node `node:` section alongside the M1
// cluster sections — both must be present, but `cluster:`/`stats:` and
// any `cluster_secret` field must not.
func TestConfigShadowYamlExcludesNodeLocal(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 2)
	leader := c.Leader(t)
	c.MustCreateBlocklist(t, leader, "ads", "tracker.example.com")
	time.Sleep(2 * time.Second)

	raw := readShadowYAML(t, leader)
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode shadow YAML: %v\n%s", err, string(raw))
	}

	// Allowed top-level keys under the merged single-config.yaml design:
	// per-node `node:`, optional `bootstrap:`, plus the M1 cluster sections.
	allowedTop := map[string]struct{}{
		"schema_version": {},
		"node":           {},
		"bootstrap":      {},
		"dns":            {},
		"filtering":      {},
		"local_dns":      {},
		"query_log":      {},
		"auth":           {},
	}
	// Operational state must never appear in the user-facing YAML: Raft
	// membership lives in the cluster_members bbolt bucket, and hourly
	// aggregates are runtime state, not config.
	forbiddenTop := []string{"cluster", "stats"}
	for _, k := range forbiddenTop {
		if _, ok := doc[k]; ok {
			t.Fatalf("shadow YAML must not contain top-level %q:\n%s", k, string(raw))
		}
	}
	for k := range doc {
		if _, ok := allowedTop[k]; !ok {
			t.Fatalf("shadow YAML has unexpected top-level key %q (not in merged config format):\n%s",
				k, string(raw))
		}
	}

	// The internal shared secret is bbolt-only and must not appear anywhere
	// in the on-disk YAML (any nesting level).
	if found := findForbiddenField(doc, map[string]struct{}{"cluster_secret": {}}); found != "" {
		t.Fatalf("shadow YAML contains forbidden field %q somewhere in the tree:\n%s",
			found, string(raw))
	}

	// Required keys must still be present (M1 schema essentials).
	for _, k := range []string{"filtering", "dns"} {
		if _, ok := doc[k]; !ok {
			t.Fatalf("shadow YAML missing required key %q:\n%s", k, string(raw))
		}
	}

	// The `node:` section must be present with its identity fields. Under
	// the merged design this section is node-local but legitimately lives
	// in config.yaml so the binary can find its identity on restart.
	nodeRaw, ok := doc["node"]
	if !ok {
		t.Fatalf("shadow YAML missing required top-level %q section:\n%s", "node", string(raw))
	}
	nodeMap, ok := nodeRaw.(map[string]any)
	if !ok {
		t.Fatalf("shadow YAML %q section is not a mapping (got %T):\n%s", "node", nodeRaw, string(raw))
	}
	for _, k := range []string{"id", "raft_address"} {
		if _, ok := nodeMap[k]; !ok {
			t.Fatalf("shadow YAML %q section missing required field %q:\n%s", "node", k, string(raw))
		}
	}
}

// findForbiddenField walks an arbitrary YAML-decoded structure looking for any
// map key matching one of the forbidden names. Returns the offending key, or
// "" if none was found.
func findForbiddenField(v any, forbidden map[string]struct{}) string {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if _, bad := forbidden[k]; bad {
				return k
			}
			if hit := findForbiddenField(child, forbidden); hit != "" {
				return hit
			}
		}
	case map[any]any:
		for k, child := range t {
			if ks, ok := k.(string); ok {
				if _, bad := forbidden[ks]; bad {
					return ks
				}
			}
			if hit := findForbiddenField(child, forbidden); hit != "" {
				return hit
			}
		}
	case []any:
		for _, child := range t {
			if hit := findForbiddenField(child, forbidden); hit != "" {
				return hit
			}
		}
	}
	return ""
}

// FS-ConfigShadowYamlRebuiltOnBoot
func TestConfigShadowYamlRebuiltOnBoot(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	leader := c.Leader(t)
	c.MustCreateBlocklist(t, leader, "boot-probe", "boot.example.com")
	time.Sleep(2 * time.Second)

	leaderIdx := indexOf(c, leader)

	// Tamper with the cluster sections of config.yaml while preserving the
	// node section (the binary needs that to know its identity / bind addrs).
	// The shadow writer must rebuild the cluster sections from bbolt after
	// the next FSM event.
	shadowPath := filepath.Join(leader.DataDir, "config.yaml")
	original, err := os.ReadFile(shadowPath)
	if err != nil {
		t.Fatalf("read original yaml: %v", err)
	}
	stale := preserveNodeSection(t, original)
	if err := os.WriteFile(shadowPath, stale, 0600); err != nil {
		t.Fatalf("overwrite YAML: %v", err)
	}

	c.KillNode(t, leaderIdx)
	c.RestartNode(t, leaderIdx)

	// Within 2 seconds of becoming ready, the YAML must match bbolt.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(shadowPath)
		if err == nil && strings.Contains(string(data), "boot.example.com") {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("shadow YAML not rebuilt from bbolt after restart")
}

// FS-ConfigShadowYamlRoundTrips
//
// Build state on a node, capture its YAML, then bootstrap a fresh node with
// the cluster sections from that YAML as seed (simulating a PBS-style
// restore where the operator updates node section for the new host). The
// new node's bbolt must reproduce the captured cluster state.
func TestConfigShadowYamlRoundTrips(t *testing.T) {
	t.Parallel()
	c := startCluster(t, 1)
	src := c.Leader(t)
	c.MustCreateBlocklist(t, src, "round-trip", "round.example.com")
	time.Sleep(2 * time.Second)

	captured, err := os.ReadFile(filepath.Join(src.DataDir, "config.yaml"))
	if err != nil {
		t.Fatalf("read captured YAML: %v", err)
	}

	// Bootstrap a fresh node, then splice the captured cluster sections on
	// top of its (fresh) node section. After the next restart the binary
	// reads the merged file, treats the cluster sections as seed, and
	// imports them via ConfigImport.
	c2 := &Cluster{t: t, bin: skoedBinary(t)}
	dnsPort := freeUDPPort(t)
	apiPort := freeTCPPort(t)
	raftPort := freeTCPPort(t)
	cn := c2.spawnNode(t, M2NodeConfig{
		NodeID:   "restored-1",
		DNSPort:  dnsPort,
		APIPort:  apiPort,
		RaftPort: raftPort,
	})
	c2.nodes = append(c2.nodes, cn)

	freshCfg, err := os.ReadFile(filepath.Join(cn.DataDir, "config.yaml"))
	if err != nil {
		t.Fatalf("read fresh config: %v", err)
	}
	merged := spliceClusterSections(t, freshCfg, captured)

	c2.KillNode(t, 0)
	// Also remove the bbolt so the binary treats the seed as fresh state on
	// restart (otherwise isClusterSectionEmpty check is moot — bbolt already
	// has state from the bootstrap and the seed is ignored).
	if err := os.RemoveAll(filepath.Join(cn.DataDir, "cluster.bbolt")); err != nil {
		t.Fatalf("remove bbolt: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(cn.DataDir, "raft")); err != nil {
		t.Fatalf("remove raft: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cn.DataDir, "config.yaml"), merged, 0600); err != nil {
		t.Fatalf("write merged YAML: %v", err)
	}
	c2.RestartNode(t, 0)
	setupAuth(t, cn.Node)

	waitForBlocklist(t, cn, "round-trip", 10*time.Second)
}

// preserveNodeSection takes a config.yaml that has both node and cluster
// sections and returns a new YAML with the SAME node/bootstrap sections but
// the cluster sections replaced by junk that the shadow writer should
// overwrite on next FSM apply.
func preserveNodeSection(t *testing.T, full []byte) []byte {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal(full, &doc); err != nil {
		t.Fatalf("decode shadow yaml: %v", err)
	}
	out := map[string]any{}
	if v, ok := doc["node"]; ok {
		out["node"] = v
	}
	if v, ok := doc["bootstrap"]; ok {
		out["bootstrap"] = v
	}
	// Intentional stale marker; not a valid cluster section.
	out["_stale_marker"] = "this should be overwritten by the shadow writer"
	b, err := yaml.Marshal(out)
	if err != nil {
		t.Fatalf("encode stale yaml: %v", err)
	}
	return b
}

// spliceClusterSections returns a config.yaml whose top-level node and
// bootstrap sections come from base and whose cluster sections (dns,
// filtering, local_dns, query_log, auth) come from seed.
func spliceClusterSections(t *testing.T, base, seed []byte) []byte {
	t.Helper()
	var baseDoc, seedDoc map[string]any
	if err := yaml.Unmarshal(base, &baseDoc); err != nil {
		t.Fatalf("decode base yaml: %v", err)
	}
	if err := yaml.Unmarshal(seed, &seedDoc); err != nil {
		t.Fatalf("decode seed yaml: %v", err)
	}
	out := map[string]any{}
	for _, k := range []string{"node", "bootstrap"} {
		if v, ok := baseDoc[k]; ok {
			out[k] = v
		}
	}
	for _, k := range []string{"schema_version", "dns", "filtering", "local_dns", "query_log", "auth"} {
		if v, ok := seedDoc[k]; ok {
			out[k] = v
		}
	}
	b, err := yaml.Marshal(out)
	if err != nil {
		t.Fatalf("encode spliced yaml: %v", err)
	}
	return b
}


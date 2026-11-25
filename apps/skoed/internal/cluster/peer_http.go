package cluster

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Peer HTTP over the cluster mTLS (TS-EncryptedClusterMesh, "Internal API").
//
// With `node.cluster.mtls.enabled: true`, every node serves the management
// API router a second time on its peer API listener, which requires a client
// certificate signed by the cluster CA. Peer-to-peer HTTP (forwarded writes,
// aggregates, query-log fan-out, rolling upgrade, blocklist content copy)
// goes through one shared client that presents this node's leaf and verifies
// the target's leaf against the cluster CA, with the target's node ID as
// ServerName. With mTLS disabled, the same client sends plain HTTP.

// CapabilityBlocklistContentV1 is advertised by nodes that hold URL
// blocklist content in their node-local cache instead of the replicated
// state (TS-BlocklistContentDistribution §3.3).
const CapabilityBlocklistContentV1 = "blocklist-content-v1"

// localCapabilities returns the capabilities this version advertises in its
// own member record.
func localCapabilities() []string {
	return []string{CapabilityBlocklistContentV1}
}

const (
	peerDialTimeout         = 5 * time.Second
	peerTLSHandshakeTimeout = 5 * time.Second
	// peerClientTimeout bounds a whole request made with PeerHTTPClient.
	// Callers that need a shorter deadline set one on the request context.
	peerClientTimeout = 60 * time.Second
	// selfMemberForwardTimeout bounds one follower → leader member update.
	selfMemberForwardTimeout = 5 * time.Second
)

// selfMemberPath is the leader endpoint a follower calls to update its own
// member record (peer API address and capabilities).
const selfMemberPath = "/api/v1/cluster/_internal/members/self"

// ErrUnknownMember is returned when a member update targets a node that has
// no member record (not enrolled yet, or removed).
var ErrUnknownMember = errors.New("unknown cluster member")

// peerState holds the peer HTTP material of a Cluster.
type peerState struct {
	// certCache holds the CA and this node's leaf; nil when mTLS is off.
	certCache *CertCache

	clientOnce   sync.Once
	client       *http.Client
	streamClient *http.Client

	stopOnce sync.Once
	stop     chan struct{}
}

// initPeer sets up the peer HTTP state. Called once from New, after the mTLS
// material is loaded.
func (c *Cluster) initPeer() error {
	c.peer.stop = make(chan struct{})
	if !c.mtlsEnabled {
		return nil
	}
	cc, err := NewCertCache(c.mtlsCA, c.mtlsLeafCert, c.mtlsLeafKey)
	if err != nil {
		return fmt.Errorf("peer cert cache: %w", err)
	}
	c.peer.certCache = cc
	return nil
}

// stopPeer stops the self member sync loop. Safe to call more than once.
func (c *Cluster) stopPeer() {
	c.peer.stopOnce.Do(func() {
		if c.peer.stop != nil {
			close(c.peer.stop)
		}
	})
}

// PeerAPIAddress returns the address of this node's peer API listener, or ""
// when mTLS is disabled (no peer listener).
func (c *Cluster) PeerAPIAddress() string {
	if !c.mtlsEnabled {
		return ""
	}
	return c.node.Node.PeerAPIAddress()
}

// PeerServerTLSConfig returns the TLS config of the peer API listener: this
// node's leaf from the cert cache, and a client certificate signed by the
// cluster CA required from every client.
func (c *Cluster) PeerServerTLSConfig() (*tls.Config, error) {
	if !c.mtlsEnabled || c.peer.certCache == nil {
		return nil, errors.New("mTLS is not enabled on this node")
	}
	cfg, err := BuildClusterTLSConfigDynamic(c.peer.certCache, c.mtlsCA)
	if err != nil {
		return nil, err
	}
	cfg.ClientAuth = tls.RequireAndVerifyClientCert
	cfg.NextProtos = []string{"http/1.1"}
	return cfg, nil
}

// PeerHTTPClient returns the shared client for node-to-node HTTP. It sends
// https:// requests over the cluster mTLS (see the file comment) and
// http:// requests as plain HTTP. A whole request is bounded by
// peerClientTimeout; set a shorter deadline on the request context if needed.
func (c *Cluster) PeerHTTPClient() *http.Client {
	c.initPeerClients()
	return c.peer.client
}

// PeerStreamHTTPClient is PeerHTTPClient without the overall request timeout,
// for long-lived responses such as SSE streams. It shares the same transport.
func (c *Cluster) PeerStreamHTTPClient() *http.Client {
	c.initPeerClients()
	return c.peer.streamClient
}

func (c *Cluster) initPeerClients() {
	c.peer.clientOnce.Do(func() {
		rt := &peerRoundTripper{c: c, plain: newPeerTransport(nil), byNode: map[string]*peerNodeTransport{}}
		c.peer.client = &http.Client{Transport: rt, Timeout: peerClientTimeout}
		c.peer.streamClient = &http.Client{Transport: rt}
	})
}

func newPeerTransport(tlsCfg *tls.Config) *http.Transport {
	return &http.Transport{
		DialContext:           (&net.Dialer{Timeout: peerDialTimeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig:       tlsCfg,
		TLSHandshakeTimeout:   peerTLSHandshakeTimeout,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

// peerNodeTransport is the transport for one target node, built for the CA
// it was created with.
type peerNodeTransport struct {
	caPEM     []byte
	transport *http.Transport
}

// peerRoundTripper sends http:// requests on a plain transport and https://
// requests on a transport per target node, whose TLS config verifies the
// server as that node ID.
type peerRoundTripper struct {
	c     *Cluster
	plain *http.Transport

	mu     sync.Mutex
	byNode map[string]*peerNodeTransport
}

func (t *peerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" {
		return t.plain.RoundTrip(req)
	}
	fail := func(err error) (*http.Response, error) {
		if req.Body != nil {
			req.Body.Close()
		}
		return nil, err
	}
	cc := t.c.peer.certCache
	if cc == nil {
		return fail(fmt.Errorf("peer request to %s: mTLS is not enabled on this node", req.URL.Host))
	}
	nodeID := t.c.peerNodeIDForHost(req.URL.Host)
	if nodeID == "" {
		return fail(fmt.Errorf("peer request to %s: no cluster member has this peer API address", req.URL.Host))
	}
	tr, err := t.transportFor(nodeID, cc)
	if err != nil {
		return fail(err)
	}
	return tr.RoundTrip(req)
}

// transportFor returns the transport for nodeID, rebuilt when the cluster CA
// in the cert cache changed since it was created.
func (t *peerRoundTripper) transportFor(nodeID string, cc *CertCache) (*http.Transport, error) {
	caPEM := cc.CACert()
	t.mu.Lock()
	defer t.mu.Unlock()
	if nt, ok := t.byNode[nodeID]; ok && bytes.Equal(nt.caPEM, caPEM) {
		return nt.transport, nil
	} else if ok {
		nt.transport.CloseIdleConnections()
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("peer TLS: cluster CA holds no PEM certificate")
	}
	tlsCfg := &tls.Config{
		RootCAs:    pool,
		ServerName: nodeID,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return cc.GetCertificate(nil)
		},
		MinVersion: tls.VersionTLS12,
	}
	nt := &peerNodeTransport{caPEM: caPEM, transport: newPeerTransport(tlsCfg)}
	t.byNode[nodeID] = nt
	return nt.transport, nil
}

// peerNodeIDForHost returns the ID of the member whose peer API URL has the
// given host:port, or "" when none does.
func (c *Cluster) peerNodeIDForHost(hostPort string) string {
	members, err := c.store.Members()
	if err != nil {
		return ""
	}
	for _, m := range members {
		if m.PeerAPIAddress == "" {
			continue
		}
		if strings.TrimPrefix(c.MemberAPIURL(m), "https://") == hostPort {
			return m.NodeID
		}
	}
	return ""
}

// memberURL returns the base URL peers use to reach m: its peer API listener
// over HTTPS when mTLS is enabled and m advertises one, its main API over
// plain HTTP otherwise (mTLS off, or a member running a previous version).
// fallbackHost replaces a wildcard bind host.
func (c *Cluster) memberURL(m Member, fallbackHost string) string {
	if c.mtlsEnabled && m.PeerAPIAddress != "" {
		return baseURL("https", m.PeerAPIAddress, fallbackHost)
	}
	return baseURL("http", m.APIAddress, fallbackHost)
}

// AllMembersHaveCapability reports whether every server in the Raft
// configuration has a member record that lists capability. A server without
// a member record yet counts as lacking it.
func (c *Cluster) AllMembersHaveCapability(capability string) bool {
	servers := c.raft.Configuration().Servers
	if len(servers) == 0 {
		return false
	}
	for _, s := range servers {
		m, err := c.store.MemberByID(string(s.ID))
		if err != nil || m == nil || !containsCapability(m.Capabilities, capability) {
			return false
		}
	}
	return true
}

func containsCapability(list []string, capability string) bool {
	for _, v := range list {
		if v == capability {
			return true
		}
	}
	return false
}

// SelfMemberUpdate is what a node sets in its own member record.
type SelfMemberUpdate struct {
	NodeID         string   `json:"node_id"`
	PeerAPIAddress string   `json:"peer_api_address,omitempty"`
	Capabilities   []string `json:"capabilities"`
}

// LocalMemberUpdate returns what the node configured in node sets in its own
// member record. A joining node sends it with its join request, so the
// leader writes the complete record at enrollment.
func LocalMemberUpdate(node *NodeYAML) SelfMemberUpdate {
	u := SelfMemberUpdate{NodeID: node.Node.ID, Capabilities: localCapabilities()}
	if node.Node.Cluster.MTLS.Enabled {
		u.PeerAPIAddress = node.Node.PeerAPIAddress()
	}
	return u
}

func (c *Cluster) selfMemberUpdate() SelfMemberUpdate {
	u := LocalMemberUpdate(c.node)
	u.PeerAPIAddress = c.PeerAPIAddress()
	return u
}

// matches reports whether m already holds the values of u.
func (u SelfMemberUpdate) matches(m Member) bool {
	if m.PeerAPIAddress != u.PeerAPIAddress || len(m.Capabilities) != len(u.Capabilities) {
		return false
	}
	for i := range u.Capabilities {
		if m.Capabilities[i] != u.Capabilities[i] {
			return false
		}
	}
	return true
}

// applySelfMemberUpdate commits u on top of the existing record m, keeping
// its addresses and join time. Leader only.
func (c *Cluster) applySelfMemberUpdate(m Member, u SelfMemberUpdate) error {
	return c.raft.ApplyCommand(CmdMemberUpsert, MemberUpsertPayload{
		NodeID:         m.NodeID,
		RaftAddress:    m.RaftAddress,
		APIAddress:     m.APIAddress,
		JoinedUnix:     m.JoinedUnix,
		PeerAPIAddress: u.PeerAPIAddress,
		Capabilities:   u.Capabilities,
	}, ApplyTimeout)
}

// ApplyForwardedSelfMemberUpdate is called on the leader by the internal
// endpoint, after it checked the cluster secret. It refuses a node without a
// member record so a removed node cannot add itself back.
func (c *Cluster) ApplyForwardedSelfMemberUpdate(u SelfMemberUpdate) error {
	if err := c.requireLeader(); err != nil {
		return err
	}
	m, err := c.store.MemberByID(u.NodeID)
	if err != nil {
		return err
	}
	if m == nil {
		return ErrUnknownMember
	}
	if u.matches(*m) {
		return nil
	}
	return c.applySelfMemberUpdate(*m, u)
}

// syncSelfMember brings this node's member record up to date with its peer
// API address and capabilities. The leader applies the change itself; a
// follower sends it to the leader. A node without a member record (not
// enrolled yet, or removed) does nothing.
func (c *Cluster) syncSelfMember() error {
	m, err := c.store.MemberByID(c.node.Node.ID)
	if err != nil || m == nil {
		return err
	}
	u := c.selfMemberUpdate()
	if u.matches(*m) {
		return nil
	}
	if c.raft.IsLeader() {
		return c.applySelfMemberUpdate(*m, u)
	}
	return c.forwardSelfMemberUpdate(u)
}

func (c *Cluster) forwardSelfMemberUpdate(u SelfMemberUpdate) error {
	leaderURL := c.LeaderAPIAddress()
	if leaderURL == "" {
		return errors.New("no leader currently elected")
	}
	secret, err := c.store.ClusterSecret()
	if err != nil || secret == "" {
		return errors.New("cluster secret not available yet")
	}
	body, err := json.Marshal(u)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), selfMemberForwardTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, leaderURL+selfMemberPath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Cluster-Secret", secret)
	resp, err := c.PeerHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("send member update to leader: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("leader rejected member update (status %d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// selfMemberSyncLoop runs syncSelfMember on every node until Close, so the
// record is set after a join, after an upgrade from a previous version, and
// once a leader that knows the endpoint is elected.
func (c *Cluster) selfMemberSyncLoop() {
	interval := 10 * time.Second
	if os.Getenv("SKOED_TEST_MODE") == "1" {
		interval = time.Second
	}
	lastErr := ""
	for {
		err := c.syncSelfMember()
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		if msg != lastErr {
			if msg != "" {
				log.Printf("cluster: update own member record: %s", msg)
			} else if lastErr != "" {
				log.Printf("cluster: own member record updated")
			}
			lastErr = msg
		}
		select {
		case <-c.peer.stop:
			return
		case <-time.After(interval):
		}
	}
}

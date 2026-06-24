// Package dhcp — M30 built-in DHCPv6 server.
//
// Server6 is leader-owned: only the Raft leader binds UDP port 547.
// On leader demotion the server stops; on election the new leader calls
// Start() and inherits the full lease table from bbolt via LoadLeases.
//
// Wire format: RFC 8415 DHCPv6 using UDP sockets on [::]:547.
// This implementation covers the SARR flow (Solicit→Advertise→Request→Reply),
// Renew→Reply, Release→Reply, and Confirm→Reply as required by M30.
package dhcp

import (
	"encoding/binary"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/skoed/skoed/internal/config"
)

// DHCPv6 message types (RFC 8415 §7.3).
const (
	msg6Solicit   = 1
	msg6Advertise = 2
	msg6Request   = 3
	msg6Confirm   = 4
	msg6Renew     = 5
	msg6Reply     = 7
	msg6Release   = 8
)

// DHCPv6 option codes (RFC 8415 §21).
const (
	optClientID    = 1
	optServerID    = 2
	optIANA        = 3
	optIANAAddr    = 5
	optStatusCode  = 13
	optDNSServers  = 23
	optDomainList  = 24
)

// DHCPv6 status codes (RFC 8415 §21.13).
const (
	statusSuccess      = 0
	statusNoAddrsAvail = 2
)

// Server6 binds UDP port 547 and handles DHCPv6 exchanges.
type Server6 struct {
	mu      sync.Mutex
	conn    net.PacketConn
	running bool
	stopCh  chan struct{}
	doneCh  chan struct{}

	cfg   config.DHCPv6ServerConfig
	cfgMu sync.RWMutex

	// leases is the in-memory active lease table, keyed by IPv6 string.
	leases   map[string]*lease6
	leasesMu sync.Mutex

	// serverDUID is this server's DUID-LLT (generated once at construction).
	serverDUID []byte

	// dnsAddr is this node's DNS listen address, used as the default DNS server option.
	dnsAddr string
}

// lease6 is the in-memory form of one active DHCPv6 lease.
type lease6 struct {
	Address   net.IP
	DUID      string
	Hostname  string
	ProfileID string
	ExpiresAt time.Time
	IsStatic  bool
}

// Lease6 is the external view of one active DHCPv6 lease.
type Lease6 struct {
	Address   string    `json:"address"`
	DUID      string    `json:"duid"`
	Hostname  string    `json:"hostname"`
	ProfileID string    `json:"profile_id,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
	Origin    string    `json:"origin"` // "dhcp6_dynamic" | "dhcp6_static"
}

// NewServer6 creates a Server6 wired to the given initial config.
func NewServer6(cfg config.DHCPv6ServerConfig, dnsAddr string) *Server6 {
	s := &Server6{
		cfg:     cfg,
		dnsAddr: dnsAddr,
		leases:  make(map[string]*lease6),
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
	s.serverDUID = generateServerDUID()
	return s
}

// generateServerDUID builds a DUID-LLT (type 1) with a link-layer address
// derived from the first non-loopback MAC found on the host.
func generateServerDUID() []byte {
	// DUID-LLT: type(2) + hw_type(2) + time(4) + link_layer(variable)
	duid := make([]byte, 8)
	binary.BigEndian.PutUint16(duid[0:2], 1) // DUID-LLT
	binary.BigEndian.PutUint16(duid[2:4], 1) // Ethernet
	// time: seconds since 2000-01-01T00:00:00Z
	t := uint32(time.Now().Unix() - 946684800)
	binary.BigEndian.PutUint32(duid[4:8], t)
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || len(iface.HardwareAddr) == 0 {
			continue
		}
		duid = append(duid, iface.HardwareAddr...)
		break
	}
	if len(duid) == 8 {
		duid = append(duid, 0, 0, 0, 0, 0, 0)
	}
	return duid
}

// UpdateConfig replaces the active DHCPv6 configuration.
func (s *Server6) UpdateConfig(cfg config.DHCPv6ServerConfig) {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	s.cfg = cfg

	s.leasesMu.Lock()
	for k, l := range s.leases {
		if l.IsStatic {
			delete(s.leases, k)
		}
	}
	for _, a := range cfg.StaticAssignments {
		ip := net.ParseIP(a.Address)
		if ip == nil {
			continue
		}
		s.leases[ip.String()] = &lease6{
			Address:  ip,
			DUID:     a.DUID,
			Hostname: a.Hostname,
			IsStatic: true,
		}
	}
	s.leasesMu.Unlock()
}

// LoadLeases populates the in-memory lease table from persisted entries (called on startup).
func (s *Server6) LoadLeases(leases []Lease6) {
	s.leasesMu.Lock()
	defer s.leasesMu.Unlock()
	now := time.Now()
	for _, l := range leases {
		if l.Origin == "dhcp6_static" {
			continue
		}
		if !l.ExpiresAt.IsZero() && l.ExpiresAt.Before(now) {
			continue
		}
		ip := net.ParseIP(l.Address)
		if ip == nil {
			continue
		}
		s.leases[ip.String()] = &lease6{
			Address:   ip,
			DUID:      l.DUID,
			Hostname:  l.Hostname,
			ProfileID: l.ProfileID,
			ExpiresAt: l.ExpiresAt,
		}
	}
}

// Start binds UDP 547 and begins serving DHCPv6 requests.
func (s *Server6) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return
	}
	conn, err := net.ListenPacket("udp6", "[::]:547")
	if err != nil {
		log.Printf("[dhcp6] server: cannot bind UDP 547: %v (is this node root?)", err)
		return
	}
	s.conn = conn
	s.stopCh = make(chan struct{})
	s.doneCh = make(chan struct{})
	s.running = true
	go s.serve()
	log.Printf("[dhcp6] server started")
}

// Stop closes the listener and waits for the goroutine to exit.
func (s *Server6) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	close(s.stopCh)
	s.conn.Close()
	doneCh := s.doneCh
	s.running = false
	s.mu.Unlock()
	<-doneCh
	log.Printf("[dhcp6] server stopped")
}

// Running reports whether the listener is currently bound.
func (s *Server6) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// ActiveLeases returns a snapshot of the current DHCPv6 lease table.
func (s *Server6) ActiveLeases() []Lease6 {
	s.leasesMu.Lock()
	defer s.leasesMu.Unlock()
	out := make([]Lease6, 0, len(s.leases))
	for _, l := range s.leases {
		origin := "dhcp6_dynamic"
		if l.IsStatic {
			origin = "dhcp6_static"
		}
		out = append(out, Lease6{
			Address:   l.Address.String(),
			DUID:      l.DUID,
			Hostname:  l.Hostname,
			ProfileID: l.ProfileID,
			ExpiresAt: l.ExpiresAt,
			Origin:    origin,
		})
	}
	return out
}

// ─── packet loop ──────────────────────────────────────────────────────────────

func (s *Server6) serve() {
	defer close(s.doneCh)
	buf := make([]byte, 1500)
	for {
		select {
		case <-s.stopCh:
			return
		default:
		}
		s.conn.SetReadDeadline(time.Now().Add(time.Second))
		n, addr, err := s.conn.ReadFrom(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			select {
			case <-s.stopCh:
				return
			default:
				log.Printf("[dhcp6] read error: %v", err)
				continue
			}
		}
		pkt := make([]byte, n)
		copy(pkt, buf[:n])
		go s.handlePacket(pkt, addr)
	}
}

// ─── DHCPv6 packet handling ───────────────────────────────────────────────────

// dhcp6Packet is a decoded DHCPv6 message (RFC 8415 §8).
type dhcp6Packet struct {
	msgType byte
	xid     [3]byte
	opts    map[uint16][]byte
}

func parseDHCP6(b []byte) *dhcp6Packet {
	if len(b) < 4 {
		return nil
	}
	p := &dhcp6Packet{
		msgType: b[0],
		opts:    make(map[uint16][]byte),
	}
	copy(p.xid[:], b[1:4])
	opts := b[4:]
	for i := 0; i+4 <= len(opts); {
		code := binary.BigEndian.Uint16(opts[i:])
		l := int(binary.BigEndian.Uint16(opts[i+2:]))
		i += 4
		if i+l > len(opts) {
			break
		}
		if _, exists := p.opts[code]; !exists {
			p.opts[code] = opts[i : i+l]
		}
		i += l
	}
	return p
}

func buildDHCP6(msgType byte, xid [3]byte, opts []byte) []byte {
	b := make([]byte, 4)
	b[0] = msgType
	copy(b[1:4], xid[:])
	return append(b, opts...)
}

func appendOpt6(b []byte, code uint16, val []byte) []byte {
	hdr := make([]byte, 4)
	binary.BigEndian.PutUint16(hdr[0:2], code)
	binary.BigEndian.PutUint16(hdr[2:4], uint16(len(val)))
	b = append(b, hdr...)
	return append(b, val...)
}

// duidHex converts a byte slice to a colon-separated hex string (DUID representation).
func duidHex(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	const hex = "0123456789abcdef"
	sb := make([]byte, 0, len(b)*3-1)
	for i, v := range b {
		if i > 0 {
			sb = append(sb, ':')
		}
		sb = append(sb, hex[v>>4], hex[v&0xf])
	}
	return string(sb)
}

func (s *Server6) handlePacket(raw []byte, addr net.Addr) {
	pkt := parseDHCP6(raw)
	if pkt == nil {
		return
	}
	s.cfgMu.RLock()
	cfg := s.cfg
	s.cfgMu.RUnlock()

	switch pkt.msgType {
	case msg6Solicit:
		s.handleSolicit(pkt, addr, cfg)
	case msg6Request:
		s.handleRequest6(pkt, addr, cfg)
	case msg6Renew:
		s.handleRenew(pkt, addr, cfg)
	case msg6Confirm:
		s.handleConfirm(pkt, addr)
	case msg6Release:
		s.handleRelease6(pkt, addr)
	}
}

func leaseDuration6(cfg config.DHCPv6ServerConfig) time.Duration {
	if cfg.LeaseTime > 0 {
		return time.Duration(cfg.LeaseTime) * time.Second
	}
	return 24 * time.Hour
}

// allocate6 returns the IPv6 address assigned to the given DUID,
// or allocates a new one from the pool.
func (s *Server6) allocate6(duid string, cfg config.DHCPv6ServerConfig) net.IP {
	s.leasesMu.Lock()
	defer s.leasesMu.Unlock()

	for _, l := range s.leases {
		if l.DUID == duid {
			return l.Address
		}
	}

	start := net.ParseIP(cfg.PoolStart)
	end := net.ParseIP(cfg.PoolEnd)
	if start == nil || end == nil {
		return nil
	}
	start = start.To16()
	end = end.To16()

	used := make(map[string]bool, len(s.leases))
	for _, l := range s.leases {
		used[l.Address.String()] = true
	}

	ip := make(net.IP, 16)
	copy(ip, start)
	for !ip6After(ip, end) {
		if !used[ip.String()] {
			newIP := make(net.IP, 16)
			copy(newIP, ip)
			s.leases[newIP.String()] = &lease6{
				Address:   newIP,
				DUID:      duid,
				ExpiresAt: time.Now().Add(leaseDuration6(cfg)),
			}
			return newIP
		}
		incrementIP6(ip)
	}
	return nil
}

func ip6After(ip, end net.IP) bool {
	for i := 0; i < 16; i++ {
		if ip[i] > end[i] {
			return true
		}
		if ip[i] < end[i] {
			return false
		}
	}
	return false
}

func incrementIP6(ip net.IP) {
	for i := 15; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			break
		}
	}
}

func buildIANA(iaid []byte, addr net.IP, preferredLifetime, validLifetime uint32) []byte {
	iaddr := make([]byte, 24)
	copy(iaddr[0:16], addr.To16())
	binary.BigEndian.PutUint32(iaddr[16:20], preferredLifetime)
	binary.BigEndian.PutUint32(iaddr[20:24], validLifetime)
	iaAddrOpt := appendOpt6(nil, optIANAAddr, iaddr)

	body := make([]byte, 12)
	if len(iaid) >= 4 {
		copy(body[0:4], iaid[:4])
	}
	binary.BigEndian.PutUint32(body[4:8], preferredLifetime/2)
	binary.BigEndian.PutUint32(body[8:12], preferredLifetime*3/4)
	body = append(body, iaAddrOpt...)
	return body
}

func (s *Server6) dnsServersOption() []byte {
	host := "::1"
	if s.dnsAddr != "" {
		h, _, err := net.SplitHostPort(s.dnsAddr)
		if err == nil && h != "" {
			host = h
		}
	}
	ip := net.ParseIP(host).To16()
	if ip == nil {
		ip = net.ParseIP("::1").To16()
	}
	return ip
}

func (s *Server6) handleSolicit(pkt *dhcp6Packet, addr net.Addr, cfg config.DHCPv6ServerConfig) {
	duid := duidHex(pkt.opts[optClientID])
	iaid := pkt.opts[optIANA]
	if len(iaid) < 4 {
		iaid = []byte{0, 0, 0, 0}
	}

	offeredIP := s.allocate6(duid, cfg)
	if offeredIP == nil {
		s.sendNoAddrsAvail(pkt, addr, msg6Advertise)
		return
	}

	lt := uint32(leaseDuration6(cfg).Seconds())
	ianaVal := buildIANA(iaid[:4], offeredIP, lt, lt)

	var opts []byte
	opts = appendOpt6(opts, optClientID, pkt.opts[optClientID])
	opts = appendOpt6(opts, optServerID, s.serverDUID)
	opts = appendOpt6(opts, optIANA, ianaVal)
	opts = appendOpt6(opts, optDNSServers, s.dnsServersOption())
	if cfg.SearchDomain != "" {
		opts = appendOpt6(opts, optDomainList, encodeDomainList(cfg.SearchDomain))
	}
	s.send6(buildDHCP6(msg6Advertise, pkt.xid, opts), addr)
}

func (s *Server6) handleRequest6(pkt *dhcp6Packet, addr net.Addr, cfg config.DHCPv6ServerConfig) {
	duid := duidHex(pkt.opts[optClientID])
	iaid := pkt.opts[optIANA]
	if len(iaid) < 4 {
		iaid = []byte{0, 0, 0, 0}
	}

	assignedIP := s.allocate6(duid, cfg)
	if assignedIP == nil {
		s.sendNoAddrsAvail(pkt, addr, msg6Reply)
		return
	}

	lt := uint32(leaseDuration6(cfg).Seconds())
	ianaVal := buildIANA(iaid[:4], assignedIP, lt, lt)

	var opts []byte
	opts = appendOpt6(opts, optClientID, pkt.opts[optClientID])
	opts = appendOpt6(opts, optServerID, s.serverDUID)
	opts = appendOpt6(opts, optIANA, ianaVal)
	opts = appendOpt6(opts, optDNSServers, s.dnsServersOption())
	if cfg.SearchDomain != "" {
		opts = appendOpt6(opts, optDomainList, encodeDomainList(cfg.SearchDomain))
	}
	s.send6(buildDHCP6(msg6Reply, pkt.xid, opts), addr)
}

func (s *Server6) handleRenew(pkt *dhcp6Packet, addr net.Addr, cfg config.DHCPv6ServerConfig) {
	duid := duidHex(pkt.opts[optClientID])
	iaid := pkt.opts[optIANA]
	if len(iaid) < 4 {
		iaid = []byte{0, 0, 0, 0}
	}

	lt := leaseDuration6(cfg)
	s.leasesMu.Lock()
	for _, l := range s.leases {
		if l.DUID == duid && !l.IsStatic {
			l.ExpiresAt = time.Now().Add(lt)
			break
		}
	}
	s.leasesMu.Unlock()

	assignedIP := s.allocate6(duid, cfg)
	if assignedIP == nil {
		s.sendNoAddrsAvail(pkt, addr, msg6Reply)
		return
	}

	lts := uint32(lt.Seconds())
	ianaVal := buildIANA(iaid[:4], assignedIP, lts, lts)

	var opts []byte
	opts = appendOpt6(opts, optClientID, pkt.opts[optClientID])
	opts = appendOpt6(opts, optServerID, s.serverDUID)
	opts = appendOpt6(opts, optIANA, ianaVal)
	opts = appendOpt6(opts, optDNSServers, s.dnsServersOption())
	s.send6(buildDHCP6(msg6Reply, pkt.xid, opts), addr)
}

func (s *Server6) handleConfirm(pkt *dhcp6Packet, addr net.Addr) {
	statusVal := make([]byte, 2)
	binary.BigEndian.PutUint16(statusVal, statusSuccess)
	var opts []byte
	opts = appendOpt6(opts, optClientID, pkt.opts[optClientID])
	opts = appendOpt6(opts, optServerID, s.serverDUID)
	opts = appendOpt6(opts, optStatusCode, statusVal)
	s.send6(buildDHCP6(msg6Reply, pkt.xid, opts), addr)
}

func (s *Server6) handleRelease6(pkt *dhcp6Packet, addr net.Addr) {
	duid := duidHex(pkt.opts[optClientID])
	s.leasesMu.Lock()
	for k, l := range s.leases {
		if l.DUID == duid && !l.IsStatic {
			delete(s.leases, k)
			break
		}
	}
	s.leasesMu.Unlock()

	statusVal := make([]byte, 2)
	binary.BigEndian.PutUint16(statusVal, statusSuccess)
	var opts []byte
	opts = appendOpt6(opts, optClientID, pkt.opts[optClientID])
	opts = appendOpt6(opts, optServerID, s.serverDUID)
	opts = appendOpt6(opts, optStatusCode, statusVal)
	s.send6(buildDHCP6(msg6Reply, pkt.xid, opts), addr)
}

func (s *Server6) sendNoAddrsAvail(pkt *dhcp6Packet, addr net.Addr, msgType byte) {
	statusVal := make([]byte, 2)
	binary.BigEndian.PutUint16(statusVal, statusNoAddrsAvail)
	ianaBody := make([]byte, 12)
	ianaBody = append(ianaBody, appendOpt6(nil, optStatusCode, statusVal)...)
	var opts []byte
	opts = appendOpt6(opts, optClientID, pkt.opts[optClientID])
	opts = appendOpt6(opts, optServerID, s.serverDUID)
	opts = appendOpt6(opts, optIANA, ianaBody)
	s.send6(buildDHCP6(msgType, pkt.xid, opts), addr)
}

func (s *Server6) send6(reply []byte, addr net.Addr) {
	if _, err := s.conn.WriteTo(reply, addr); err != nil {
		log.Printf("[dhcp6] send: %v", err)
	}
}

// encodeDomainList encodes a single domain in DNS wire format (RFC 3646 §3).
func encodeDomainList(domain string) []byte {
	var buf []byte
	labels := strings.Split(strings.TrimSuffix(domain, "."), ".")
	for _, label := range labels {
		if label == "" {
			continue
		}
		buf = append(buf, byte(len(label)))
		buf = append(buf, []byte(label)...)
	}
	buf = append(buf, 0)
	return buf
}

// poolSize6 computes the approximate number of IPv6 addresses in [start, end].
// It compares only the last 4 bytes, which is sufficient for /96+ pools.
func poolSize6(start, end string) int {
	s := net.ParseIP(start).To16()
	e := net.ParseIP(end).To16()
	if s == nil || e == nil {
		return 0
	}
	si := binary.BigEndian.Uint32(s[12:16])
	ei := binary.BigEndian.Uint32(e[12:16])
	if ei < si {
		return 0
	}
	return int(ei-si) + 1
}

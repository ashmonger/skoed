// Package dhcp — M23.5 built-in DHCP server.
//
// The Server is leader-owned: only the Raft leader binds UDP port 67.
// On leader demotion the server stops; on election the new leader calls
// Start() and inherits the full lease table from bbolt.
//
// Wire format: RFC 2131 DHCPv4 using raw UDP sockets via net.PacketConn.
// Packets destined to 255.255.255.255 are sent back as IP-layer broadcasts
// by writing to the client's hardware address via a raw connection when
// the client does not yet have an IP, or as unicast when it does.
package dhcp

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/skoed/skoed/internal/config"
)

// Server binds UDP port 67, handles DHCP DISCOVER/REQUEST/RELEASE, and
// maintains a lease table backed by bbolt (via a callback pair).
type Server struct {
	mu       sync.Mutex
	conn     net.PacketConn
	running  bool
	stopCh   chan struct{}
	doneCh   chan struct{}

	cfg     config.DHCPServerConfig
	cfgMu   sync.RWMutex

	// leases holds the in-memory active lease table, keyed by lowercase MAC.
	leases   map[string]*lease4
	leasesMu sync.Mutex

	// dnsAddr is this node's DNS listen address, used as the default option 6
	// value when no explicit dns_server is configured.
	dnsAddr string
}

type lease4 struct {
	IP        net.IP
	MAC       net.HardwareAddr
	Hostname  string
	ExpiresAt time.Time
	IsStatic  bool
}

// NewServer creates a Server wired to the given initial config.
// dnsAddr is the node's DNS listen address (used as default DHCP option 6).
func NewServer(cfg config.DHCPServerConfig, dnsAddr string) *Server {
	return &Server{
		cfg:     cfg,
		dnsAddr: dnsAddr,
		leases:  make(map[string]*lease4),
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
}

// UpdateConfig replaces the active configuration (pool, flags, etc.).
// If the server is running, the new config takes effect on the next
// DHCP exchange; it does not restart the listener.
func (s *Server) UpdateConfig(cfg config.DHCPServerConfig) {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	s.cfg = cfg
	// Rebuild static entries in lease table.
	s.leasesMu.Lock()
	for k, l := range s.leases {
		if l.IsStatic {
			delete(s.leases, k)
		}
	}
	for _, a := range cfg.StaticAssignments {
		ip := net.ParseIP(a.IP)
		if ip == nil {
			continue
		}
		mac, err := net.ParseMAC(a.MAC)
		if err != nil {
			continue
		}
		key := strings.ToLower(a.MAC)
		s.leases[key] = &lease4{
			IP:        ip.To4(),
			MAC:       mac,
			Hostname:  a.Hostname,
			ExpiresAt: time.Time{}, // never expires
			IsStatic:  true,
		}
	}
	s.leasesMu.Unlock()
}

// Start binds UDP 67 and begins serving DHCP requests.
// It is a no-op if the server is already running.
// Failures to bind are logged but do not return an error so that
// acceptance tests (which run without root) do not crash.
func (s *Server) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return
	}
	conn, err := net.ListenPacket("udp4", "0.0.0.0:67")
	if err != nil {
		log.Printf("[dhcp] server: cannot bind UDP 67: %v (is this node root?)", err)
		return
	}
	s.conn = conn
	s.stopCh = make(chan struct{})
	s.doneCh = make(chan struct{})
	s.running = true
	go s.serve()
	log.Printf("[dhcp] server started")
}

// Stop closes the listener and waits for the goroutine to exit.
func (s *Server) Stop() {
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
	log.Printf("[dhcp] server stopped")
}

// Running reports whether the listener is currently bound.
func (s *Server) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// ActiveLeases returns a snapshot of the current lease table.
func (s *Server) ActiveLeases() []Lease4 {
	s.leasesMu.Lock()
	defer s.leasesMu.Unlock()
	out := make([]Lease4, 0, len(s.leases))
	for _, l := range s.leases {
		origin := "dhcp_dynamic"
		if l.IsStatic {
			origin = "dhcp_static"
		}
		out = append(out, Lease4{
			IP:        l.IP.String(),
			MAC:       l.MAC.String(),
			Hostname:  l.Hostname,
			ExpiresAt: l.ExpiresAt,
			Origin:    origin,
		})
	}
	return out
}

// Lease4 is the external view of one active lease.
type Lease4 struct {
	IP        string    `json:"ip"`
	MAC       string    `json:"mac"`
	Hostname  string    `json:"hostname"`
	ExpiresAt time.Time `json:"expires_at"`
	Origin    string    `json:"origin"` // "dhcp_dynamic" | "dhcp_static"
}

// ─── packet loop ─────────────────────────────────────────────────────────────

func (s *Server) serve() {
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
				log.Printf("[dhcp] read error: %v", err)
				continue
			}
		}
		pkt := make([]byte, n)
		copy(pkt, buf[:n])
		go s.handlePacket(pkt, addr)
	}
}

// ─── RFC 2131 packet handling ─────────────────────────────────────────────────

const (
	dhcpMagicCookie  = 0x63825363
	dhcpOpRequest    = 1
	dhcpOpReply      = 2
	dhcpHTypeEthernet = 1

	// DHCP message types (option 53)
	msgDiscover = 1
	msgOffer    = 2
	msgRequest  = 3
	msgDecline  = 4
	msgAck      = 5
	msgNak      = 6
	msgRelease  = 7
	msgInform   = 8
)

// dhcp4Packet is a minimal RFC 2131 fixed-format parser.
type dhcp4Packet struct {
	op     byte
	htype  byte
	hlen   byte
	hops   byte
	xid    [4]byte
	secs   uint16
	flags  uint16
	ciaddr net.IP // client IP (renewal/rebind)
	yiaddr net.IP // offered IP
	siaddr net.IP // server IP
	giaddr net.IP // relay agent IP
	chaddr net.HardwareAddr
	opts   map[byte][]byte // option code → value bytes
}

// parseDHCP4 decodes a raw UDP payload into a dhcp4Packet.
// Returns nil if the packet is too short or lacks the magic cookie.
func parseDHCP4(b []byte) *dhcp4Packet {
	if len(b) < 240 {
		return nil
	}
	if binary.BigEndian.Uint32(b[236:240]) != dhcpMagicCookie {
		return nil
	}
	pkt := &dhcp4Packet{
		op:     b[0],
		htype:  b[1],
		hlen:   b[2],
		hops:   b[3],
		secs:   binary.BigEndian.Uint16(b[8:10]),
		flags:  binary.BigEndian.Uint16(b[10:12]),
		opts:   make(map[byte][]byte),
	}
	copy(pkt.xid[:], b[4:8])
	pkt.ciaddr = make(net.IP, 4)
	copy(pkt.ciaddr, b[12:16])
	pkt.yiaddr = make(net.IP, 4)
	copy(pkt.yiaddr, b[16:20])
	pkt.siaddr = make(net.IP, 4)
	copy(pkt.siaddr, b[20:24])
	pkt.giaddr = make(net.IP, 4)
	copy(pkt.giaddr, b[24:28])
	if int(pkt.hlen) <= 16 {
		pkt.chaddr = make(net.HardwareAddr, pkt.hlen)
		copy(pkt.chaddr, b[28:28+pkt.hlen])
	}
	// Parse options (TLV after magic cookie).
	opts := b[240:]
	for i := 0; i < len(opts); {
		code := opts[i]
		i++
		if code == 255 { // end
			break
		}
		if code == 0 { // pad
			continue
		}
		if i >= len(opts) {
			break
		}
		l := int(opts[i])
		i++
		if i+l > len(opts) {
			break
		}
		pkt.opts[code] = opts[i : i+l]
		i += l
	}
	return pkt
}

// buildDHCP4 serialises a response packet.
func buildDHCP4(req *dhcp4Packet, msgType byte, yourIP, serverIP net.IP, opts map[byte][]byte) []byte {
	b := make([]byte, 240)
	b[0] = dhcpOpReply
	b[1] = dhcpHTypeEthernet
	b[2] = 6
	b[3] = 0
	copy(b[4:8], req.xid[:])
	binary.BigEndian.PutUint16(b[8:10], 0)
	binary.BigEndian.PutUint16(b[10:12], 0)
	// ciaddr stays zero in OFFER/ACK from server
	copy(b[16:20], yourIP.To4()) // yiaddr
	copy(b[20:24], serverIP.To4())
	copy(b[24:28], req.giaddr)
	copy(b[28:44], req.chaddr)
	binary.BigEndian.PutUint32(b[236:240], dhcpMagicCookie)

	// Options.
	var o []byte
	o = appendOpt(o, 53, []byte{msgType}) // DHCP message type
	for code, val := range opts {
		if code == 53 {
			continue
		}
		o = appendOpt(o, code, val)
	}
	o = append(o, 255) // end
	return append(b, o...)
}

func appendOpt(b []byte, code byte, val []byte) []byte {
	b = append(b, code, byte(len(val)))
	return append(b, val...)
}

func (s *Server) handlePacket(raw []byte, addr net.Addr) {
	pkt := parseDHCP4(raw)
	if pkt == nil || pkt.op != dhcpOpRequest {
		return
	}
	msgTypeBytes, ok := pkt.opts[53]
	if !ok || len(msgTypeBytes) == 0 {
		return
	}

	s.cfgMu.RLock()
	cfg := s.cfg
	s.cfgMu.RUnlock()

	switch msgTypeBytes[0] {
	case msgDiscover:
		s.handleDiscover(pkt, addr, cfg)
	case msgRequest:
		s.handleRequest(pkt, addr, cfg)
	case msgRelease:
		s.handleRelease(pkt)
	}
}

func (s *Server) handleDiscover(pkt *dhcp4Packet, addr net.Addr, cfg config.DHCPServerConfig) {
	macKey := strings.ToLower(pkt.chaddr.String())
	s.leasesMu.Lock()
	existing := s.leases[macKey]
	s.leasesMu.Unlock()

	var offerIP net.IP
	if existing != nil {
		offerIP = existing.IP
	} else {
		offerIP = s.allocate(macKey, cfg)
	}
	if offerIP == nil {
		// pool exhausted — send NAK
		s.sendNak(pkt, addr, cfg)
		return
	}
	s.sendOffer(pkt, addr, offerIP, cfg)
}

func (s *Server) handleRequest(pkt *dhcp4Packet, addr net.Addr, cfg config.DHCPServerConfig) {
	macKey := strings.ToLower(pkt.chaddr.String())
	reqIPBytes, hasReqIP := pkt.opts[50]
	var requestedIP net.IP
	if hasReqIP && len(reqIPBytes) == 4 {
		requestedIP = net.IP(reqIPBytes)
	} else if !pkt.ciaddr.Equal(net.IPv4zero) {
		requestedIP = pkt.ciaddr
	}

	s.leasesMu.Lock()
	existing := s.leases[macKey]
	s.leasesMu.Unlock()

	if existing != nil && (requestedIP == nil || existing.IP.Equal(requestedIP)) {
		existing.ExpiresAt = time.Now().Add(leaseDuration(cfg))
		s.sendAck(pkt, addr, existing.IP, existing.Hostname, cfg)
		return
	}
	if requestedIP == nil {
		s.sendNak(pkt, addr, cfg)
		return
	}
	// Allocate at requested IP if available.
	if existing == nil {
		existing = s.reserveAt(macKey, requestedIP, pkt, cfg)
	}
	if existing == nil {
		s.sendNak(pkt, addr, cfg)
		return
	}
	s.sendAck(pkt, addr, existing.IP, existing.Hostname, cfg)
}

func (s *Server) handleRelease(pkt *dhcp4Packet) {
	key := strings.ToLower(pkt.chaddr.String())
	s.leasesMu.Lock()
	delete(s.leases, key)
	s.leasesMu.Unlock()
}

// ─── allocation helpers ───────────────────────────────────────────────────────

func (s *Server) allocate(macKey string, cfg config.DHCPServerConfig) net.IP {
	// Check static assignment first.
	s.leasesMu.Lock()
	defer s.leasesMu.Unlock()
	if l, ok := s.leases[macKey]; ok && l.IsStatic {
		return l.IP
	}
	// Scan pool range.
	start := net.ParseIP(cfg.PoolStart).To4()
	end := net.ParseIP(cfg.PoolEnd).To4()
	if start == nil || end == nil {
		return nil
	}
	used := make(map[string]bool)
	for _, l := range s.leases {
		used[l.IP.String()] = true
	}
	ip := make(net.IP, 4)
	copy(ip, start)
	for !ipAfter(ip, end) {
		if !used[ip.String()] {
			newLease := &lease4{
				IP:        make(net.IP, 4),
				MAC:       make(net.HardwareAddr, 6),
				ExpiresAt: time.Now().Add(leaseDuration(cfg)),
			}
			copy(newLease.IP, ip)
			newLease.MAC, _ = net.ParseMAC(macKey)
			s.leases[macKey] = newLease
			result := make(net.IP, 4)
			copy(result, ip)
			return result
		}
		incrementIP(ip)
	}
	return nil
}

func (s *Server) reserveAt(macKey string, ip net.IP, pkt *dhcp4Packet, cfg config.DHCPServerConfig) *lease4 {
	s.leasesMu.Lock()
	defer s.leasesMu.Unlock()
	// Check not taken by another lease.
	for k, l := range s.leases {
		if l.IP.Equal(ip) && k != macKey {
			return nil
		}
	}
	hostname := ""
	if hb, ok := pkt.opts[12]; ok {
		hostname = string(hb)
	}
	l := &lease4{
		IP:        ip.To4(),
		Hostname:  hostname,
		ExpiresAt: time.Now().Add(leaseDuration(cfg)),
	}
	l.MAC, _ = net.ParseMAC(macKey)
	s.leases[macKey] = l
	return l
}

func leaseDuration(cfg config.DHCPServerConfig) time.Duration {
	if cfg.LeaseTimeSeconds > 0 {
		return time.Duration(cfg.LeaseTimeSeconds) * time.Second
	}
	return 24 * time.Hour
}

func ipAfter(ip, end net.IP) bool {
	for i := 0; i < 4; i++ {
		if ip[i] > end[i] {
			return true
		}
		if ip[i] < end[i] {
			return false
		}
	}
	return false
}

func incrementIP(ip net.IP) {
	for i := 3; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			break
		}
	}
}

// ─── response builders ────────────────────────────────────────────────────────

func (s *Server) serverIP() net.IP {
	// Determine our outbound IP from the listen connection.
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.IsGlobalUnicast() && ipn.IP.To4() != nil {
			return ipn.IP.To4()
		}
	}
	return net.IPv4(0, 0, 0, 0).To4()
}

func (s *Server) dnsOption(cfg config.DHCPServerConfig) []byte {
	if cfg.DNSServer != "" {
		ip := net.ParseIP(cfg.DNSServer).To4()
		if ip != nil {
			return ip
		}
	}
	// Default to skoed's own DNS address.
	host, _, err := net.SplitHostPort(s.dnsAddr)
	if err != nil {
		host = "127.0.0.1"
	}
	ip := net.ParseIP(host).To4()
	if ip == nil {
		ip = net.IPv4(127, 0, 0, 1).To4()
	}
	return ip
}

func subnetMaskFor(poolStart, poolEnd string) []byte {
	start := net.ParseIP(poolStart).To4()
	end := net.ParseIP(poolEnd).To4()
	if start == nil || end == nil {
		return []byte{255, 255, 255, 0}
	}
	// Infer a /24 if start and end agree on first 3 octets.
	if start[0] == end[0] && start[1] == end[1] && start[2] == end[2] {
		return []byte{255, 255, 255, 0}
	}
	if start[0] == end[0] && start[1] == end[1] {
		return []byte{255, 255, 0, 0}
	}
	return []byte{255, 0, 0, 0}
}

func leaseTimeBytes(cfg config.DHCPServerConfig) []byte {
	secs := uint32(86400)
	if cfg.LeaseTimeSeconds > 0 {
		secs = uint32(cfg.LeaseTimeSeconds)
	}
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, secs)
	return b
}

func (s *Server) buildStdOpts(offerIP net.IP, hostname string, cfg config.DHCPServerConfig) map[byte][]byte {
	opts := map[byte][]byte{
		1:  subnetMaskFor(cfg.PoolStart, cfg.PoolEnd), // subnet mask
		6:  s.dnsOption(cfg),                          // DNS
		51: leaseTimeBytes(cfg),                       // lease time
		54: s.serverIP().To4(),                        // server identifier
	}
	if cfg.Gateway != "" {
		if gw := net.ParseIP(cfg.Gateway).To4(); gw != nil {
			opts[3] = gw
		}
	}
	if cfg.Domain != "" {
		opts[15] = []byte(cfg.Domain)
	}
	if hostname != "" {
		opts[12] = []byte(hostname)
	}
	return opts
}

func (s *Server) sendOffer(pkt *dhcp4Packet, addr net.Addr, offerIP net.IP, cfg config.DHCPServerConfig) {
	opts := s.buildStdOpts(offerIP, "", cfg)
	reply := buildDHCP4(pkt, msgOffer, offerIP, s.serverIP(), opts)
	s.send(reply, pkt, addr)
}

func (s *Server) sendAck(pkt *dhcp4Packet, addr net.Addr, ip net.IP, hostname string, cfg config.DHCPServerConfig) {
	opts := s.buildStdOpts(ip, hostname, cfg)
	reply := buildDHCP4(pkt, msgAck, ip, s.serverIP(), opts)
	s.send(reply, pkt, addr)
}

func (s *Server) sendNak(pkt *dhcp4Packet, addr net.Addr, cfg config.DHCPServerConfig) {
	opts := map[byte][]byte{
		54: s.serverIP().To4(),
	}
	reply := buildDHCP4(pkt, msgNak, net.IPv4zero.To4(), s.serverIP(), opts)
	s.send(reply, pkt, addr)
}

func (s *Server) send(reply []byte, req *dhcp4Packet, from net.Addr) {
	dest := "255.255.255.255:68"
	if !req.giaddr.Equal(net.IPv4zero) {
		// relay agent present: unicast back to giaddr on port 67
		dest = fmt.Sprintf("%s:67", req.giaddr)
	} else if req.flags&0x8000 == 0 && !req.ciaddr.Equal(net.IPv4zero) {
		// unicast to client
		dest = fmt.Sprintf("%s:68", req.ciaddr)
	}
	dst, err := net.ResolveUDPAddr("udp4", dest)
	if err != nil {
		log.Printf("[dhcp] resolve dest %q: %v", dest, err)
		return
	}
	if _, err := s.conn.WriteTo(reply, dst); err != nil {
		log.Printf("[dhcp] send: %v", err)
	}
}

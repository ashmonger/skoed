// Package netguard provides SSRF-hardened HTTP clients. Outbound fetches to
// operator-supplied URLs (blocklists, webhooks) run from a privileged network
// position, so they must not be usable to reach internal, loopback, or
// cloud-metadata endpoints.
//
// The guard is applied at DIAL time against the resolved IP, so it also defeats
// DNS-rebinding (a hostname that resolves public on the validation lookup and
// internal on the connection) and redirect-based SSRF.
package netguard

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"syscall"
	"time"
)

// allowPrivate is a test-only escape hatch. The acceptance harness serves
// fixtures from 127.0.0.1, so it sets SKOED_ALLOW_PRIVATE_FETCH=1. It is never
// set in production.
var allowPrivate = os.Getenv("SKOED_ALLOW_PRIVATE_FETCH") == "1"

// IsUnsafeIP reports whether ip is not publicly routable: loopback, RFC1918
// private, link-local (incl. 169.254.169.254 cloud metadata), unspecified,
// IPv6 unique-local, or multicast.
func IsUnsafeIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	if v6 := ip.To16(); v6 != nil && ip.To4() == nil {
		if v6[0]&0xfe == 0xfc { // fc00::/7 unique-local
			return true
		}
	}
	return false
}

// safeControl rejects a connection whose resolved destination IP is unsafe.
// It runs after DNS resolution with the concrete address about to be dialed.
func safeControl(_, address string, _ syscall.RawConn) error {
	if allowPrivate {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("netguard: bad address %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	if IsUnsafeIP(ip) {
		return fmt.Errorf("netguard: refusing connection to non-public address %s", host)
	}
	return nil
}

// Client returns an *http.Client whose dialer refuses connections to non-public
// addresses. Use it for every fetch of an operator- or user-supplied URL.
func Client(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: safeControl}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			MaxIdleConns:          10,
		},
	}
}

// DialContext exposes the guarded dialer for callers that build their own
// transport (kept for completeness).
func DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: safeControl}
	return dialer.DialContext(ctx, network, addr)
}

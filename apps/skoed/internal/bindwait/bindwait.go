// Package bindwait retries binding a listener while its local address is
// not assigned yet.
//
// At boot, systemd can start skoed before the IP address of a Proxmox
// LXC container is configured, although the unit waits for
// network-online.target. Binding a specific IP then fails with
// EADDRNOTAVAIL ("cannot assign requested address"). Instead of exiting,
// skoed retries the bind for a bounded time.
package bindwait

import (
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"syscall"
	"time"
)

// Options sets how long and how often a bind is retried.
type Options struct {
	Interval time.Duration // time between two binds
	Window   time.Duration // time after the first failure before giving up
	Logf     func(format string, args ...any)
}

var defaults = Options{
	Interval: 500 * time.Millisecond,
	Window:   60 * time.Second,
	Logf:     log.Printf,
}

// Retry calls listen, which binds addr ("host:port"), with the default
// options: every 500 ms for up to 60 seconds while it fails with
// EADDRNOTAVAIL. Any other error is returned at once.
func Retry[T any](addr string, listen func() (T, error)) (T, error) {
	return RetryWith(defaults, addr, listen)
}

// RetryWith is Retry with explicit options.
func RetryWith[T any](o Options, addr string, listen func() (T, error)) (T, error) {
	v, err := listen()
	if !isNotAssigned(err) {
		return v, err
	}
	host := hostOf(addr)
	o.Logf("waiting for local address %s to be assigned (listener %s)", host, addr)
	deadline := time.Now().Add(o.Window)
	for time.Now().Before(deadline) {
		time.Sleep(o.Interval)
		v, err = listen()
		if err == nil {
			o.Logf("local address %s assigned, listening on %s", host, addr)
			return v, nil
		}
		if !isNotAssigned(err) {
			return v, err
		}
	}
	var zero T
	return zero, fmt.Errorf("address %s still not assigned after %s: %w", addr, seconds(o.Window), err)
}

// Listen is net.Listen retried with the default options.
func Listen(network, addr string) (net.Listener, error) {
	return ListenWith(defaults, network, addr)
}

// ListenWith is net.Listen retried with explicit options.
func ListenWith(o Options, network, addr string) (net.Listener, error) {
	return RetryWith(o, addr, func() (net.Listener, error) {
		return net.Listen(network, addr)
	})
}

func isNotAssigned(err error) bool {
	return errors.Is(err, syscall.EADDRNOTAVAIL)
}

func hostOf(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// seconds writes d as "60s" rather than time.Duration's "1m0s".
func seconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', -1, 64) + "s"
}

package bindwait

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// notAvail is the error net.Listen returns for an address no interface
// has yet.
func notAvail() error {
	return &net.OpError{Op: "listen", Net: "tcp", Err: &os.SyscallError{Syscall: "bind", Err: syscall.EADDRNOTAVAIL}}
}

type recorder struct{ lines []string }

func (r *recorder) logf(format string, args ...any) {
	r.lines = append(r.lines, fmt.Sprintf(format, args...))
}

func testOptions(r *recorder) Options {
	return Options{Interval: time.Millisecond, Window: 50 * time.Millisecond, Logf: r.logf}
}

func TestRetryWithSucceedsAfterAddressIsAssigned(t *testing.T) {
	var r recorder
	calls := 0
	got, err := RetryWith(testOptions(&r), "10.10.10.21:7000", func() (string, error) {
		calls++
		if calls <= 3 {
			return "", fmt.Errorf("raft transport: %w", notAvail())
		}
		return "listener", nil
	})
	if err != nil {
		t.Fatalf("RetryWith: %v", err)
	}
	if got != "listener" || calls != 4 {
		t.Fatalf("got %q after %d calls, want \"listener\" after 4", got, calls)
	}
	want := []string{
		"waiting for local address 10.10.10.21 to be assigned (listener 10.10.10.21:7000)",
		"local address 10.10.10.21 assigned, listening on 10.10.10.21:7000",
	}
	if strings.Join(r.lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("log lines:\n%s\nwant:\n%s", strings.Join(r.lines, "\n"), strings.Join(want, "\n"))
	}
}

func TestRetryWithGivesUpAfterWindow(t *testing.T) {
	var r recorder
	calls := 0
	start := time.Now()
	_, err := RetryWith(testOptions(&r), "10.10.10.14:7000", func() (int, error) {
		calls++
		return 0, notAvail()
	})
	if err == nil {
		t.Fatal("RetryWith succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "address 10.10.10.14:7000 still not assigned after 0.05s") {
		t.Fatalf("error %q does not name the address and the window", err)
	}
	if !errors.Is(err, syscall.EADDRNOTAVAIL) {
		t.Fatalf("error %q does not wrap EADDRNOTAVAIL", err)
	}
	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Fatalf("gave up after %s, before the 50ms window", elapsed)
	}
	if calls < 2 {
		t.Fatalf("listen called %d times, want retries", calls)
	}
	if len(r.lines) != 1 || r.lines[0] != "waiting for local address 10.10.10.14 to be assigned (listener 10.10.10.14:7000)" {
		t.Fatalf("log lines %q, want the single waiting line", r.lines)
	}
}

func TestRetryWithFailsAtOnceOnOtherErrors(t *testing.T) {
	for _, errno := range []syscall.Errno{syscall.EADDRINUSE, syscall.EACCES} {
		var r recorder
		calls := 0
		_, err := RetryWith(testOptions(&r), "10.10.10.21:7000", func() (int, error) {
			calls++
			return 0, &net.OpError{Op: "listen", Net: "tcp", Err: &os.SyscallError{Syscall: "bind", Err: errno}}
		})
		if !errors.Is(err, errno) {
			t.Fatalf("%v: got error %v", errno, err)
		}
		if calls != 1 || len(r.lines) != 0 {
			t.Fatalf("%v: %d calls and log lines %q, want 1 call and no log", errno, calls, r.lines)
		}
	}
}

func TestRetryWithStopsOnOtherErrorWhileWaiting(t *testing.T) {
	var r recorder
	calls := 0
	_, err := RetryWith(testOptions(&r), "10.10.10.21:7000", func() (int, error) {
		calls++
		if calls == 1 {
			return 0, notAvail()
		}
		return 0, syscall.EADDRINUSE
	})
	if !errors.Is(err, syscall.EADDRINUSE) || calls != 2 {
		t.Fatalf("got %v after %d calls, want EADDRINUSE after 2", err, calls)
	}
	if len(r.lines) != 1 {
		t.Fatalf("log lines %q, want only the waiting line", r.lines)
	}
}

func TestRetryWithLogsNothingOnSuccess(t *testing.T) {
	var r recorder
	if _, err := RetryWith(testOptions(&r), "127.0.0.1:0", func() (int, error) { return 1, nil }); err != nil {
		t.Fatalf("RetryWith: %v", err)
	}
	if len(r.lines) != 0 {
		t.Fatalf("log lines %q, want none", r.lines)
	}
}

func TestRetryWithIPv6Host(t *testing.T) {
	var r recorder
	_, _ = RetryWith(Options{Interval: time.Millisecond, Window: time.Millisecond, Logf: r.logf}, "[2001:db8::1]:7000", func() (int, error) {
		return 0, notAvail()
	})
	if len(r.lines) != 1 || !strings.Contains(r.lines[0], "waiting for local address 2001:db8::1 to be assigned (listener [2001:db8::1]:7000)") {
		t.Fatalf("log lines %q", r.lines)
	}
}

func TestDefaultWindowIsWrittenInSeconds(t *testing.T) {
	if got := seconds(defaults.Window); got != "60s" {
		t.Fatalf("default window written %q, want 60s", got)
	}
	if defaults.Interval != 500*time.Millisecond {
		t.Fatalf("default interval %s, want 500ms", defaults.Interval)
	}
}

// TestListenWithUnassignedAddress checks that the error of a real bind on
// an address no interface has (TEST-NET-1) is recognized.
func TestListenWithUnassignedAddress(t *testing.T) {
	var r recorder
	ln, err := ListenWith(Options{Interval: time.Millisecond, Window: 10 * time.Millisecond, Logf: r.logf}, "tcp", "192.0.2.123:0")
	if err == nil {
		ln.Close()
		t.Skip("192.0.2.123 is assigned on this host")
	}
	if !strings.Contains(err.Error(), "address 192.0.2.123:0 still not assigned after 0.01s") {
		t.Fatalf("error %q", err)
	}
	if len(r.lines) != 1 {
		t.Fatalf("log lines %q, want one", r.lines)
	}
}

func TestListenWithLoopback(t *testing.T) {
	var r recorder
	ln, err := ListenWith(testOptions(&r), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenWith: %v", err)
	}
	ln.Close()
	if len(r.lines) != 0 {
		t.Fatalf("log lines %q, want none", r.lines)
	}
}

package acceptance

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	os.Setenv("SKOED_TEST_MODE", "1")
	// Acceptance fixtures (blocklists, webhook receivers, upstreams) are served
	// from 127.0.0.1, so allow the SSRF guard to reach private/loopback in tests.
	// Spawned skoed nodes inherit this via os.Environ(). Never set in production.
	os.Setenv("SKOED_ALLOW_PRIVATE_FETCH", "1")
	os.Exit(m.Run())
}

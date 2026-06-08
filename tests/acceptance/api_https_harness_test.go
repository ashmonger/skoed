// Harness extension for M4.6 management-API HTTPS tests.

package acceptance

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// APITLSOpts is the per-node management-API TLS knob set the harness
// writes under node.api.tls.* in config.yaml.
type APITLSOpts struct {
	Enabled  bool
	Mode     string // "single_port" (default) | "dual_port"
	HSTS     bool
	CertFile string // node.dns.tls.cert_file (shared with DoH/DoT)
	KeyFile  string // node.dns.tls.key_file
}

// startClusterAPIHTTPS spins a single-node cluster with the management
// API serving HTTPS. Sets Node.APIHTTPSBase so tests can target it.
func startClusterAPIHTTPS(t *testing.T, opts APITLSOpts) *Cluster {
	t.Helper()
	bin := skoedBinary(t)
	if _, err := os.Stat(bin); os.IsNotExist(err) {
		t.Skipf("skoed binary not found at %s (set SKOED_BINARY to override)", bin)
	}
	c := &Cluster{t: t, bin: bin}
	apiPort := freeTCPPort(t)
	cfg := M2NodeConfig{
		NodeID:         "node-1",
		DNSPort:        freeUDPPort(t),
		APIPort:        apiPort,
		RaftPort:       freeTCPPort(t),
		TLSCertFile:    opts.CertFile,
		TLSKeyFile:     opts.KeyFile,
		APITLSEnabled:  opts.Enabled,
		APITLSMode:     opts.Mode,
		APITLSHSTS:     opts.HSTS,
	}
	httpsPort := apiPort
	if opts.Mode == "dual_port" {
		cfg.APITLSHTTPSPort = freeTCPPort(t)
		httpsPort = cfg.APITLSHTTPSPort
	}
	cn := c.spawnNode(t, cfg)
	cn.Node.APIHTTPSBase = fmt.Sprintf("https://127.0.0.1:%d", httpsPort)
	c.nodes = append(c.nodes, cn)
	waitReadyHTTPS(t, cn.Node, opts.Mode)
	setupAuth(t, cn.Node)
	return c
}

// waitReadyHTTPS is waitReady's HTTPS-aware sibling. In single-port
// mode the same port serves both (plain HTTP is now a redirect), so we
// poll the HTTPS URL. In dual-port mode plain HTTP is unchanged, so the
// regular waitReady on n.APIBase works.
func waitReadyHTTPS(t *testing.T, n *Node, mode string) {
	t.Helper()
	if mode == "dual_port" {
		waitReady(t, n)
		return
	}
	// single-port: poll the HTTPS endpoint with cert verification off.
	client := tlsAPIClient()
	deadline := time.Now().Add(readyTimeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(n.APIHTTPSBase + "/api/v1/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 || resp.StatusCode == 401 {
				return
			}
		}
		time.Sleep(readyPollInterval)
	}
	t.Fatalf("skoed did not become ready over HTTPS within %s at %s", readyTimeout, n.APIHTTPSBase)
}

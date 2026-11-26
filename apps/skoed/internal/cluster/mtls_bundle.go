package cluster

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// MTLSBundle is the cluster CA and the node leaf a leader hands to a
// joining node through POST /api/v1/cluster/mtls-bootstrap.
type MTLSBundle struct {
	CACertPEM   []byte `json:"ca_cert_pem"`
	LeafCertPEM []byte `json:"leaf_cert_pem"`
	LeafKeyPEM  []byte `json:"leaf_key_pem"`
}

// MTLSBootstrapRejectedError is returned by FetchMTLSBundle when the
// leader answers with a 4xx status (bad or expired token, invalid node
// ID). It is not retried.
type MTLSBootstrapRejectedError struct {
	StatusCode int
	Body       []byte
}

func (e *MTLSBootstrapRejectedError) Error() string {
	return fmt.Sprintf("mtls-bootstrap rejected (status %d): %s", e.StatusCode, string(e.Body))
}

// mtlsBootstrapDeadline, mtlsBootstrapRequestTimeout and
// mtlsBootstrapRetryDelay set how long FetchMTLSBundle keeps trying a
// leader that is unreachable or answers 5xx.
var (
	mtlsBootstrapDeadline       = 30 * time.Second
	mtlsBootstrapRequestTimeout = 5 * time.Second
	mtlsBootstrapRetryDelay     = 500 * time.Millisecond
)

// FetchMTLSBundle POSTs {token, node_id} to the leader's
// /api/v1/cluster/mtls-bootstrap and returns the bundle it answers. The
// leader validates the token but does not consume it; the following
// /api/v1/cluster/join call does. Network errors and 5xx answers are
// retried for 30 seconds; a 4xx answer fails at once with a
// *MTLSBootstrapRejectedError. Nothing is written to disk.
func FetchMTLSBundle(leaderBaseURL, token, nodeID string) (*MTLSBundle, error) {
	body, err := json.Marshal(map[string]string{
		"token":   token,
		"node_id": nodeID,
	})
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(leaderBaseURL, "/") + "/api/v1/cluster/mtls-bootstrap"
	client := &http.Client{Timeout: mtlsBootstrapRequestTimeout}
	deadline := time.Now().Add(mtlsBootstrapDeadline)
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(mtlsBootstrapRetryDelay)
			continue
		}
		buf, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			var b MTLSBundle
			if err := json.Unmarshal(buf, &b); err != nil {
				return nil, fmt.Errorf("decode mtls-bootstrap response: %w", err)
			}
			return &b, nil
		}
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return nil, &MTLSBootstrapRejectedError{StatusCode: resp.StatusCode, Body: buf}
		}
		lastErr = fmt.Errorf("mtls-bootstrap status %d: %s", resp.StatusCode, string(buf))
		time.Sleep(mtlsBootstrapRetryDelay)
	}
	return nil, fmt.Errorf("mtls-bootstrap: %w", lastErr)
}

// InstallMTLSBundle writes the bundle's CA cert, leaf cert and leaf key
// under MtlsPaths(dataDir), each through a temporary file renamed into
// place, then removes the node's own CA key: a node that received the
// cluster CA no longer signs anything. A missing CA key is not an error.
// The bundle is checked before anything is written, so a malformed one
// leaves the directory unchanged.
func InstallMTLSBundle(dataDir string, b *MTLSBundle) error {
	if b == nil {
		return errors.New("install mtls bundle: nil bundle")
	}
	if blk, _ := pem.Decode(b.CACertPEM); blk == nil {
		return errors.New("install mtls bundle: CA cert: pem decode failed")
	}
	if _, err := tls.X509KeyPair(b.LeafCertPEM, b.LeafKeyPEM); err != nil {
		return fmt.Errorf("install mtls bundle: leaf keypair: %w", err)
	}

	p := MtlsPaths(dataDir)
	if err := os.MkdirAll(p.Dir, 0700); err != nil {
		return fmt.Errorf("create %s: %w", p.Dir, err)
	}
	files := []struct {
		path string
		data []byte
		mode os.FileMode
	}{
		{p.CACertFile, b.CACertPEM, 0644},
		{p.NodeCert, b.LeafCertPEM, 0644},
		{p.NodeKey, b.LeafKeyPEM, 0600},
	}
	// Write every temporary file first so a failure leaves the installed
	// files untouched, then rename them into place.
	tmps := make([]string, 0, len(files))
	defer func() {
		for _, t := range tmps {
			_ = os.Remove(t)
		}
	}()
	for _, f := range files {
		tmp := f.path + ".tmp"
		if err := os.WriteFile(tmp, f.data, f.mode); err != nil {
			return fmt.Errorf("write %s: %w", tmp, err)
		}
		// WriteFile keeps the mode of an existing file; set it explicitly.
		if err := os.Chmod(tmp, f.mode); err != nil {
			return fmt.Errorf("chmod %s: %w", tmp, err)
		}
		tmps = append(tmps, tmp)
	}
	for i, f := range files {
		if err := os.Rename(tmps[i], f.path); err != nil {
			return fmt.Errorf("install %s: %w", f.path, err)
		}
	}
	tmps = nil
	if err := os.Remove(p.CAKeyFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove own CA key: %w", err)
	}
	return nil
}

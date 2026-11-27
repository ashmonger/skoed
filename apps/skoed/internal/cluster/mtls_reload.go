package cluster

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

// certBundle holds the current TLS material loaded into the CertCache.
type certBundle struct {
	cert     tls.Certificate
	caCert   []byte
	caPool   *x509.CertPool
	caExpiry time.Time
}

// ReloadClusterCerts reads the cluster CA and this node's leaf from disk
// (LoadClusterCA, EnsureNodeLeaf; the CA key may be absent) and switches
// the running process to them: the peer listener takes its leaf and its
// client-CA pool from the cert cache at each handshake, the peer client
// rebuilds its per-node transports when the cache's CA changes, and
// MTLSConfig, MTLSBundle and the leaves this node issues use the new CA.
// The Raft stream layer is not changed; ResetRaftForJoin builds a new one
// from the same files. On error, nothing is changed.
func (c *Cluster) ReloadClusterCerts() error {
	if !c.mtlsEnabled || c.peer.certCache == nil {
		return errors.New("mTLS is not enabled on this node")
	}
	dataDir, nodeID := c.node.Node.DataDir, c.node.Node.ID
	caCertPEM, caKeyPEM, err := LoadClusterCA(dataDir)
	if err != nil {
		return fmt.Errorf("reload cluster CA: %w", err)
	}
	leafCertPEM, leafKeyPEM, err := EnsureNodeLeaf(dataDir, nodeID, caCertPEM, caKeyPEM)
	if err != nil {
		return fmt.Errorf("reload node leaf: %w", err)
	}
	if err := verifyLeafSignedByCA(caCertPEM, leafCertPEM); err != nil {
		return fmt.Errorf("reload node leaf: %w", err)
	}
	if err := c.peer.certCache.Update(caCertPEM, leafCertPEM, leafKeyPEM); err != nil {
		return fmt.Errorf("reload cert cache: %w", err)
	}
	c.mtlsMu.Lock()
	c.mtlsCA, c.mtlsLeafCert, c.mtlsLeafKey = caCertPEM, leafCertPEM, leafKeyPEM
	c.mtlsMu.Unlock()
	if expiry, err := parseCertExpiry(leafCertPEM); err == nil {
		_ = c.store.SetNodeCertExpiry(nodeID, expiry)
	}
	return nil
}

// verifyLeafSignedByCA checks that the leaf PEM is a certificate signed by
// the CA PEM, so a mismatched pair on disk is refused before it is used.
func verifyLeafSignedByCA(caCertPEM, leafCertPEM []byte) error {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCertPEM) {
		return errors.New("CA cert: invalid PEM")
	}
	block, _ := pem.Decode(leafCertPEM)
	if block == nil {
		return errors.New("leaf cert: pem decode failed")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("leaf cert: %w", err)
	}
	_, err = leaf.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	})
	if err != nil {
		return fmt.Errorf("leaf cert not signed by the cluster CA: %w", err)
	}
	return nil
}

// CertCache holds the current TLS material behind an atomic pointer,
// enabling hot-reload of leaf certs without closing existing connections.
// New connections call GetCertificate which dereferences the pointer at
// handshake time, so they always see the latest certificate.
type CertCache struct {
	val atomic.Pointer[certBundle]
}

// NewCertCache builds a CertCache from the supplied PEM blobs and stores
// the initial bundle atomically.
func NewCertCache(caCertPEM, leafCertPEM, leafKeyPEM []byte) (*CertCache, error) {
	cc := &CertCache{}
	if err := cc.Update(caCertPEM, leafCertPEM, leafKeyPEM); err != nil {
		return nil, err
	}
	return cc, nil
}

// Update atomically swaps in a new certificate bundle. Safe to call
// concurrently; existing TLS connections are not interrupted.
func (cc *CertCache) Update(caCertPEM, leafCertPEM, leafKeyPEM []byte) error {
	leaf, err := tls.X509KeyPair(leafCertPEM, leafKeyPEM)
	if err != nil {
		return fmt.Errorf("load leaf keypair: %w", err)
	}
	caExpiry, err := parseCertExpiry(caCertPEM)
	if err != nil {
		return fmt.Errorf("parse CA cert: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCertPEM) {
		return errors.New("append CA: invalid PEM")
	}
	b := &certBundle{
		cert:     leaf,
		caCert:   append([]byte(nil), caCertPEM...),
		caPool:   pool,
		caExpiry: caExpiry,
	}
	cc.val.Store(b)
	return nil
}

// GetCertificate implements tls.Config.GetCertificate. Called per-handshake
// so new connections always receive the latest leaf certificate.
func (cc *CertCache) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	b := cc.val.Load()
	if b == nil {
		return nil, errors.New("cert cache not initialised")
	}
	c := b.cert
	return &c, nil
}

// CACert returns the current CA certificate PEM.
func (cc *CertCache) CACert() []byte {
	b := cc.val.Load()
	if b == nil {
		return nil
	}
	return b.caCert
}

// CAPool returns the pool holding the current CA certificate, or nil when
// the cache is empty.
func (cc *CertCache) CAPool() *x509.CertPool {
	b := cc.val.Load()
	if b == nil {
		return nil
	}
	return b.caPool
}

// LeafExpiry returns the NotAfter of the current leaf certificate.
func (cc *CertCache) LeafExpiry() (time.Time, error) {
	b := cc.val.Load()
	if b == nil {
		return time.Time{}, errors.New("cert cache not initialised")
	}
	return parseTLSCertExpiry(b.cert)
}

// CAExpiry returns the NotAfter of the current CA certificate.
func (cc *CertCache) CAExpiry() (time.Time, error) {
	b := cc.val.Load()
	if b == nil {
		return time.Time{}, errors.New("cert cache not initialised")
	}
	return b.caExpiry, nil
}

// BuildClusterTLSConfigDynamic builds a server *tls.Config backed by the
// CertCache: GetConfigForClient returns, per handshake, a copy of the
// config with the leaf (GetCertificate) and the client-CA pool taken from
// the cache, so new connections use the latest CA and leaf without a
// listener restart. Fields set on the returned config after this call
// (e.g. NextProtos) are kept in the per-handshake copy.
func BuildClusterTLSConfigDynamic(cc *CertCache) (*tls.Config, error) {
	pool := cc.CAPool()
	if pool == nil {
		return nil, errors.New("cert cache not initialised")
	}
	cfg := &tls.Config{
		GetCertificate: cc.GetCertificate,
		ClientCAs:      pool,
		ClientAuth:     tls.RequireAndVerifyClientCert,
		MinVersion:     tls.VersionTLS12,
	}
	cfg.GetConfigForClient = func(*tls.ClientHelloInfo) (*tls.Config, error) {
		pool := cc.CAPool()
		if pool == nil {
			return nil, errors.New("cert cache not initialised")
		}
		hs := cfg.Clone()
		hs.GetConfigForClient = nil
		hs.ClientCAs = pool
		return hs, nil
	}
	return cfg, nil
}

// parseCertExpiry decodes the first certificate in the PEM block and
// returns its NotAfter.
func parseCertExpiry(certPEM []byte) (time.Time, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return time.Time{}, errors.New("pem decode failed")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse cert: %w", err)
	}
	return cert.NotAfter, nil
}

// parseTLSCertExpiry extracts NotAfter from a tls.Certificate's parsed leaf.
func parseTLSCertExpiry(cert tls.Certificate) (time.Time, error) {
	if cert.Leaf != nil {
		return cert.Leaf.NotAfter, nil
	}
	if len(cert.Certificate) == 0 {
		return time.Time{}, errors.New("no certificate in chain")
	}
	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return time.Time{}, fmt.Errorf("parse leaf: %w", err)
	}
	return parsed.NotAfter, nil
}

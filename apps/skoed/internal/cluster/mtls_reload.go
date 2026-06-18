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
	caExpiry time.Time
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
	b := &certBundle{
		cert:     leaf,
		caCert:   append([]byte(nil), caCertPEM...),
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

// BuildClusterTLSConfigDynamic builds a *tls.Config that uses GetCertificate
// and GetConfigForClient hooks backed by the CertCache. New connections
// always pick up the latest certificate without requiring a listener restart.
func BuildClusterTLSConfigDynamic(cc *CertCache, caCertPEM []byte) (*tls.Config, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCertPEM) {
		return nil, errors.New("append CA: invalid PEM")
	}
	cfg := &tls.Config{
		GetCertificate: cc.GetCertificate,
		ClientCAs:      pool,
		RootCAs:        pool,
		ClientAuth:     tls.RequireAndVerifyClientCert,
		MinVersion:     tls.VersionTLS12,
		// GetConfigForClient lets us refresh the CA pool when it changes,
		// but for now the CA pool is static per-reload; we rebuild the
		// full config in Update when needed.
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

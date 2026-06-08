package cluster

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/hashicorp/raft"
)

// MTLSPaths returns the canonical on-disk paths for the cluster's TLS
// material under <data_dir>/tls/cluster/.
type MTLSPaths struct {
	Dir         string
	CACertFile  string
	CAKeyFile   string
	NodeCert    string
	NodeKey     string
}

// MtlsDir returns the directory holding the cluster TLS material.
func MtlsDir(dataDir string) string {
	return filepath.Join(dataDir, "tls", "cluster")
}

// MtlsPaths returns the canonical file paths.
func MtlsPaths(dataDir string) MTLSPaths {
	dir := MtlsDir(dataDir)
	return MTLSPaths{
		Dir:        dir,
		CACertFile: filepath.Join(dir, "ca.crt"),
		CAKeyFile:  filepath.Join(dir, "ca.key"),
		NodeCert:   filepath.Join(dir, "node.crt"),
		NodeKey:    filepath.Join(dir, "node.key"),
	}
}

// GenerateClusterCA generates an ECDSA P-256 cluster CA and writes
// both cert + key to disk. Called only on the BOOTSTRAP node — joining
// nodes never have the CA private key (the leader does). Use
// LoadClusterCA on followers.
func GenerateClusterCA(dataDir string) (caCertPEM, caKeyPEM []byte, err error) {
	p := MtlsPaths(dataDir)
	if err := os.MkdirAll(p.Dir, 0700); err != nil {
		return nil, nil, fmt.Errorf("create %s: %w", p.Dir, err)
	}
	// Idempotent: if both files exist, reuse them.
	if certPEM, err := os.ReadFile(p.CACertFile); err == nil {
		keyPEM, err := os.ReadFile(p.CAKeyFile)
		if err == nil {
			return certPEM, keyPEM, nil
		}
	}

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate CA key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, nil, fmt.Errorf("serial: %w", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "dblock cluster CA", Organization: []string{"dblock"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
	}
	der, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("self-sign CA: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal CA key: %w", err)
	}

	caCertPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	caKeyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	if err := os.WriteFile(p.CACertFile, caCertPEM, 0644); err != nil {
		return nil, nil, fmt.Errorf("write CA cert: %w", err)
	}
	if err := os.WriteFile(p.CAKeyFile, caKeyPEM, 0600); err != nil {
		return nil, nil, fmt.Errorf("write CA key: %w", err)
	}
	return caCertPEM, caKeyPEM, nil
}

// LoadClusterCA reads the CA cert from disk. Returns empty caKeyPEM
// when the CA private key isn't present (the joining-node case — only
// the leader needs the CA private key to issue new leaves).
func LoadClusterCA(dataDir string) (caCertPEM, caKeyPEM []byte, err error) {
	p := MtlsPaths(dataDir)
	caCertPEM, err = os.ReadFile(p.CACertFile)
	if err != nil {
		return nil, nil, fmt.Errorf("read CA cert: %w", err)
	}
	if k, err := os.ReadFile(p.CAKeyFile); err == nil {
		caKeyPEM = k
	}
	return caCertPEM, caKeyPEM, nil
}

// IssueLeafCert signs a leaf certificate for the named node, using the
// provided CA PEMs. Returns leaf cert + leaf key PEMs.
func IssueLeafCert(caCertPEM, caKeyPEM []byte, nodeID string, ipSANs []net.IP) (certPEM, keyPEM []byte, err error) {
	caCert, caKey, err := parseCA(caCertPEM, caKeyPEM)
	if err != nil {
		return nil, nil, err
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate leaf key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, nil, err
	}

	// Always trust localhost + loopback IPs so the harness + operators
	// running on the same box don't have to wire DNS for testing.
	sans := append([]net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}, ipSANs...)

	leafTmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: nodeID, Organization: []string{"dblock"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:     []string{nodeID, "localhost"},
		IPAddresses:  sans,
	}
	der, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("sign leaf: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

// EnsureNodeLeaf returns the per-node leaf PEMs. If the leaf already
// exists on disk (the joining-node case — leaf came in via the
// mtls-bootstrap response), it's loaded as-is. Otherwise — the leader
// case — a fresh leaf is minted using the CA private key. Fails
// loudly when the leaf is missing AND we have no CA private key
// (misconfiguration we can't recover from at runtime).
func EnsureNodeLeaf(dataDir, nodeID string, caCertPEM, caKeyPEM []byte) (certPEM, keyPEM []byte, err error) {
	p := MtlsPaths(dataDir)
	if cert, err := os.ReadFile(p.NodeCert); err == nil {
		key, err := os.ReadFile(p.NodeKey)
		if err == nil {
			return cert, key, nil
		}
	}
	if len(caKeyPEM) == 0 {
		return nil, nil, fmt.Errorf("no node leaf on disk and no CA private key available (mtls-bootstrap missing?)")
	}
	cert, key, err := IssueLeafCert(caCertPEM, caKeyPEM, nodeID, nil)
	if err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(p.NodeCert, cert, 0644); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(p.NodeKey, key, 0600); err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

func parseCA(certPEM, keyPEM []byte) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	cb, _ := pem.Decode(certPEM)
	if cb == nil {
		return nil, nil, errors.New("ca cert: pem decode failed")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("ca cert parse: %w", err)
	}
	kb, _ := pem.Decode(keyPEM)
	if kb == nil {
		return nil, nil, errors.New("ca key: pem decode failed")
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("ca key parse: %w", err)
	}
	return cert, key, nil
}

// BuildClusterTLSConfig builds the *tls.Config used by both Raft and
// internal-API peer connections. RequireAndVerifyClientCert means every
// peer must present a cluster-CA-signed cert.
func BuildClusterTLSConfig(caCertPEM, leafCertPEM, leafKeyPEM []byte) (*tls.Config, error) {
	leaf, err := tls.X509KeyPair(leafCertPEM, leafKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("load leaf keypair: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCertPEM) {
		return nil, errors.New("append CA: invalid PEM")
	}
	return &tls.Config{
		Certificates: []tls.Certificate{leaf},
		ClientCAs:    pool,
		RootCAs:      pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// TLSStreamLayer wraps a TLS-listening socket so it satisfies
// hashicorp/raft's StreamLayer interface, letting Raft peers handshake
// with the cluster CA before any AppendEntries traffic flows.
type TLSStreamLayer struct {
	listener net.Listener
	cfg      *tls.Config
	advertise net.Addr
}

// NewTLSStreamLayer binds to bindAddr and accepts TLS connections
// validated against the cluster CA. advertise is the address peers
// learn — defaults to the bound address.
func NewTLSStreamLayer(bindAddr string, advertise net.Addr, cfg *tls.Config) (*TLSStreamLayer, error) {
	l, err := tls.Listen("tcp", bindAddr, cfg)
	if err != nil {
		return nil, fmt.Errorf("tls listen %s: %w", bindAddr, err)
	}
	if advertise == nil {
		advertise = l.Addr()
	}
	return &TLSStreamLayer{listener: l, cfg: cfg, advertise: advertise}, nil
}

func (t *TLSStreamLayer) Accept() (net.Conn, error) { return t.listener.Accept() }
func (t *TLSStreamLayer) Close() error              { return t.listener.Close() }
func (t *TLSStreamLayer) Addr() net.Addr            { return t.advertise }

// Dial connects to a peer over TLS, presenting our leaf and verifying
// theirs against the cluster CA.
func (t *TLSStreamLayer) Dial(address raft.ServerAddress, timeout time.Duration) (net.Conn, error) {
	d := &net.Dialer{Timeout: timeout}
	return tls.DialWithDialer(d, "tcp", string(address), t.cfg)
}

package dns

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// EnsureSelfSignedCert returns the paths to a TLS certificate and key
// usable for the DoH/DoT listeners. If certFile and keyFile are non-empty
// and both files exist, they're returned as-is. Otherwise a fresh
// self-signed ECDSA P-256 cert is generated under <dataDir>/tls/ and
// reused on subsequent calls.
//
// commonName is used for the cert's Subject.CommonName and as a DNS SAN.
// It's typically the node_id; production deployments should override by
// supplying their own cert.
func EnsureSelfSignedCert(dataDir, certFile, keyFile, commonName string) (string, string, error) {
	if certFile != "" && keyFile != "" {
		if _, err := os.Stat(certFile); err == nil {
			if _, err := os.Stat(keyFile); err == nil {
				return certFile, keyFile, nil
			}
		}
	}

	tlsDir := filepath.Join(dataDir, "tls")
	if err := os.MkdirAll(tlsDir, 0o700); err != nil {
		return "", "", fmt.Errorf("create tls dir: %w", err)
	}
	cp := filepath.Join(tlsDir, "cert.pem")
	kp := filepath.Join(tlsDir, "key.pem")

	if _, err := os.Stat(cp); err == nil {
		if _, err := os.Stat(kp); err == nil {
			return cp, kp, nil
		}
	}

	if err := generateSelfSigned(cp, kp, commonName); err != nil {
		return "", "", err
	}
	return cp, kp, nil
}

func generateSelfSigned(certPath, keyPath, commonName string) error {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("serial: %w", err)
	}

	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"skoed self-signed"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{commonName, "localhost"},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &priv.PublicKey, priv)
	if err != nil {
		return fmt.Errorf("create cert: %w", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return fmt.Errorf("marshal key: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return fmt.Errorf("write cert: %w", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return fmt.Errorf("write key: %w", err)
	}
	return nil
}

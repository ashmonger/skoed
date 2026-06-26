package config

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"

	"filippo.io/age"
	"gopkg.in/yaml.v3"
)

// ageHeader is the magic string at the beginning of every age-encrypted file.
const ageHeader = "age-encryption.org/v1"

// Import reads a tar.gz archive from r, validates the embedded config.yaml,
// and returns the parsed Config. Returns an error if the archive is malformed,
// the YAML is invalid, or schema validation fails.
// The caller is responsible for persisting the returned config.
func Import(r io.Reader) (*Config, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("import: open gzip stream: %w", err)
	}
	defer gz.Close()
	return importFromTar(gz)
}

// ImportWithPassphrase reads a tar.gz or age-encrypted archive from r. When
// the archive begins with the age magic header the passphrase is used to
// decrypt it before reading the inner tar.gz. An empty passphrase on an
// encrypted archive returns an error. A wrong passphrase returns an error
// containing "invalid passphrase or corrupted archive".
func ImportWithPassphrase(r io.Reader, passphrase string) (*Config, error) {
	// Buffer the full input so we can peek without consuming.
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("import: read input: %w", err)
	}
	if !bytes.HasPrefix(data, []byte(ageHeader)) {
		return Import(bytes.NewReader(data))
	}
	return importAge(bytes.NewReader(data), passphrase)
}

// importAge decrypts an age-encrypted archive and returns the parsed Config.
func importAge(r io.Reader, passphrase string) (*Config, error) {
	if passphrase == "" {
		return nil, fmt.Errorf("import: passphrase required for encrypted archive")
	}
	id, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return nil, fmt.Errorf("import: create age identity: %w", err)
	}
	dec, err := age.Decrypt(r, id)
	if err != nil {
		return nil, fmt.Errorf("invalid passphrase or corrupted archive")
	}
	gz, err := gzip.NewReader(dec)
	if err != nil {
		return nil, fmt.Errorf("import: open decrypted gzip stream: %w", err)
	}
	defer gz.Close()
	return importFromTar(gz)
}

// importFromTar reads a tar stream and returns the parsed Config from config.yaml.
func importFromTar(r io.Reader) (*Config, error) {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("import: read tar entry: %w", err)
		}
		if hdr.Name != "config.yaml" {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("import: read config.yaml: %w", err)
		}
		var c Config
		if err := yaml.Unmarshal(data, &c); err != nil {
			return nil, fmt.Errorf("import: parse config.yaml: %w", err)
		}
		c.Defaults()
		if err := c.Validate(); err != nil {
			return nil, fmt.Errorf("import: invalid config: %w", err)
		}
		return &c, nil
	}
	return nil, fmt.Errorf("import: config.yaml not found in archive")
}

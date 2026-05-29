package config

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

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

	tr := tar.NewReader(gz)

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

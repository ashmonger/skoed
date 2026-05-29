package config

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"time"

	"gopkg.in/yaml.v3"
)

// Export serialises the config to a tar.gz archive and writes it to w.
// For URL-sourced blocklists the Domains field is cleared (they will be
// re-downloaded on import). For inline blocklists Domains is included verbatim.
func Export(c *Config, w io.Writer) error {
	// Work on a shallow copy so we do not mutate the caller's config.
	exported := *c
	if len(exported.Filtering.Blocklists) > 0 {
		lists := make([]Blocklist, len(exported.Filtering.Blocklists))
		copy(lists, exported.Filtering.Blocklists)
		for i := range lists {
			if lists[i].Source.Type == "url" {
				lists[i].Domains = nil
			}
		}
		exported.Filtering.Blocklists = lists
	}

	data, err := yaml.Marshal(&exported)
	if err != nil {
		return fmt.Errorf("export: marshal config: %w", err)
	}

	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)

	hdr := &tar.Header{
		Name:     "config.yaml",
		Mode:     0600,
		Size:     int64(len(data)),
		ModTime:  time.Now().UTC(),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("export: write tar header: %w", err)
	}
	if _, err := io.Copy(tw, bytes.NewReader(data)); err != nil {
		return fmt.Errorf("export: write tar body: %w", err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("export: close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("export: close gzip: %w", err)
	}
	return nil
}

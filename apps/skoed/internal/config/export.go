package config

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"time"

	"filippo.io/age"
	"gopkg.in/yaml.v3"
)

// exportShape is a copy of Config with the Auth field omitted so that the
// backup archive never contains admin credentials. Using a separate struct
// (rather than zeroing the Auth field) ensures the field is absent from the
// YAML output even when yaml.v3 does not honour omitempty on struct values.
type exportShape struct {
	Version    int             `yaml:"version"`
	DNS        DNSConfig       `yaml:"dns"`
	Filtering  FilteringConfig `yaml:"filtering"`
	LocalDNS   LocalDNSConfig  `yaml:"local_dns"`
	API        APIConfig       `yaml:"api"`
	QueryLog   QueryLogConfig  `yaml:"query_log"`
	Profiles   []Profile       `yaml:"profiles,omitempty"`
	Schedules  []Schedule      `yaml:"schedules,omitempty"`
	Bindings   []ScheduleBinding `yaml:"schedule_bindings,omitempty"`
	Categories []CategoryOverride `yaml:"category_overrides,omitempty"`
}

// Export serialises the config to a tar.gz archive and writes it to w.
// For URL-sourced blocklists the Domains field is cleared (they will be
// re-downloaded on import). For inline blocklists Domains is included verbatim.
// Admin credentials are never included — they are a per-node secret and must
// not travel in a portable backup.
func Export(c *Config, w io.Writer) error {
	// Copy into the export shape which omits the Auth field entirely.
	exported := exportShape{
		Version:    c.Version,
		DNS:        c.DNS,
		Filtering:  c.Filtering,
		LocalDNS:   c.LocalDNS,
		API:        c.API,
		QueryLog:   c.QueryLog,
		Profiles:   c.Profiles,
		Schedules:  c.Schedules,
		Bindings:   c.Bindings,
		Categories: c.Categories,
	}
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

// ExportWithPassphrase serialises the config to a tar.gz archive. When
// passphrase is non-empty the archive is wrapped in an age-encrypted envelope
// so the caller receives an opaque binary blob that can only be decrypted with
// the same passphrase.
func ExportWithPassphrase(c *Config, w io.Writer, passphrase string) error {
	if passphrase == "" {
		return Export(c, w)
	}
	r, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return fmt.Errorf("export: create age recipient: %w", err)
	}
	aw, err := age.Encrypt(w, r)
	if err != nil {
		return fmt.Errorf("export: start age encryption: %w", err)
	}
	if err := Export(c, aw); err != nil {
		return err
	}
	return aw.Close()
}

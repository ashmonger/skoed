package config

import (
	"fmt"
	"io"
	"strings"
)

// DiffResult describes the structural difference between two config archives.
type DiffResult struct {
	Added   DiffSection `json:"added"`
	Removed DiffSection `json:"removed"`
	Changed DiffSection `json:"changed"`
}

// DiffSection holds per-resource differences for one direction of the diff.
type DiffSection struct {
	Blocklists []string          `json:"blocklists"`
	Allowlist  []string          `json:"allowlist"`
	LocalDNS   []string          `json:"local_dns"`
	Settings   map[string]string `json:"settings"`
}

// newDiffSection returns a DiffSection with non-nil slices and map.
func newDiffSection() DiffSection {
	return DiffSection{
		Blocklists: []string{},
		Allowlist:  []string{},
		LocalDNS:   []string{},
		Settings:   map[string]string{},
	}
}

// DiffArchives extracts config.yaml from two plain tar.gz archives and returns
// a structured diff. r1 is archive A (before), r2 is archive B (after).
// Encrypted archives must be decrypted before calling this function.
func DiffArchives(r1, r2 io.Reader) (*DiffResult, error) {
	cfgA, err := Import(r1)
	if err != nil {
		return nil, fmt.Errorf("diff: parse archive A: %w", err)
	}
	cfgB, err := Import(r2)
	if err != nil {
		return nil, fmt.Errorf("diff: parse archive B: %w", err)
	}
	return diffConfigs(cfgA, cfgB), nil
}

// diffConfigs computes the diff between two parsed configs.
func diffConfigs(a, b *Config) *DiffResult {
	res := &DiffResult{
		Added:   newDiffSection(),
		Removed: newDiffSection(),
		Changed: newDiffSection(),
	}
	diffBlocklists(a, b, res)
	diffAllowlist(a, b, res)
	diffLocalDNS(a, b, res)
	diffSettings(a, b, res)
	return res
}

func diffBlocklists(a, b *Config, res *DiffResult) {
	inA := make(map[string]bool, len(a.Filtering.Blocklists))
	for _, bl := range a.Filtering.Blocklists {
		inA[bl.ID] = true
	}
	for _, bl := range b.Filtering.Blocklists {
		if !inA[bl.ID] {
			res.Added.Blocklists = append(res.Added.Blocklists, bl.Name)
		}
	}
	inB := make(map[string]bool, len(b.Filtering.Blocklists))
	for _, bl := range b.Filtering.Blocklists {
		inB[bl.ID] = true
	}
	for _, bl := range a.Filtering.Blocklists {
		if !inB[bl.ID] {
			res.Removed.Blocklists = append(res.Removed.Blocklists, bl.Name)
		}
	}
}

func diffAllowlist(a, b *Config, res *DiffResult) {
	inA := make(map[string]bool, len(a.Filtering.Allowlist))
	for _, d := range a.Filtering.Allowlist {
		inA[d] = true
	}
	for _, d := range b.Filtering.Allowlist {
		if !inA[d] {
			res.Added.Allowlist = append(res.Added.Allowlist, d)
		}
	}
	inB := make(map[string]bool, len(b.Filtering.Allowlist))
	for _, d := range b.Filtering.Allowlist {
		inB[d] = true
	}
	for _, d := range a.Filtering.Allowlist {
		if !inB[d] {
			res.Removed.Allowlist = append(res.Removed.Allowlist, d)
		}
	}
}

func diffLocalDNS(a, b *Config, res *DiffResult) {
	inA := make(map[string]bool, len(a.LocalDNS.Entries))
	for _, e := range a.LocalDNS.Entries {
		inA[e.ID] = true
	}
	for _, e := range b.LocalDNS.Entries {
		if !inA[e.ID] {
			res.Added.LocalDNS = append(res.Added.LocalDNS, e.Hostname)
		}
	}
	inB := make(map[string]bool, len(b.LocalDNS.Entries))
	for _, e := range b.LocalDNS.Entries {
		inB[e.ID] = true
	}
	for _, e := range a.LocalDNS.Entries {
		if !inB[e.ID] {
			res.Removed.LocalDNS = append(res.Removed.LocalDNS, e.Hostname)
		}
	}
}

func diffSettings(a, b *Config, res *DiffResult) {
	upA := strings.Join(a.DNS.UpstreamResolvers, ",")
	upB := strings.Join(b.DNS.UpstreamResolvers, ",")
	if upA != upB {
		res.Changed.Settings["upstream_resolvers"] = upB
	}
	if a.Filtering.BlockPolicy != b.Filtering.BlockPolicy {
		res.Changed.Settings["block_policy"] = b.Filtering.BlockPolicy
	}
}

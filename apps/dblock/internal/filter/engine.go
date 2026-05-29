package filter

import (
	"strings"
	"sync"

	"github.com/dblock/dblock/internal/config"
	"github.com/dblock/dblock/internal/filter/parsers"
)

type BlockPolicy int

const (
	PolicyInherit  BlockPolicy = iota
	PolicyNXDOMAIN
	PolicyNULL
	PolicyNODATA
)

type Disposition int

const (
	Allow Disposition = iota
	Block
)

type Result struct {
	Disposition Disposition
	Policy      BlockPolicy
	BlocklistID string
}

type domainSet struct {
	exact    map[string]struct{}
	wildcards []string // each entry is the suffix after "*."; e.g. "example.com"
}

func newDomainSet(entries []string) domainSet {
	ds := domainSet{
		exact: make(map[string]struct{}, len(entries)),
	}
	for _, e := range entries {
		e = strings.ToLower(e)
		if strings.HasPrefix(e, "*.") {
			ds.wildcards = append(ds.wildcards, e[2:])
		} else {
			ds.exact[e] = struct{}{}
			// Every plain domain entry implicitly blocks all its subdomains too.
			ds.wildcards = append(ds.wildcards, e)
		}
	}
	return ds
}

// matches returns true if domain matches an exact entry or a wildcard entry.
// Wildcard suffix "example.com" matches "example.com" and "*.example.com" at any depth.
func (ds *domainSet) matches(domain string) bool {
	if _, ok := ds.exact[domain]; ok {
		return true
	}
	for _, suffix := range ds.wildcards {
		if domain == suffix {
			return true
		}
		if strings.HasSuffix(domain, "."+suffix) {
			return true
		}
	}
	return false
}

type blocklistEntry struct {
	id     string
	policy BlockPolicy
	set    domainSet
}

type Engine struct {
	mu            sync.RWMutex
	globalPolicy  BlockPolicy
	allowlist     domainSet
	blocklists    []blocklistEntry
}

func parsePolicy(s string) BlockPolicy {
	switch s {
	case "nxdomain":
		return PolicyNXDOMAIN
	case "null":
		return PolicyNULL
	case "nodata":
		return PolicyNODATA
	default:
		return PolicyInherit
	}
}

func parseDomains(raw []string, format string) []string {
	if len(raw) == 0 {
		return nil
	}

	detectedFormat := format
	if detectedFormat == "auto" || detectedFormat == "" {
		detectedFormat = detectFormat(raw)
	}

	joined := strings.Join(raw, "\n")
	r := strings.NewReader(joined)

	var domains []string
	var err error
	switch detectedFormat {
	case "hosts":
		domains, err = parsers.ParseHosts(r)
	case "adblock":
		domains, err = parsers.ParseAdblock(r)
	default:
		domains, err = parsers.ParseDomainList(r)
	}
	if err != nil {
		return nil
	}
	return domains
}

func detectFormat(lines []string) string {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "!") {
			continue
		}
		if strings.HasPrefix(trimmed, "||") {
			return "adblock"
		}
		if strings.Contains(trimmed, " ") || strings.Contains(trimmed, "\t") {
			return "hosts"
		}
		return "domainlist"
	}
	return "domainlist"
}

func New(cfg config.FilteringConfig) *Engine {
	e := &Engine{
		globalPolicy: parsePolicy(cfg.BlockPolicy),
		allowlist:    newDomainSet(cfg.Allowlist),
	}

	for _, bl := range cfg.Blocklists {
		if !bl.Enabled {
			continue
		}
		domains := parseDomains(bl.Domains, bl.Source.Format)
		entry := blocklistEntry{
			id:     bl.ID,
			policy: parsePolicy(bl.BlockPolicy),
			set:    newDomainSet(domains),
		}
		e.blocklists = append(e.blocklists, entry)
	}

	return e
}

func (e *Engine) Evaluate(domain string) Result {
	e.mu.RLock()
	defer e.mu.RUnlock()

	domain = strings.ToLower(domain)

	if e.allowlist.matches(domain) {
		return Result{Disposition: Allow}
	}

	for _, bl := range e.blocklists {
		if bl.set.matches(domain) {
			return Result{
				Disposition: Block,
				Policy:      bl.policy,
				BlocklistID: bl.id,
			}
		}
	}

	return Result{Disposition: Allow}
}

func (e *Engine) EffectivePolicy(r Result) BlockPolicy {
	if r.Policy != PolicyInherit {
		return r.Policy
	}
	return e.globalPolicy
}

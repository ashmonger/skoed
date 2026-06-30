package filter

import (
	"fmt"
	"regexp"
	"strings"
)

// customRule is one parsed line from the custom rules text.
type customRule struct {
	isAllow bool
	isExact bool
	apex    string         // for exact-domain rules
	re      *regexp.Regexp // for regex rules
}

// customRuleSet holds all compiled custom rules.
type customRuleSet struct {
	rules []customRule
}

// ParseCustomRules parses the admin-supplied rules text and returns a compiled
// rule set. It returns an error on the first invalid line, with the 1-based
// line number included.
//
// Supported syntax:
//   - /regex/           → block domains matching regex (case-insensitive)
//   - @@/regex/         → allow domains matching regex
//   - domain            → exact-domain block (also matches all sub-domains)
//   - @@domain          → exact-domain allow
//   - ||domain^         → AdGuard domain-anchor syntax; equivalent to "domain"
//   - @@||domain^       → AdGuard allow form; equivalent to "@@domain"
//   - Empty lines and lines starting with # or ! (AdGuard comment) are ignored.
//
// Unsupported AdGuard modifiers ($client=, $important, $dnsrewrite=, …) are
// rejected with a descriptive error. For per-client filtering use Profiles.
func ParseCustomRules(text string) (customRuleSet, error) {
	var rs customRuleSet
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		r, err := parseSingleRule(line)
		if err != nil {
			return customRuleSet{}, fmt.Errorf("line %d: %w", i+1, err)
		}
		rs.rules = append(rs.rules, r)
	}
	return rs, nil
}

func parseSingleRule(s string) (customRule, error) {
	isAllow := strings.HasPrefix(s, "@@")
	if isAllow {
		s = s[2:]
	}
	// Regex rule: surrounded by /.../ with at least one character inside.
	// Checked before AdGuard anchor stripping so $ inside a regex pattern
	// is never mistaken for a modifier separator.
	if len(s) > 1 && s[0] == '/' && s[len(s)-1] == '/' {
		pattern := s[1 : len(s)-1]
		re, err := regexp.Compile("(?i)" + pattern)
		if err != nil {
			return customRule{}, fmt.Errorf("invalid regex %q: %w", pattern, err)
		}
		return customRule{isAllow: isAllow, isExact: false, re: re}, nil
	}
	// AdGuard domain-anchor prefix: ||domain → domain.
	if strings.HasPrefix(s, "||") {
		s = s[2:]
	}
	// AdGuard end-of-address anchor: ^ is the field separator between the
	// domain and optional $modifiers. Strip ^ and any trailing | end anchor;
	// reject anything else that follows (all $modifier= forms).
	if i := strings.IndexByte(s, '^'); i >= 0 {
		after := strings.TrimRight(s[i+1:], "|")
		s = s[:i]
		if after != "" {
			return customRule{}, adguardModifierError(after)
		}
	}
	// AdGuard $modifier syntax without ^ (e.g. domain$important).
	if i := strings.IndexByte(s, '$'); i >= 0 {
		return customRule{}, adguardModifierError(s[i:])
	}
	// Exact-domain rule. Normalise to apex (strip leading *. wildcard).
	apex := strings.ToLower(strings.TrimPrefix(s, "*."))
	if apex == "" {
		return customRule{}, fmt.Errorf("empty domain after stripping wildcard prefix")
	}
	return customRule{isAllow: isAllow, isExact: true, apex: apex}, nil
}

// adguardModifierError returns a clear error for unsupported AdGuard $modifiers.
func adguardModifierError(modifiers string) error {
	if strings.Contains(modifiers, "$client=") || strings.Contains(modifiers, "$ctag=") {
		return fmt.Errorf("modifier %q is not supported — for per-client filtering, assign the client to a profile and add the rule there", modifiers)
	}
	return fmt.Errorf("AdGuard modifier %q is not supported", modifiers)
}

// matchDomain reports whether the rule matches domain (direction-agnostic).
func (r *customRule) matchDomain(domain string) bool {
	if r.isExact {
		return domain == r.apex || strings.HasSuffix(domain, "."+r.apex)
	}
	return r.re.MatchString(domain)
}

// evaluate checks domain against all custom rules.
// Priority: allow rules are checked before block rules so "@@safe.example.com"
// wins when "/\.example\.com$/" also exists in the same rule set.
// Returns (matched, isAllow). When !matched the caller continues normally.
func (rs *customRuleSet) evaluate(domain string) (matched bool, isAllow bool) {
	for i := range rs.rules {
		if rs.rules[i].isAllow && rs.rules[i].matchDomain(domain) {
			return true, true
		}
	}
	for i := range rs.rules {
		if !rs.rules[i].isAllow && rs.rules[i].matchDomain(domain) {
			return true, false
		}
	}
	return false, false
}

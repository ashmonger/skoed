package parsers

import (
	"bufio"
	"io"
	"strings"
)

func ParseAskoed(r io.Reader) ([]string, error) {
	var domains []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "!") || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "||") {
			continue
		}
		// Strip leading ||
		line = line[2:]
		// Must end with ^ (possibly followed by options like ^$third-party)
		idx := strings.Index(line, "^")
		if idx == -1 {
			continue
		}
		domain := line[:idx]
		// Skip path rules
		if strings.Contains(domain, "/") {
			continue
		}
		if domain == "" {
			continue
		}
		domains = append(domains, strings.ToLower(domain))
	}
	return domains, scanner.Err()
}

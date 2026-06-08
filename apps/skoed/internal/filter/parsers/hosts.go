package parsers

import (
	"bufio"
	"io"
	"net"
	"strings"
)

var hostsSkip = map[string]struct{}{
	"localhost":             {},
	"localhost.localdomain": {},
	"broadcasthost":         {},
}

func ParseHosts(r io.Reader) ([]string, error) {
	var domains []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip inline comment
		if idx := strings.Index(line, "#"); idx != -1 {
			line = strings.TrimSpace(line[:idx])
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// fields[0] is the IP address, fields[1] is the domain
		domain := strings.ToLower(fields[1])
		if _, skip := hostsSkip[domain]; skip {
			continue
		}
		if net.ParseIP(domain) != nil {
			continue
		}
		domains = append(domains, domain)
	}
	return domains, scanner.Err()
}

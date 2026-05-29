package parsers

import (
	"bufio"
	"io"
	"strings"
)

func ParseDomainList(r io.Reader) ([]string, error) {
	var domains []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if idx := strings.Index(line, "#"); idx != -1 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		domains = append(domains, strings.ToLower(line))
	}
	return domains, scanner.Err()
}

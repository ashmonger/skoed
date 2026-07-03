package filter

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/skoed/skoed/internal/filter/parsers"
	"github.com/skoed/skoed/internal/netguard"
)

// maxBlocklistBytes bounds a downloaded blocklist so a hostile or compromised
// list server cannot exhaust memory on the node.
const maxBlocklistBytes = 64 << 20 // 64 MiB

func Download(url, format string, timeout time.Duration) ([]string, error) {
	// SSRF-guarded client: operator-supplied URLs must not reach internal /
	// loopback / cloud-metadata endpoints. Enforced at dial time.
	client := netguard.Client(timeout)
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: unexpected status %d", url, resp.StatusCode)
	}

	body := io.LimitReader(resp.Body, maxBlocklistBytes)
	if format == "auto" || format == "" {
		return downloadAutoDetect(body)
	}

	return parseByFormat(body, format)
}

func downloadAutoDetect(body io.Reader) ([]string, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	format := detectFormat(lines)
	return parseByFormat(strings.NewReader(string(data)), format)
}

func parseByFormat(r io.Reader, format string) ([]string, error) {
	switch format {
	case "hosts":
		return parsers.ParseHosts(r)
	case "askoed", "adblock":
		return parsers.ParseAskoed(r)
	default:
		return parsers.ParseDomainList(r)
	}
}

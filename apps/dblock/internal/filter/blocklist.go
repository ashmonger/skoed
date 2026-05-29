package filter

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dblock/dblock/internal/filter/parsers"
)

func Download(url, format string, timeout time.Duration) ([]string, error) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: unexpected status %d", url, resp.StatusCode)
	}

	if format == "auto" || format == "" {
		return downloadAutoDetect(resp.Body)
	}

	return parseByFormat(resp.Body, format)
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
	case "adblock":
		return parsers.ParseAdblock(r)
	default:
		return parsers.ParseDomainList(r)
	}
}

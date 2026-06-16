package upgrade

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// AssetKey returns the feed assets map key for the current runtime,
// e.g. "linux_amd64".
func AssetKey() string {
	return runtime.GOOS + "_" + runtime.GOARCH
}

// Swap downloads the tar.gz at assetURL, extracts the "skoed" binary
// entry, and atomically replaces exePath. The caller is responsible for
// calling os.Exit(0) after the HTTP response is flushed.
func Swap(assetURL, exePath string) error {
	dir := filepath.Dir(exePath)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(assetURL) //nolint:noctx
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}

	newPath, err := extractBinary(resp.Body, dir)
	if err != nil {
		return err
	}

	if err := os.Rename(newPath, exePath); err != nil {
		os.Remove(newPath)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// extractBinary reads a gzip-compressed tar stream and writes the first
// entry named "skoed" (at any path depth) to a temp file in dir.
// Returns the path of the temp file on success.
func extractBinary(r io.Reader, dir string) (string, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return "", fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("tar: %w", err)
		}
		if filepath.Base(hdr.Name) != "skoed" {
			continue
		}
		out, err := os.CreateTemp(dir, "skoed_new_*.tmp")
		if err != nil {
			return "", fmt.Errorf("create temp: %w", err)
		}
		if _, err := io.Copy(out, io.LimitReader(tr, 256<<20)); err != nil {
			out.Close()
			os.Remove(out.Name())
			return "", fmt.Errorf("write: %w", err)
		}
		out.Close()
		if err := os.Chmod(out.Name(), 0755); err != nil {
			os.Remove(out.Name())
			return "", fmt.Errorf("chmod: %w", err)
		}
		return out.Name(), nil
	}
	return "", fmt.Errorf("skoed binary not found in archive")
}

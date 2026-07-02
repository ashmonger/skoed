package upgrade

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
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
//
// The binary is first written to os.TempDir() so that read-only mounts on
// the destination directory (e.g. /usr/bin on some Alpine LXC containers)
// do not prevent extraction. A same-directory atomic rename is then attempted;
// if the temp and target are on different filesystems (EXDEV), we fall back to
// a copy-then-rename inside the destination directory.
func Swap(assetURL, exePath string) error {
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(assetURL) //nolint:noctx
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}

	// Extract to /tmp first so that a read-only /usr/bin doesn't block download.
	tmpPath, err := extractBinary(resp.Body, os.TempDir())
	if err != nil {
		return err
	}
	defer os.Remove(tmpPath)

	// Happy path: same filesystem → atomic rename.
	if err := os.Rename(tmpPath, exePath); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return fmt.Errorf("rename: %w", err)
	}

	// Cross-device: copy into the destination directory, then rename.
	return installCrossDevice(tmpPath, exePath)
}

// installCrossDevice copies src to a temp file beside dst, then renames it
// into place. Both the create and the rename happen on the same filesystem as
// dst, so the rename is still atomic.
func installCrossDevice(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open src: %w", err)
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dst), "skoed_new_*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("copy: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close: %w", err)
	}
	if err := os.Chmod(tmpName, 0755); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("chmod: %w", err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		os.Remove(tmpName)
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

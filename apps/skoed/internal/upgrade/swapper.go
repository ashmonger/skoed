package upgrade

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// maxArchiveBytes bounds how much we download/extract, protecting against a
// hostile or compromised asset server streaming an unbounded body.
const maxArchiveBytes = 256 << 20 // 256 MiB

// AssetKey returns the feed assets map key for the current runtime,
// e.g. "linux_amd64".
func AssetKey() string {
	return runtime.GOOS + "_" + runtime.GOARCH
}

// Swap downloads the tar.gz at assetURL, verifies its SHA-256 against
// expectedSHA256, extracts the "skoed" binary entry, and atomically replaces
// exePath. The caller is responsible for calling os.Exit after the HTTP
// response is flushed.
//
// expectedSHA256 (hex, as produced by goreleaser's checksums.txt) MUST be
// non-empty: swapping the running binary with unverified bytes is remote code
// execution, so an empty checksum is rejected. Callers obtain the checksum
// from the signed release feed (GitHub checksums.txt) or, on the cluster
// rolling-upgrade path, from the leader that already verified it.
//
// The archive is first downloaded to os.TempDir() so that read-only mounts on
// the destination directory (e.g. /usr/bin on some Alpine LXC containers) do
// not prevent extraction. A same-directory atomic rename is then attempted; if
// the temp and target are on different filesystems (EXDEV), we fall back to a
// copy-then-rename inside the destination directory.
func Swap(assetURL, exePath, expectedSHA256 string) error {
	if strings.TrimSpace(expectedSHA256) == "" {
		return fmt.Errorf("refusing unverified upgrade: no SHA-256 checksum supplied for %s", assetURL)
	}

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(assetURL) //nolint:noctx
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}

	// Download the whole archive to a temp file while hashing it, so we can
	// verify integrity BEFORE extracting or installing anything.
	archivePath, sum, err := downloadAndHash(resp.Body, os.TempDir())
	if err != nil {
		return err
	}
	defer os.Remove(archivePath)

	want, err := hex.DecodeString(strings.TrimSpace(expectedSHA256))
	if err != nil || len(want) != sha256.Size {
		return fmt.Errorf("invalid expected SHA-256 %q", expectedSHA256)
	}
	if subtle.ConstantTimeCompare(sum, want) != 1 {
		return fmt.Errorf("checksum mismatch: got %s, want %s", hex.EncodeToString(sum), expectedSHA256)
	}

	archive, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer archive.Close()

	// Extract to /tmp first so that a read-only /usr/bin doesn't block download.
	tmpPath, err := extractBinary(archive, os.TempDir())
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

// downloadAndHash streams r (the archive body) into a bounded temp file in dir
// while computing its SHA-256. Returns the temp file path and the digest.
func downloadAndHash(r io.Reader, dir string) (string, []byte, error) {
	out, err := os.CreateTemp(dir, "skoed_dl_*.tar.gz")
	if err != nil {
		return "", nil, fmt.Errorf("create temp: %w", err)
	}
	// Interim file is owner-only until verified and installed.
	_ = os.Chmod(out.Name(), 0600)
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(r, maxArchiveBytes)); err != nil {
		out.Close()
		os.Remove(out.Name())
		return "", nil, fmt.Errorf("download: %w", err)
	}
	if err := out.Close(); err != nil {
		os.Remove(out.Name())
		return "", nil, fmt.Errorf("close: %w", err)
	}
	return out.Name(), h.Sum(nil), nil
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

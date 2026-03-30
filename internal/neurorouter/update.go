package neurorouter

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"runtime"
	"strings"
)

// UpdateRepo is the GitHub repo to check for releases.
const UpdateRepo = "ppiankov/neurorouter"

// ReleaseInfo holds metadata from a GitHub release.
type ReleaseInfo struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// DownloadedArchive is a verified release archive ready for extraction.
type DownloadedArchive struct {
	Path      string
	AssetName string
	Checksum  string
}

// CheckUpdate queries GitHub for the latest release and compares to current version.
// Returns the release info if an update is available, nil if already latest.
func CheckUpdate(currentVersion string) (*ReleaseInfo, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", UpdateRepo)
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("check update: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release ReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}

	// Compare versions (strip 'v' prefix).
	latest := strings.TrimPrefix(release.TagName, "v")
	current := strings.TrimPrefix(currentVersion, "v")

	if latest == current || latest == "" {
		return nil, nil // already latest
	}

	return &release, nil
}

// AssetName returns the expected asset name for the current platform.
func AssetName(version string) string {
	os := runtime.GOOS
	arch := runtime.GOARCH
	ext := "tar.gz"
	if os == "windows" {
		ext = "zip"
	}
	v := strings.TrimPrefix(version, "v")
	return fmt.Sprintf("neurorouter_%s_%s_%s.%s", v, os, arch, ext)
}

// FindAssetURL returns the download URL for the current platform's binary.
func FindAssetURL(release *ReleaseInfo) (string, error) {
	target := AssetName(release.TagName)
	for _, asset := range release.Assets {
		if asset.Name == target {
			return asset.BrowserDownloadURL, nil
		}
	}
	return "", fmt.Errorf("no binary found for %s/%s in release %s", runtime.GOOS, runtime.GOARCH, release.TagName)
}

// FindChecksumURL returns the download URL for the checksums file.
func FindChecksumURL(release *ReleaseInfo) string {
	for _, asset := range release.Assets {
		if asset.Name == "checksums.txt" {
			return asset.BrowserDownloadURL
		}
	}
	return ""
}

// DownloadUpdate downloads the binary to a temp file and verifies its checksum.
// Returns the path to the downloaded archive.
func DownloadUpdate(assetURL, checksumURL, expectedAssetName string) (*DownloadedArchive, error) {
	if checksumURL == "" {
		return nil, fmt.Errorf("release is missing checksums.txt")
	}

	expectedChecksum, err := fetchExpectedChecksum(checksumURL, expectedAssetName)
	if err != nil {
		return nil, fmt.Errorf("fetch checksum: %w", err)
	}

	// Download binary.
	resp, err := http.Get(assetURL)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("download returned %d", resp.StatusCode)
	}

	tmpFile, err := os.CreateTemp("", "neurorouter-update-*")
	if err != nil {
		return nil, fmt.Errorf("create temp: %w", err)
	}

	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmpFile, hasher), resp.Body); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
		return nil, fmt.Errorf("write: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpFile.Name())
		return nil, fmt.Errorf("close temp archive: %w", err)
	}

	actualChecksum := hex.EncodeToString(hasher.Sum(nil))
	if actualChecksum != expectedChecksum {
		_ = os.Remove(tmpFile.Name())
		return nil, fmt.Errorf("checksum mismatch: expected %s, got %s", shortChecksum(expectedChecksum), shortChecksum(actualChecksum))
	}

	return &DownloadedArchive{
		Path:      tmpFile.Name(),
		AssetName: expectedAssetName,
		Checksum:  actualChecksum,
	}, nil
}

func fetchExpectedChecksum(checksumURL, assetName string) (string, error) {
	resp, err := http.Get(checksumURL)
	if err != nil {
		return "", fmt.Errorf("download checksum: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checksum download returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read checksum: %w", err)
	}

	// Format: <sha256>  <filename>
	for _, line := range strings.Split(string(body), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == assetName {
			return parts[0], nil
		}
	}
	return "", fmt.Errorf("checksum entry for %s not found", assetName)
}

// ExtractBinary extracts the current platform binary from the verified archive.
func ExtractBinary(archivePath, assetName string) (string, error) {
	switch {
	case strings.HasSuffix(assetName, ".tar.gz"):
		return extractBinaryFromTarGz(archivePath)
	case strings.HasSuffix(assetName, ".zip"):
		return extractBinaryFromZip(archivePath)
	default:
		return "", fmt.Errorf("unsupported archive format for %s", assetName)
	}
}

// ReplaceBinary replaces the current binary with the downloaded one.
func ReplaceBinary(binaryPath string) error {
	currentPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find current binary: %w", err)
	}
	return replaceBinaryAt(currentPath, binaryPath)
}

func replaceBinaryAt(currentPath, binaryPath string) error {
	backup := backupPath(currentPath)

	// Rename current to .old backup.
	if err := os.Rename(currentPath, backup); err != nil {
		return fmt.Errorf("backup current: %w", err)
	}

	// Copy new binary into place so temp files on other filesystems still work.
	if err := copyFile(binaryPath, currentPath, 0o755); err != nil {
		_ = os.Remove(currentPath)
		_ = os.Rename(backup, currentPath)
		return fmt.Errorf("install new: %w", err)
	}

	// Make executable.
	if err := os.Chmod(currentPath, 0o755); err != nil {
		_ = os.Remove(currentPath)
		_ = os.Rename(backup, currentPath)
		return fmt.Errorf("chmod new: %w", err)
	}

	// Best-effort cleanup. Keeping the backup is safer than failing after install.
	_ = os.Remove(backup)

	return nil
}

func backupPath(currentPath string) string {
	return currentPath + ".old"
}

func extractBinaryFromTarGz(archivePath string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("open archive: %w", err)
	}
	defer func() { _ = f.Close() }()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("open gzip: %w", err)
	}
	defer func() { _ = gzr.Close() }()

	tr := tar.NewReader(gzr)
	return extractBinaryFromTarReader(tr)
}

func extractBinaryFromTarReader(tr *tar.Reader) (string, error) {
	target := platformBinaryName()
	var extracted string

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read tar entry: %w", err)
		}
		if !hdr.FileInfo().Mode().IsRegular() {
			continue
		}
		if path.Base(hdr.Name) != target {
			continue
		}
		if extracted != "" {
			return "", fmt.Errorf("archive contains multiple %s binaries", target)
		}
		extracted, err = writeExtractedBinary(tr)
		if err != nil {
			return "", err
		}
	}

	if extracted == "" {
		return "", fmt.Errorf("binary %s not found in archive", target)
	}
	return extracted, nil
}

func extractBinaryFromZip(archivePath string) (string, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", fmt.Errorf("open zip: %w", err)
	}
	defer func() { _ = zr.Close() }()

	target := platformBinaryName()
	var extracted string

	for _, file := range zr.File {
		if !file.FileInfo().Mode().IsRegular() {
			continue
		}
		if path.Base(file.Name) != target {
			continue
		}
		if extracted != "" {
			return "", fmt.Errorf("archive contains multiple %s binaries", target)
		}

		rc, err := file.Open()
		if err != nil {
			return "", fmt.Errorf("open zip entry: %w", err)
		}
		extracted, err = writeExtractedBinary(rc)
		_ = rc.Close()
		if err != nil {
			return "", err
		}
	}

	if extracted == "" {
		return "", fmt.Errorf("binary %s not found in archive", target)
	}
	return extracted, nil
}

func writeExtractedBinary(r io.Reader) (string, error) {
	tmp, err := os.CreateTemp("", "neurorouter-binary-*")
	if err != nil {
		return "", fmt.Errorf("create extracted binary temp file: %w", err)
	}

	if _, err := io.Copy(tmp, r); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("extract binary: %w", err)
	}

	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("chmod extracted binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("close extracted binary: %w", err)
	}

	return tmp.Name(), nil
}

func copyFile(srcPath, dstPath string, mode os.FileMode) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer func() { _ = src.Close() }()

	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("open destination: %w", err)
	}

	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return fmt.Errorf("copy file: %w", err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("close destination: %w", err)
	}

	return nil
}

func platformBinaryName() string {
	if runtime.GOOS == "windows" {
		return "neurorouter.exe"
	}
	return "neurorouter"
}

func shortChecksum(sum string) string {
	if len(sum) <= 16 {
		return sum
	}
	return sum[:16] + "..."
}

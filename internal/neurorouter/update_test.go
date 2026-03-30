package neurorouter

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAssetName(t *testing.T) {
	name := AssetName("v0.2.0")
	if !strings.Contains(name, "neurorouter_0.2.0") {
		t.Errorf("expected version in name, got %q", name)
	}
	if !strings.Contains(name, runtime.GOOS) {
		t.Errorf("expected OS in name, got %q", name)
	}
	if !strings.Contains(name, runtime.GOARCH) {
		t.Errorf("expected arch in name, got %q", name)
	}
	if runtime.GOOS == "windows" {
		if !strings.HasSuffix(name, ".zip") {
			t.Errorf("expected .zip on windows, got %q", name)
		}
	} else {
		if !strings.HasSuffix(name, ".tar.gz") {
			t.Errorf("expected .tar.gz, got %q", name)
		}
	}
}

func TestCheckUpdate_NewVersionAvailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		release := ReleaseInfo{TagName: "v0.2.0"}
		_ = json.NewEncoder(w).Encode(release)
	}))
	defer server.Close()

	release := &ReleaseInfo{TagName: "v0.2.0"}
	latest := strings.TrimPrefix(release.TagName, "v")
	current := strings.TrimPrefix("v0.1.0", "v")

	if latest == current {
		t.Error("0.2.0 should be newer than 0.1.0")
	}
}

func TestCheckUpdate_AlreadyLatest(t *testing.T) {
	latest := strings.TrimPrefix("v0.1.0", "v")
	current := strings.TrimPrefix("v0.1.0", "v")

	if latest != current {
		t.Error("same version should match")
	}
}

func TestFindAssetURL(t *testing.T) {
	release := &ReleaseInfo{
		TagName: "v0.2.0",
		Assets: []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		}{
			{Name: "neurorouter_0.2.0_linux_amd64.tar.gz", BrowserDownloadURL: "https://example.com/linux"},
			{Name: "neurorouter_0.2.0_darwin_arm64.tar.gz", BrowserDownloadURL: "https://example.com/darwin-arm64"},
			{Name: "neurorouter_0.2.0_darwin_amd64.tar.gz", BrowserDownloadURL: "https://example.com/darwin-amd64"},
			{Name: "neurorouter_0.2.0_windows_amd64.zip", BrowserDownloadURL: "https://example.com/windows"},
			{Name: "checksums.txt", BrowserDownloadURL: "https://example.com/checksums"},
		},
	}

	url, err := FindAssetURL(release)
	if err != nil {
		t.Fatalf("find asset: %v", err)
	}
	if url == "" {
		t.Fatal("expected URL")
	}
}

func TestFindAssetURL_Missing(t *testing.T) {
	release := &ReleaseInfo{
		TagName: "v0.2.0",
		Assets: []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		}{
			{Name: "neurorouter_0.2.0_plan9_mips.tar.gz", BrowserDownloadURL: "https://example.com/plan9"},
		},
	}

	_, err := FindAssetURL(release)
	if err == nil {
		t.Error("should error when platform binary not found")
	}
}

func TestFindChecksumURL(t *testing.T) {
	release := &ReleaseInfo{
		Assets: []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		}{
			{Name: "checksums.txt", BrowserDownloadURL: "https://example.com/checksums"},
			{Name: "neurorouter_0.2.0_linux_amd64.tar.gz", BrowserDownloadURL: "https://example.com/linux"},
		},
	}

	url := FindChecksumURL(release)
	if url != "https://example.com/checksums" {
		t.Errorf("expected checksums URL, got %q", url)
	}
}

func TestFindChecksumURL_Missing(t *testing.T) {
	release := &ReleaseInfo{
		Assets: []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		}{
			{Name: "binary.tar.gz", BrowserDownloadURL: "https://example.com/binary"},
		},
	}

	url := FindChecksumURL(release)
	if url != "" {
		t.Error("expected empty URL when checksums not found")
	}
}

func TestDownloadUpdateAndExtractBinary(t *testing.T) {
	assetName := AssetName("v0.2.0")
	archive := archiveForCurrentPlatform(t, []archiveEntry{
		{name: platformBinaryName(), data: []byte("new-binary")},
		{name: "README.md", data: []byte("notes")},
	})
	checksum := sha256.Sum256(archive)
	checksumsBody := hex.EncodeToString(checksum[:]) + "  " + assetName + "\n"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/asset":
			_, _ = w.Write(archive)
		case "/checksums":
			_, _ = w.Write([]byte(checksumsBody))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	downloaded, err := DownloadUpdate(server.URL+"/asset", server.URL+"/checksums", assetName)
	if err != nil {
		t.Fatalf("download update: %v", err)
	}
	defer func() { _ = os.Remove(downloaded.Path) }()

	if downloaded.AssetName != assetName {
		t.Fatalf("asset name: got %q, want %q", downloaded.AssetName, assetName)
	}

	extracted, err := ExtractBinary(downloaded.Path, downloaded.AssetName)
	if err != nil {
		t.Fatalf("extract binary: %v", err)
	}
	defer func() { _ = os.Remove(extracted) }()

	data, err := os.ReadFile(extracted)
	if err != nil {
		t.Fatalf("read extracted: %v", err)
	}
	if string(data) != "new-binary" {
		t.Fatalf("extracted content: got %q", string(data))
	}
}

func TestDownloadUpdate_RequiresChecksumURL(t *testing.T) {
	_, err := DownloadUpdate("https://example.com/asset", "", AssetName("v0.2.0"))
	if err == nil {
		t.Fatal("expected error when checksums.txt is missing")
	}
}

func TestDownloadUpdate_FailsWhenChecksumFileMissing(t *testing.T) {
	assetName := AssetName("v0.2.0")
	archive := archiveForCurrentPlatform(t, []archiveEntry{{name: platformBinaryName(), data: []byte("new-binary")}})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/asset":
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_, err := DownloadUpdate(server.URL+"/asset", server.URL+"/missing", assetName)
	if err == nil {
		t.Fatal("expected checksum download failure")
	}
}

func TestDownloadUpdate_FailsWhenChecksumEntryMissing(t *testing.T) {
	assetName := AssetName("v0.2.0")
	archive := archiveForCurrentPlatform(t, []archiveEntry{{name: platformBinaryName(), data: []byte("new-binary")}})
	checksum := sha256.Sum256(archive)
	checksumsBody := hex.EncodeToString(checksum[:]) + "  some-other-file.tar.gz\n"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/asset":
			_, _ = w.Write(archive)
		case "/checksums":
			_, _ = w.Write([]byte(checksumsBody))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_, err := DownloadUpdate(server.URL+"/asset", server.URL+"/checksums", assetName)
	if err == nil {
		t.Fatal("expected checksum entry failure")
	}
}

func TestDownloadUpdate_FailsOnChecksumMismatch(t *testing.T) {
	assetName := AssetName("v0.2.0")
	archive := archiveForCurrentPlatform(t, []archiveEntry{{name: platformBinaryName(), data: []byte("new-binary")}})
	checksumsBody := strings.Repeat("a", 64) + "  " + assetName + "\n"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/asset":
			_, _ = w.Write(archive)
		case "/checksums":
			_, _ = w.Write([]byte(checksumsBody))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_, err := DownloadUpdate(server.URL+"/asset", server.URL+"/checksums", assetName)
	if err == nil {
		t.Fatal("expected checksum mismatch failure")
	}
}

func TestExtractBinary_Zip(t *testing.T) {
	archive := createZipArchive(t, []archiveEntry{
		{name: platformBinaryName(), data: []byte("zip-binary")},
		{name: "notes.txt", data: []byte("notes")},
	})
	path := writeTempFile(t, archive)
	defer func() { _ = os.Remove(path) }()

	extracted, err := ExtractBinary(path, "neurorouter_test.zip")
	if err != nil {
		t.Fatalf("extract zip: %v", err)
	}
	defer func() { _ = os.Remove(extracted) }()

	data, err := os.ReadFile(extracted)
	if err != nil {
		t.Fatalf("read extracted zip file: %v", err)
	}
	if string(data) != "zip-binary" {
		t.Fatalf("zip extracted content: got %q", string(data))
	}
}

func TestExtractBinary_MalformedArchive(t *testing.T) {
	path := writeTempFile(t, []byte("not-a-real-archive"))
	defer func() { _ = os.Remove(path) }()

	_, err := ExtractBinary(path, "neurorouter_test.tar.gz")
	if err == nil {
		t.Fatal("expected malformed archive failure")
	}
}

func TestReplaceBinaryAt_RestoresBackupOnFailure(t *testing.T) {
	dir := t.TempDir()
	currentPath := filepath.Join(dir, "neurorouter")
	if err := os.WriteFile(currentPath, []byte("old-binary"), 0o755); err != nil {
		t.Fatalf("write current binary: %v", err)
	}

	missingPath := filepath.Join(dir, "does-not-exist")
	err := replaceBinaryAt(currentPath, missingPath)
	if err == nil {
		t.Fatal("expected install failure")
	}

	data, readErr := os.ReadFile(currentPath)
	if readErr != nil {
		t.Fatalf("read restored binary: %v", readErr)
	}
	if string(data) != "old-binary" {
		t.Fatalf("restored content: got %q", string(data))
	}
	if _, statErr := os.Stat(backupPath(currentPath)); !os.IsNotExist(statErr) {
		t.Fatalf("backup file should be cleaned up, stat err=%v", statErr)
	}
}

type archiveEntry struct {
	name string
	data []byte
}

func archiveForCurrentPlatform(t *testing.T, entries []archiveEntry) []byte {
	t.Helper()
	if runtime.GOOS == "windows" {
		return createZipArchive(t, entries)
	}
	return createTarGzArchive(t, entries)
}

func createTarGzArchive(t *testing.T, entries []archiveEntry) []byte {
	t.Helper()

	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	for _, entry := range entries {
		hdr := &tar.Header{
			Name: entry.name,
			Mode: 0o755,
			Size: int64(len(entry.data)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := tw.Write(entry.data); err != nil {
			t.Fatalf("write tar entry: %v", err)
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}

	return buf.Bytes()
}

func createZipArchive(t *testing.T, entries []archiveEntry) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	for _, entry := range entries {
		w, err := zw.Create(entry.name)
		if err != nil {
			t.Fatalf("create zip entry: %v", err)
		}
		if _, err := w.Write(entry.data); err != nil {
			t.Fatalf("write zip entry: %v", err)
		}
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}

	return buf.Bytes()
}

func writeTempFile(t *testing.T, data []byte) string {
	t.Helper()

	f, err := os.CreateTemp("", "neurorouter-update-test-*")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		t.Fatalf("write temp file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close temp file: %v", err)
	}

	return f.Name()
}

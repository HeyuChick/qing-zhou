package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRefreshHostedProbesSkipsWhenUnset(t *testing.T) {
	t.Setenv("QZ_PROBE_DIR", "")
	m := New(nil, nil)
	rel := &ghRelease{TagName: "v9.9.9"}
	if got := refreshHostedProbes(context.Background(), m, rel, "v9.9.9"); got != "" {
		t.Fatalf("unset dir note = %q", got)
	}
}

func TestRefreshHostedProbesInstallsMatchingAssets(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QZ_PROBE_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "probe-linux-amd64"), []byte("old-amd64"), 0o755); err != nil {
		t.Fatal(err)
	}

	amd := []byte("new-amd64-probe")
	arm := []byte("new-arm64-probe")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/probe-linux-amd64":
			_, _ = w.Write(amd)
		case "/probe-linux-arm64":
			_, _ = w.Write(arm)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	rel := &ghRelease{
		TagName: "v0.2.41",
		Assets: []ghAsset{
			{Name: "probe-linux-amd64", BrowserDownloadURL: srv.URL + "/probe-linux-amd64", Size: int64(len(amd)), Digest: "sha256:" + shaHex(amd)},
			{Name: "probe-linux-arm64", BrowserDownloadURL: srv.URL + "/probe-linux-arm64", Size: int64(len(arm)), Digest: "sha256:" + shaHex(arm)},
		},
	}
	m := New(nil, nil)
	m.client = srv.Client()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	note := refreshHostedProbes(ctx, m, rel, "v0.2.41")
	if !strings.Contains(note, "已刷新探针") || strings.Contains(note, "未刷新") {
		t.Fatalf("note = %q", note)
	}
	if got := readFile(t, filepath.Join(dir, "probe-linux-amd64")); got != string(amd) {
		t.Fatalf("amd64 = %q", got)
	}
	if got := readFile(t, filepath.Join(dir, "probe-linux-arm64")); got != string(arm) {
		t.Fatalf("arm64 = %q", got)
	}
}

func TestRefreshHostedProbesNotesMissingAssets(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QZ_PROBE_DIR", dir)
	m := New(nil, nil)
	rel := &ghRelease{TagName: "v1.0.0"}
	note := refreshHostedProbes(context.Background(), m, rel, "v1.0.0")
	if !strings.Contains(note, "probe-linux-amd64 缺失") || !strings.Contains(note, "probe-linux-arm64 缺失") {
		t.Fatalf("missing-asset note = %q", note)
	}
}

func TestRefreshHostedProbesSkipsMatchingDigest(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QZ_PROBE_DIR", dir)
	amd := []byte("already-amd64")
	arm := []byte("already-arm64")
	if err := os.WriteFile(filepath.Join(dir, "probe-linux-amd64"), amd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "probe-linux-arm64"), arm, 0o755); err != nil {
		t.Fatal(err)
	}
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Error(w, "should not download", 500)
	}))
	t.Cleanup(srv.Close)
	rel := &ghRelease{
		TagName: "v0.2.72",
		Assets: []ghAsset{
			{Name: "probe-linux-amd64", BrowserDownloadURL: srv.URL + "/probe-linux-amd64", Size: int64(len(amd)), Digest: "sha256:" + shaHex(amd)},
			{Name: "probe-linux-arm64", BrowserDownloadURL: srv.URL + "/probe-linux-arm64", Size: int64(len(arm)), Digest: "sha256:" + shaHex(arm)},
		},
	}
	m := New(nil, nil)
	m.client = srv.Client()
	if got := refreshHostedProbes(context.Background(), m, rel, "v0.2.72"); got != "" {
		t.Fatalf("current probes should be silent, got %q", got)
	}
	if hits != 0 {
		t.Fatalf("downloaded %d times, want 0", hits)
	}
}

func TestSyncHostedProbesNoopsOnDev(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QZ_PROBE_DIR", dir)
	m := New(nil, nil)
	if got := m.SyncHostedProbes(context.Background()); got != "" {
		t.Fatalf("dev sync note = %q", got)
	}
}

func TestInstallHostedProbeRejectsBadDigest(t *testing.T) {
	dir := t.TempDir()
	body := []byte("probe-bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	m := New(nil, nil)
	m.client = srv.Client()
	asset := &ghAsset{
		Name:               "probe-linux-amd64",
		BrowserDownloadURL: srv.URL + "/probe-linux-amd64",
		Size:               int64(len(body)),
		Digest:             "sha256:" + strings.Repeat("ab", 32),
	}
	dst := filepath.Join(dir, "probe-linux-amd64")
	if err := installHostedProbe(context.Background(), m, asset, dst); err == nil {
		t.Fatal("expected digest mismatch")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("failed install left a destination file: %v", err)
	}
}

func shaHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

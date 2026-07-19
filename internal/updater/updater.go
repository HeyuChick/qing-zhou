// Package updater implements in-panel self-update against GitHub Releases.
//
// It queries the repo's latest release, compares the tag against the running
// binary's version (see internal/version), and — on admin request — downloads
// the matching Linux asset, verifies its sha256 against the digest GitHub
// publishes for the asset, atomically replaces the running binary, and re-execs
// the process so the new version takes over (same PID; works with systemd's
// Restart=on-failure without needing the unit name or extra privileges).
//
// The binary swap + re-exec is Linux-only (see restart_linux.go); on other
// platforms Apply refuses with a clear message. Checking for updates works
// everywhere.
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"qingzhou/internal/version"
)

// DefaultRepo is the GitHub "owner/name" polled when no override is configured.
const DefaultRepo = "mllt992/qing-zhou"

// assetName is the release asset this binary knows how to install for the
// current OS/arch, e.g. "qingzhou-linux-amd64".
func assetName() string {
	return fmt.Sprintf("qingzhou-%s-%s", runtime.GOOS, runtime.GOARCH)
}

// Status is a coarse phase of an in-flight update.
type Status string

const (
	StatusIdle        Status = "idle"
	StatusDownloading Status = "downloading"
	StatusVerifying   Status = "verifying"
	StatusInstalling  Status = "installing"
	StatusRestarting  Status = "restarting"
	StatusFailed      Status = "failed"
)

// State is the observable progress of the updater, returned by the status API.
type State struct {
	Status        Status `json:"status"`
	Message       string `json:"message"`
	Percent       int    `json:"percent"`
	TargetVersion string `json:"target_version"`
	StartedAt     int64  `json:"started_at,omitempty"`
}

// CheckResult is what the check API returns to the admin UI.
type CheckResult struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	Name            string `json:"name"`
	Notes           string `json:"notes"`
	URL             string `json:"url"`
	PublishedAt     string `json:"published_at"`
	UpdateAvailable bool   `json:"update_available"`
	Downloadable    bool   `json:"downloadable"`
	AssetName       string `json:"asset_name"`
	AssetSize       int64  `json:"asset_size"`
	Dev             bool   `json:"dev"`
}

// Manager coordinates update checks and the (single-flight) apply operation.
type Manager struct {
	repoFn  func() string // resolves the configured "owner/name" (env/setting/default)
	tokenFn func() string // optional GitHub token to lift the 60/hr anon rate limit
	client  *http.Client

	mu      sync.Mutex
	state   State
	running bool
}

// New builds a Manager. repoFn/tokenFn may be nil; sensible defaults are used.
func New(repoFn, tokenFn func() string) *Manager {
	if repoFn == nil {
		repoFn = func() string { return DefaultRepo }
	}
	if tokenFn == nil {
		tokenFn = func() string { return "" }
	}
	return &Manager{
		repoFn:  repoFn,
		tokenFn: tokenFn,
		// No overall client timeout: a release binary is tens of MB and the
		// download is bounded by the caller's context instead.
		client: &http.Client{},
		state:  State{Status: StatusIdle},
	}
}

func (m *Manager) repo() string {
	r := strings.TrimSpace(m.repoFn())
	if r == "" {
		return DefaultRepo
	}
	return r
}

// ghRelease mirrors the subset of the GitHub releases API we consume.
type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt string    `json:"published_at"`
	Assets      []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	Digest             string `json:"digest"` // e.g. "sha256:abcd..."
}

func (m *Manager) newRequest(ctx context.Context, url string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "qingzhou-updater")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if tok := strings.TrimSpace(m.tokenFn()); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return req, nil
}

// latestRelease fetches the newest non-draft release for the configured repo.
func (m *Manager) latestRelease(ctx context.Context) (*ghRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", m.repo())
	req, err := m.newRequest(ctx, url)
	if err != nil {
		return nil, err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 GitHub 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.New("未找到任何发布版本（仓库无 release 或名称配置有误）")
	}
	if resp.StatusCode == http.StatusForbidden {
		return nil, errors.New("GitHub API 速率受限，请稍后再试（或配置 update_github_token）")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("GitHub 返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var rel ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("解析 GitHub 响应失败: %w", err)
	}
	return &rel, nil
}

func findAsset(rel *ghRelease, name string) *ghAsset {
	for i := range rel.Assets {
		if rel.Assets[i].Name == name {
			return &rel.Assets[i]
		}
	}
	return nil
}

// Check queries the latest release and reports whether an update is available.
func (m *Manager) Check(ctx context.Context) (*CheckResult, error) {
	rel, err := m.latestRelease(ctx)
	if err != nil {
		return nil, err
	}
	cur := version.Current()
	dev := version.IsDev()
	asset := findAsset(rel, assetName())

	res := &CheckResult{
		Current:     cur,
		Latest:      rel.TagName,
		Name:        rel.Name,
		Notes:       rel.Body,
		URL:         rel.HTMLURL,
		PublishedAt: rel.PublishedAt,
		AssetName:   assetName(),
		Dev:         dev,
	}
	if asset != nil {
		res.Downloadable = true
		res.AssetSize = asset.Size
	}
	// A dev build has no comparable version — surface the latest as installable
	// so operators can bootstrap onto a tagged build. Otherwise compare semver.
	if dev {
		res.UpdateAvailable = true
	} else {
		res.UpdateAvailable = version.Compare(rel.TagName, cur) > 0
	}
	return res, nil
}

// State returns a snapshot of the current updater progress.
func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *Manager) setState(s Status, msg string, pct int, target string) {
	m.mu.Lock()
	m.state.Status = s
	m.state.Message = msg
	if pct >= 0 {
		m.state.Percent = pct
	}
	if target != "" {
		m.state.TargetVersion = target
	}
	m.mu.Unlock()
}

// Apply kicks off a self-update in the background. It returns immediately; the
// caller polls State() for progress. Returns an error if the platform is
// unsupported or an update is already running.
func (m *Manager) Apply(nowUnix int64) error {
	if runtime.GOOS != "linux" {
		return errors.New("自更新仅支持 Linux 部署；请在服务器上手动替换二进制")
	}
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return errors.New("已有更新任务在进行中")
	}
	m.running = true
	m.state = State{Status: StatusDownloading, Message: "准备下载…", Percent: 0, StartedAt: nowUnix}
	m.mu.Unlock()

	go m.run()
	return nil
}

func (m *Manager) fail(msg string) {
	m.setState(StatusFailed, msg, -1, "")
	m.mu.Lock()
	m.running = false
	m.mu.Unlock()
}

// run performs the full download → verify → swap → re-exec sequence. On success
// it never returns (the process image is replaced); any failure lands in
// StatusFailed with a message the UI shows.
func (m *Manager) run() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	rel, err := m.latestRelease(ctx)
	if err != nil {
		m.fail("获取版本信息失败: " + err.Error())
		return
	}
	target := rel.TagName

	// Refuse anything that isn't newer. run() re-fetches the release rather than
	// carrying the one Check() showed, so without this an admin could be shown
	// vX and served whatever /releases/latest returns at apply time — including
	// an older, known-vulnerable build, which installed silently.
	if !isNewer(target, version.Current(), version.IsDev()) {
		m.fail(fmt.Sprintf("最新版本 %s 不比当前版本 %s 新，已取消更新", target, version.Current()))
		return
	}
	m.setState(StatusDownloading, "开始下载 "+target, 0, target)

	asset := findAsset(rel, assetName())
	if asset == nil {
		m.fail(fmt.Sprintf("该版本未提供适配当前架构的资产 (%s)", assetName()))
		return
	}
	wantDigest := parseSHA256(asset.Digest)
	if wantDigest == "" {
		m.fail("该版本资产缺少 sha256 摘要，出于安全考虑拒绝更新")
		return
	}
	// Fetch the detached signature before spending bandwidth on the binary.
	// Fail-closed: with a key compiled in, a release without a usable signature
	// is refused rather than silently falling back to the digest alone.
	var sig []byte
	if signingEnabled() {
		sigAsset := findAsset(rel, signatureAssetName(assetName()))
		if sigAsset == nil {
			m.fail(fmt.Sprintf("该版本缺少签名资产 (%s)，出于安全考虑拒绝更新", signatureAssetName(assetName())))
			return
		}
		if sig, err = m.fetchSignature(ctx, sigAsset.BrowserDownloadURL); err != nil {
			m.fail("下载签名失败: " + err.Error())
			return
		}
	}

	exePath, err := os.Executable()
	if err != nil {
		m.fail("无法定位当前程序路径: " + err.Error())
		return
	}
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}

	// Download into a sibling temp file so the final rename is atomic (same fs).
	tmpPath := exePath + ".new"
	sum, err := m.download(ctx, asset.BrowserDownloadURL, tmpPath, asset.Size)
	if err != nil {
		_ = os.Remove(tmpPath)
		m.fail("下载失败: " + err.Error())
		return
	}

	m.setState(StatusVerifying, "校验完整性…", 100, target)
	if !strings.EqualFold(sum, wantDigest) {
		_ = os.Remove(tmpPath)
		m.fail("sha256 校验不匹配，已丢弃下载内容（可能被篡改或下载损坏）")
		return
	}
	// The digest proves the transfer was intact; only the signature proves the
	// project produced these bytes.
	if signingEnabled() {
		m.setState(StatusVerifying, "校验发布签名…", 100, target)
		body, rerr := os.ReadFile(tmpPath)
		if rerr != nil {
			_ = os.Remove(tmpPath)
			m.fail("读取下载内容失败: " + rerr.Error())
			return
		}
		if verr := verifySignature(body, sig); verr != nil {
			_ = os.Remove(tmpPath)
			m.fail("发布签名校验失败，已丢弃下载内容: " + verr.Error())
			return
		}
	}

	m.setState(StatusInstalling, "安装新版本…", 100, target)
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		_ = os.Remove(tmpPath)
		m.fail("设置可执行权限失败: " + err.Error())
		return
	}
	// Keep the outgoing binary so a release that cannot start can be rolled
	// back. This deployment updates only through this feature and has no SSH,
	// so without a local copy a bad release means the panel is unreachable and
	// there is nothing left on disk to fall back to.
	prev := backupPath(exePath)
	_ = os.Remove(prev)
	if err := os.Link(exePath, prev); err != nil {
		// Hard links can fail (e.g. /proc-backed or cross-device layouts); copy.
		if cerr := copyFile(exePath, prev); cerr != nil {
			_ = os.Remove(tmpPath)
			m.fail("备份当前版本失败，已取消更新: " + cerr.Error())
			return
		}
	}
	// rename() over the running binary is safe on Linux (no ETXTBSY; only
	// *writing* a busy text file fails — the probe installer relies on the same).
	if err := os.Rename(tmpPath, exePath); err != nil {
		_ = os.Remove(tmpPath)
		m.fail("替换二进制失败: " + err.Error())
		return
	}

	m.setState(StatusRestarting, "更新完成，正在重启服务…", 100, target)
	// Give the HTTP layer a beat to flush any in-flight status poll, then
	// re-exec. On success this call does not return.
	time.Sleep(600 * time.Millisecond)
	if err := restartSelf(exePath); err != nil {
		// The new binary is already installed but this process could not hand
		// over to it. Put the old one back: leaving the new binary on disk means
		// the next restart — whenever that happens — silently jumps versions to
		// something that has already failed to start once.
		msg := "重启失败: " + err.Error()
		if rerr := restoreBackup(exePath); rerr != nil {
			msg += "；回滚也失败了（" + rerr.Error() + "），请手动恢复"
		} else {
			msg += "；已回滚到更新前的版本，请手动重启服务"
		}
		m.fail(msg)
		return
	}
}

// copyFile is the fallback for backing up the running binary when a hard link
// isn't possible.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// fetchSignature retrieves a detached signature asset into memory.
func (m *Manager) fetchSignature(ctx context.Context, url string) ([]byte, error) {
	req, err := m.newRequest(ctx, url)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxSignatureBytes))
}

// download streams url into dst, returning the lowercase hex sha256 of the body
// and updating progress from the known content length.
func (m *Manager) download(ctx context.Context, url, dst string, size int64) (string, error) {
	req, err := m.newRequest(ctx, url)
	if err != nil {
		return "", err
	}
	// Asset downloads are octet-streams, not the JSON API media type.
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := m.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > 0 {
		size = resp.ContentLength
	}

	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	pw := &progressWriter{m: m, total: size}
	// Bounded: see maxAssetBytes. Read one byte past the cap so an oversized
	// body is reported as such instead of silently truncating into a digest
	// mismatch (a confusing "tampered" message for what is really a bad repo).
	n, err := io.Copy(io.MultiWriter(f, h, pw), io.LimitReader(resp.Body, maxAssetBytes+1))
	if err != nil {
		return "", err
	}
	if n > maxAssetBytes {
		return "", fmt.Errorf("资产超过 %d MiB 上限，已中止下载", maxAssetBytes>>20)
	}
	if err := f.Sync(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// progressWriter updates the manager's download percentage as bytes flow.
type progressWriter struct {
	m       *Manager
	total   int64
	written int64
	lastPct int
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n := len(b)
	p.written += int64(n)
	if p.total > 0 {
		pct := int(p.written * 100 / p.total)
		if pct > 100 {
			pct = 100
		}
		if pct != p.lastPct {
			p.lastPct = pct
			p.m.setState(StatusDownloading, "", pct, "")
		}
	}
	return n, nil
}

// parseSHA256 extracts the hex digest from a "sha256:<hex>" string (case- and
// prefix-tolerant), returning "" if it isn't a sha256 digest.
func parseSHA256(d string) string {
	d = strings.TrimSpace(d)
	if d == "" {
		return ""
	}
	if i := strings.IndexByte(d, ':'); i >= 0 {
		if !strings.EqualFold(d[:i], "sha256") {
			return ""
		}
		d = d[i+1:]
	}
	if len(d) != 64 {
		return ""
	}
	return strings.ToLower(d)
}

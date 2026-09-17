package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"qingzhou/internal/version"
)

type RecoverySource struct {
	Path     string `json:"path"`
	Optional bool   `json:"optional,omitempty"`
}

type RecoverySpec struct {
	Repository     string           `json:"repository"`
	SingBoxVersion string           `json:"sing_box_version"`
	Sources        []RecoverySource `json:"sources"`
}

type RecoveryInfo struct {
	Enabled   bool             `json:"enabled"`
	Format    string           `json:"format"`
	Encrypted bool             `json:"encrypted"`
	Sources   []RecoverySource `json:"sources,omitempty"`
	Error     string           `json:"error,omitempty"`
}

type recoveryEntry struct {
	Name        string `json:"name"`
	Destination string `json:"destination"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	Mode        uint32 `json:"mode"`
}

type recoveryManifest struct {
	SchemaVersion   int             `json:"schema_version"`
	CreatedAt       string          `json:"created_at"`
	PanelVersion    string          `json:"panel_version"`
	Repository      string          `json:"repository"`
	SingBoxVersion  string          `json:"sing_box_version"`
	OS              string          `json:"os"`
	Arch            string          `json:"arch"`
	Encrypted       bool            `json:"encrypted"`
	Files           []recoveryEntry `json:"files"`
	MissingOptional []string        `json:"missing_optional,omitempty"`
}

func loadRecoverySpec() (*RecoverySpec, error) {
	filename := os.Getenv("QZ_BACKUP_MANIFEST")
	if filename == "" {
		return nil, nil
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("读取灾备清单失败: %w", err)
	}
	var spec RecoverySpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("灾备清单格式错误: %w", err)
	}
	if len(spec.Sources) == 0 {
		return nil, fmt.Errorf("灾备清单 sources 不能为空")
	}
	for _, source := range spec.Sources {
		if !filepath.IsAbs(source.Path) || filepath.Clean(source.Path) != source.Path || source.Path == "/" {
			return nil, fmt.Errorf("灾备路径必须为规范的绝对路径，不能为根目录")
		}
	}
	return &spec, nil
}

func (m *Manager) RecoveryInfo() RecoveryInfo {
	info := RecoveryInfo{Format: "sqlite"}
	spec, err := loadRecoverySpec()
	if err != nil {
		info.Error = err.Error()
		return info
	}
	if spec != nil {
		info.Enabled = true
		info.Format = "tar.gz"
		info.Sources = spec.Sources
	}
	return info
}

func (m *Manager) createRecoveryArchive(ctx context.Context, spec *RecoverySpec, snapshot, destination string) error {
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	compressed := gzip.NewWriter(file)
	archive := tar.NewWriter(compressed)
	complete := false
	defer func() {
		if !complete {
			_ = archive.Close()
			_ = compressed.Close()
			_ = file.Close()
		}
	}()
	manifest := recoveryManifest{SchemaVersion: 1, CreatedAt: time.Now().UTC().Format(time.RFC3339), PanelVersion: version.Current(), Repository: spec.Repository, SingBoxVersion: spec.SingBoxVersion, OS: runtime.GOOS, Arch: runtime.GOARCH}
	seen := map[string]bool{}
	var total int64
	add := func(source, name, target string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if seen[name] {
			return nil
		}
		info, err := os.Lstat(source)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("灾备拒绝符号链接或特殊文件: %s", source)
		}
		resolved, err := filepath.EvalSymlinks(source)
		if err != nil {
			return err
		}
		absolute, err := filepath.Abs(source)
		if err != nil {
			return err
		}
		if source != snapshot && resolved != absolute {
			return fmt.Errorf("灾备路径包含符号链接: %s", source)
		}
		total += info.Size()
		if total > 256<<20 || len(seen) >= 10000 {
			return fmt.Errorf("灾备超出 256 MiB 或 10000 个文件限制")
		}
		input, err := os.Open(source)
		if err != nil {
			return err
		}
		defer input.Close()
		opened, err := input.Stat()
		if err != nil {
			return err
		}
		if !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
			return fmt.Errorf("灾备期间文件被替换: %s", source)
		}
		if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: info.Size(), ModTime: info.ModTime(), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		digest := sha256.New()
		if _, err := io.CopyN(io.MultiWriter(archive, digest), input, info.Size()); err != nil {
			return err
		}
		after, err := input.Stat()
		if err != nil {
			return err
		}
		if after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
			return fmt.Errorf("灾备期间文件发生变化，请重试: %s", source)
		}
		seen[name] = true
		manifest.Files = append(manifest.Files, recoveryEntry{Name: name, Destination: target, Size: info.Size(), SHA256: hex.EncodeToString(digest.Sum(nil)), Mode: uint32(info.Mode().Perm())})
		return nil
	}
	if err := add(snapshot, "database/qingzhou.db", m.st.Path()); err != nil {
		return err
	}
	for _, source := range spec.Sources {
		if _, err := os.Lstat(source.Path); os.IsNotExist(err) && source.Optional {
			manifest.MissingOptional = append(manifest.MissingOptional, source.Path)
			continue
		}
		err := filepath.WalkDir(source.Path, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			if path == m.st.Path() || strings.HasSuffix(path, ".db") || strings.HasSuffix(path, ".db-wal") || strings.HasSuffix(path, ".db-shm") {
				return fmt.Errorf("不能直接打包在线数据库，请缩小灾备路径范围: %s", path)
			}
			return add(path, "files/"+strings.TrimPrefix(filepath.ToSlash(path), "/"), path)
		})
		if err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	for _, document := range []struct{ name, content string }{{"manifest.json", string(data)}, {"RESTORE.md", recoveryInstructions}} {
		if err := archive.WriteHeader(&tar.Header{Name: document.name, Mode: 0600, Size: int64(len(document.content)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		if _, err := io.WriteString(archive, document.content); err != nil {
			return err
		}
	}
	if err := archive.Close(); err != nil {
		return err
	}
	if err := compressed.Close(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	complete = true
	return nil
}

const recoveryInstructions = `# QingZhou 灾备恢复

此包未额外加密，包含数据库、环境变量和可能的私钥。仅存入私有桶；禁止公开或提交 Git。

1. 在隔离目录以普通用户解包，不要直接向 / 解压，不要运行包内环境文件。
2. 核对外部记录中的整包 SHA-256，再按 manifest.json 校验每个文件的 SHA-256 和大小。
3. 用 manifest.json 的 repository、panel_version 获取匹配源码和构建版本；安装对应架构及功能的 sing-box。源码和二进制不在包中。
4. 在目标服务器停止面板与节点服务；保留目标现有数据，不要把旧快照覆盖到还在运行的数据库上。
5. 人工审核 manifest.json 中的 destination，把 database/qingzhou.db 及 files/ 内配置放回相应位置；按清单恢复文件权限及服务账户所需的所有权。不要直接从备份恢复未知可执行文件。
6. 恢复原 QZ_SECRET_KEY 与环境配置；缺少原密钥会导致数据库敏感配置无法解密。检查 systemd 的 EnvironmentFile、ExecStart、WorkingDirectory 与覆盖项。
7. 重新配置 DNS、反向代理/Tunnel、防火墙和云安全组。它们不一定在此包范围，须参考项目权威文档。检查 missing_optional，不把缺失项当作已备份。
8. 执行 SQLite integrity_check、systemctl daemon-reload；先在隔离环境验证再启动生产。核对健康接口、登录、订阅、节点连通、流量统计及灾备下载。

R2 恢复凭据、Cloudflare MFA 恢复方式必须在服务器以外独立保管。已生成下载链接属于短时授权凭据，不应粘贴到公开文档。
每次备份只包含快照时刻已提交的数据，不提供跨文件事务或自动在线恢复。
`

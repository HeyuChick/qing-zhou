package sshctl

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

func isWindows() bool { return runtime.GOOS == "windows" }

// Private keys can be kept as files on the panel host instead of being pasted
// into the browser and stored in the database. A server row then holds only a
// file NAME, and this file is what turns that name into bytes.
//
// The name is a name, never a path. That restriction is the entire security
// model here: without it, an admin-controlled string that the panel opens and
// reads is an arbitrary-file-read primitive on the panel host — point a row at
// /etc/shadow and probe it through the parse errors. Everything below exists to
// keep the resolved file inside the configured directory.

// ErrKeyDirUnset means the panel has no SSH key directory configured, so file
// keys cannot be used at all.
var ErrKeyDirUnset = fmt.Errorf("未配置 SSH 私钥目录")

// KeyFile describes one candidate key file for the admin UI.
type KeyFile struct {
	Name string `json:"name"`
	// Readable is whether the panel process can actually open it. Under Docker
	// the panel runs as uid 10001, so a 0600 key owned by root on the host is
	// mounted in unreadable — by far the most common way this feature fails, and
	// worth showing before someone saves a row that cannot connect.
	Readable bool `json:"readable"`
	// ModeOK is whether the file is not group/world readable.
	ModeOK bool `json:"mode_ok"`
	Size   int64 `json:"size"`
}

// validKeyName rejects anything that is not a plain file name.
func validKeyName(name string) error {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return fmt.Errorf("私钥文件名为空")
	case name != filepath.Base(name),
		strings.ContainsAny(name, `/\`),
		strings.Contains(name, ".."):
		return fmt.Errorf("私钥只能填目录里的文件名，不能是路径：%q", name)
	}
	return nil
}

// ResolveKeyFile turns a stored file name into an absolute path inside dir,
// refusing anything that escapes it.
func ResolveKeyFile(dir, name string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", ErrKeyDirUnset
	}
	if err := validKeyName(name); err != nil {
		return "", err
	}
	full := filepath.Join(dir, strings.TrimSpace(name))

	// Name validation alone is not enough: a symlink inside the directory can
	// still point anywhere on the host. Compare the resolved targets, not the
	// strings that produced them.
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("私钥目录不可用：%w", err)
	}
	realFull, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", fmt.Errorf("读取私钥 %s 失败：%w", name, err)
	}
	rel, err := filepath.Rel(realDir, realFull)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("私钥 %s 指向了目录之外，拒绝使用", name)
	}
	return realFull, nil
}

// ReadKeyFile returns the PEM bytes of a key file in dir.
func ReadKeyFile(dir, name string) ([]byte, error) {
	full, err := ResolveKeyFile(dir, name)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(full)
	if err != nil {
		return nil, fmt.Errorf("读取私钥 %s 失败：%w", name, err)
	}
	if fi.IsDir() {
		return nil, fmt.Errorf("%s 是目录，不是私钥文件", name)
	}
	// Refuse a key any other local account can read. ssh(1) does the same, and
	// for the same reason: this key opens a root shell on every node it serves.
	//
	// Not on Windows, where Go reports 0666 for any writable file regardless of
	// its actual ACL — the check there would reject every key ever offered.
	if mode := fi.Mode().Perm(); !isWindows() && mode&0o077 != 0 {
		return nil, fmt.Errorf("私钥 %s 权限过松（%04o），其他本地账号也能读；请 chmod 600", name, mode)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("读取私钥 %s 失败：%w", name, err)
	}
	return data, nil
}

// ListKeyFiles enumerates candidate key files in dir, newest name order, with
// what the panel can tell about each without reading it.
//
// It never returns file contents — the whole point of keeping keys on disk is
// that they do not travel to the browser.
func ListKeyFiles(dir string) ([]KeyFile, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, ErrKeyDirUnset
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // not created yet is not an error, just nothing to offer
		}
		return nil, err
	}
	var out []KeyFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// Public keys and known_hosts sit in the same directory on a real box and
		// are never what you want here.
		if strings.HasSuffix(name, ".pub") || name == "known_hosts" || name == "config" {
			continue
		}
		k := KeyFile{Name: name}
		if fi, err := e.Info(); err == nil {
			k.Size = fi.Size()
			k.ModeOK = isWindows() || fi.Mode().Perm()&0o077 == 0
		}
		if f, err := os.Open(filepath.Join(dir, name)); err == nil {
			k.Readable = true
			f.Close()
		}
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ValidateKeyFile checks that a named key file exists, is readable, has sane
// permissions and actually parses with the given passphrase.
//
// Called when a server row is saved so the admin learns about a typo'd name or a
// wrong passphrase right there, instead of hours later when a config deploy
// fails on a machine nobody was looking at.
func ValidateKeyFile(dir, name, passphrase string) error {
	data, err := ReadKeyFile(dir, name)
	if err != nil {
		return err
	}
	if _, err := parsePrivateKey(string(data), passphrase); err != nil {
		return fmt.Errorf("私钥 %s 解析失败（密钥密码不对，或不是私钥文件）：%w", name, err)
	}
	return nil
}

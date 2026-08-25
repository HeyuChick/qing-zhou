package sshctl

import (
	"errors"
	"strings"
	"testing"
)

// The whole promise of adding sudo support is that it changes nothing for the
// installations that already work — and virtually all of them log in as root.
// If this ever fails, an upgrade has started sending different commands to every
// existing node.
func TestElevate_RootRowsAreUntouched(t *testing.T) {
	cfg := &ServerConfig{SSHUser: "root"} // UseSudo false
	for _, cmd := range []string{
		"systemctl restart 'sing-box'",
		"mv -f '/etc/sing-box/config.json.qz-new-3' '/etc/sing-box/config.json'",
		"cat '/etc/sing-box/config.json' 2>/dev/null",
	} {
		if got := elevate(cfg, cmd); got != cmd {
			t.Errorf("elevate() rewrote a root row's command:\n got %q\nwant %q", got, cmd)
		}
	}
}

func TestElevate_SudoForms(t *testing.T) {
	nopasswd := &ServerConfig{UseSudo: true}
	if got, want := elevate(nopasswd, "systemctl restart 'x'"), "sudo -n -- systemctl restart 'x'"; got != want {
		t.Errorf("passwordless sudo: got %q, want %q", got, want)
	}

	withPass := &ServerConfig{UseSudo: true, SudoPassword: "hunter2"}
	got := elevate(withPass, "systemctl restart 'x'")
	if !strings.HasPrefix(got, "sudo -S -p '' -- ") {
		t.Errorf("password sudo: got %q, want a `sudo -S -p ''` prefix", got)
	}
	// The password reaches sudo over the session's stdin. Putting it in the
	// command would expose it to every local user on the node through ps.
	if strings.Contains(got, "hunter2") {
		t.Errorf("sudo password leaked into the command line: %q", got)
	}
}

// No `sh -c`: each caller passes one simple command, and interposing a shell
// would mean quoting the whole thing — including, on the write path, a config
// carrying the Reality private key and every user's credentials.
func TestElevate_DoesNotWrapInAShell(t *testing.T) {
	got := elevate(&ServerConfig{UseSudo: true}, "mkdir -p '/etc/sing-box'")
	if strings.Contains(got, "sh -c") {
		t.Errorf("elevate wrapped the command in a shell: %q", got)
	}
}

// A root row must keep staging next to the live config: a same-directory rename
// is atomic, and the config never leaves the directory it belongs in. Only an
// unprivileged account needs the /tmp hop, because sudo cannot take the shell
// redirect that writes the file.
func TestStagePath_RootStagesBesideTheConfig(t *testing.T) {
	root := &ServerConfig{ID: 7, ConfigPath: "/etc/sing-box/config.json"}
	if got, want := rootStagePath(root), "/etc/sing-box/config.json.qz-new-7"; got != want {
		t.Errorf("root staging path: got %q, want %q", got, want)
	}
}

// Rebuild fans out one goroutine per enabled server, so two rows pointing at the
// same host and path must not share a root staging name — they would interleave
// and publish each other's config.
func TestStagePathIsPerServer(t *testing.T) {
	a := &ServerConfig{ID: 1, Host: "h", ConfigPath: "/etc/sing-box/config.json"}
	b := &ServerConfig{ID: 2, Host: "h", ConfigPath: "/etc/sing-box/config.json"}
	if rootStagePath(a) == rootStagePath(b) {
		t.Error("two servers on the same host+path share a staging file")
	}
}

func TestTempStagePathMustBeRandomFileCreatedByMktemp(t *testing.T) {
	template := ".qz-cfg.XXXXXXXXXX"
	cmd := tempFileCommand(template)
	if !strings.Contains(cmd, "mktemp ") || strings.Contains(cmd, "mktemp -u") {
		t.Fatalf("temp file must be created atomically, got command %q", cmd)
	}
	for _, good := range []string{
		"/tmp/.qz-cfg.aB91kLmN0p",
		"/tmp/.qz-cfg.1234567890",
	} {
		if !validTempFilePath(good, template) {
			t.Errorf("valid mktemp path rejected: %q", good)
		}
	}
	for _, bad := range []string{
		"/tmp/.qz-cfg-7.json", // old predictable name
		"/var/tmp/.qz-cfg.1234567890",
		"/tmp/other.1234567890",
		"/tmp/.qz-cfg.123\n/tmp/second",
	} {
		if validTempFilePath(bad, template) {
			t.Errorf("unsafe temp path accepted: %q", bad)
		}
	}
}

func TestBinCacheEntryIsBoundToConnectionAndConfiguredPath(t *testing.T) {
	cfg := &ServerConfig{ID: 7, Host: "old.example", Port: 22, SingBoxBin: " /custom/sing-box "}
	entry := binCacheEntry{
		host:       "old.example",
		port:       22,
		configured: "/custom/sing-box",
		resolved:   "/custom/sing-box",
	}
	if !entry.matches(cfg) {
		t.Fatal("matching cache entry was rejected")
	}

	for name, mutate := range map[string]func(*ServerConfig){
		"host":            func(c *ServerConfig) { c.Host = "new.example" },
		"port":            func(c *ServerConfig) { c.Port = 2222 },
		"configured path": func(c *ServerConfig) { c.SingBoxBin = "/usr/local/bin/sing-box" },
	} {
		changed := *cfg
		mutate(&changed)
		if entry.matches(&changed) {
			t.Errorf("cache entry survived changed %s", name)
		}
	}
}

func TestForgetSingBoxBinClearsLongLivedManagerCache(t *testing.T) {
	m := New()
	m.binCache = map[int64]binCacheEntry{7: {resolved: "/old/sing-box"}}
	m.ForgetSingBoxBin(7)
	if _, ok := m.binCache[7]; ok {
		t.Error("cached binary survived explicit invalidation")
	}
}

// sudo's refusals are terse and the reason is never in the exit status. An admin
// reading "exit status 1" has nowhere to go; these say what to change.
func TestAnnotateSudoError_ExplainsTheCommonRefusals(t *testing.T) {
	for _, tc := range []struct{ out, want string }{
		{"sudo: sorry, you must have a tty to run sudo", "requiretty"},
		{"sudo: a password is required", "NOPASSWD"},
		{"sudo: incorrect password attempt", "密码不对"},
		{"deploy is not in the sudoers file.", "sudoers"},
		{"bash: sudo: command not found", "没装 sudo"},
	} {
		got := annotateSudoError(errors.New("exit status 1"), tc.out)
		if !strings.Contains(got.Error(), tc.want) {
			t.Errorf("output %q\n got %q\nwant it to mention %q", tc.out, got, tc.want)
		}
	}

	// An unrecognised failure must pass through unchanged rather than acquiring a
	// guess about its cause.
	orig := errors.New("exit status 3")
	if got := annotateSudoError(orig, "something else entirely"); !errors.Is(got, orig) {
		t.Errorf("unrecognised failure was rewritten: %v", got)
	}
}

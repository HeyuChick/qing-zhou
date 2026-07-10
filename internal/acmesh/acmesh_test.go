package acmesh

import (
	"strings"
	"testing"
)

func TestBuildIssueCmd_HTTP01(t *testing.T) {
	cmd, err := buildIssueCmd(IssueOpts{Domain: "proxy.example.com", Method: MethodHTTP01})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	for _, want := range []string{"--issue", "-d 'proxy.example.com'", "--standalone --httpport 80", "--server letsencrypt"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("cmd %q missing %q", cmd, want)
		}
	}
	if strings.Contains(cmd, "CF_Token") {
		t.Errorf("http-01 cmd should not export CF_Token: %q", cmd)
	}
}

func TestBuildIssueCmd_HTTP01RejectsWildcard(t *testing.T) {
	if _, err := buildIssueCmd(IssueOpts{Domain: "*.example.com", Method: MethodHTTP01}); err == nil {
		t.Error("expected wildcard to be rejected for http-01")
	}
}

func TestBuildIssueCmd_CFDNS(t *testing.T) {
	cmd, err := buildIssueCmd(IssueOpts{Domain: "*.example.com", Method: MethodCFDNS, CFToken: "tok123"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	for _, want := range []string{"export CF_Token='tok123';", "-d '*.example.com'", "--dns dns_cf"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("cmd %q missing %q", cmd, want)
		}
	}
}

func TestBuildIssueCmd_CFDNSNeedsToken(t *testing.T) {
	if _, err := buildIssueCmd(IssueOpts{Domain: "a.example.com", Method: MethodCFDNS}); err == nil {
		t.Error("expected error when CF token is missing")
	}
}

func TestBuildIssueCmd_Validation(t *testing.T) {
	if _, err := buildIssueCmd(IssueOpts{Domain: "", Method: MethodHTTP01}); err == nil {
		t.Error("expected error for empty domain")
	}
	if _, err := buildIssueCmd(IssueOpts{Domain: "a.com", Method: "bogus"}); err == nil {
		t.Error("expected error for unknown method")
	}
}

func TestBuildInstallCmd(t *testing.T) {
	cmd := buildInstallCmd(IssueOpts{Domain: "a.example.com", ReloadCmd: "systemctl restart sing-box"},
		"/etc/sing-box/certs/a.example.com.crt", "/etc/sing-box/certs/a.example.com.key")
	for _, want := range []string{
		"--install-cert", "-d 'a.example.com'",
		"--key-file '/etc/sing-box/certs/a.example.com.key'",
		"--fullchain-file '/etc/sing-box/certs/a.example.com.crt'",
		"--reloadcmd 'systemctl restart sing-box'",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("cmd %q missing %q", cmd, want)
		}
	}
}

func TestCertPaths(t *testing.T) {
	c, k := certPaths("", "a.example.com")
	if c != "/etc/sing-box/certs/a.example.com.crt" || k != "/etc/sing-box/certs/a.example.com.key" {
		t.Errorf("default paths wrong: %q %q", c, k)
	}
	c, k = certPaths("/opt/certs", "*.example.com")
	if c != "/opt/certs/wildcard.example.com.crt" || k != "/opt/certs/wildcard.example.com.key" {
		t.Errorf("wildcard paths wrong: %q %q", c, k)
	}
}

func TestShellQuoteEscaping(t *testing.T) {
	// A token containing a single quote must be escaped so injection is impossible.
	got := shellQuote("a'b")
	if got != `'a'\''b'` {
		t.Errorf("shellQuote = %q", got)
	}
}

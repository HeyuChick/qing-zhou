// Package acmesh drives the acme.sh shell client to issue and install real
// Let's Encrypt certificates, mirroring what mature panels (3x-ui / s-ui) do:
// rather than embedding a Go ACME stack, it shells out to acme.sh, installs the
// cert to a fixed path, and lets acme.sh's own cron handle renewal. sing-box is
// pointed at the installed file paths (tls.certificate_path / key_path), so a
// renewal is picked up on the next service reload with no panel involvement.
//
// Execution is abstracted behind Runner so the same logic works on the local
// host (os/exec) or, later, a remote server (SSH).
package acmesh

import (
	"context"
	"fmt"
	"path"
	"strings"
)

// acmeBin is the standard acme.sh install location (per-user, root here).
const acmeBin = "$HOME/.acme.sh/acme.sh"

// Runner executes a shell command on a host and returns combined stdout+stderr.
type Runner interface {
	Run(ctx context.Context, cmd string) (out string, err error)
}

// Method is the ACME challenge method.
type Method string

const (
	MethodHTTP01 Method = "http-01" // standalone listener on :80
	MethodCFDNS  Method = "dns-cf"  // Cloudflare DNS-01 (supports wildcard)
)

// IssueOpts describes a certificate request.
type IssueOpts struct {
	Domain    string // e.g. proxy.example.com or *.example.com (dns-cf only)
	Method    Method
	CFToken   string // Cloudflare API token, required for MethodCFDNS
	Email     string // ACME account email (optional but recommended)
	CertDir   string // where the installed cert/key land; default /etc/sing-box/certs
	ReloadCmd string // run by acme.sh after issue and on every renewal
}

// IssueResult is the on-disk location of the installed certificate.
type IssueResult struct {
	CertPath string // fullchain PEM
	KeyPath  string // private key PEM
}

// shellQuote wraps s in single quotes, escaping any embedded single quotes, so
// it is safe to interpolate into a /bin/sh command line.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Installed reports whether acme.sh is present and executable on the host.
func Installed(ctx context.Context, r Runner) bool {
	out, err := r.Run(ctx, `[ -x `+acmeBin+` ] && echo __yes__ || echo __no__`)
	return err == nil && strings.Contains(out, "__yes__")
}

// Install fetches and installs acme.sh (idempotent — acme.sh's installer is safe
// to re-run) and pins Let's Encrypt as the default CA. A curl or wget must be
// present on the host.
func Install(ctx context.Context, r Runner, email string) error {
	acct := ""
	if e := strings.TrimSpace(email); e != "" {
		acct = " --accountemail " + shellQuote(e)
	}
	// Prefer curl, fall back to wget — same probe the probe installer uses.
	get := `if command -v curl >/dev/null 2>&1; then curl -fsSL https://get.acme.sh | sh -s -- ` +
		strings.TrimSpace(acct) + `; elif command -v wget >/dev/null 2>&1; then wget -qO- https://get.acme.sh | sh -s -- ` +
		strings.TrimSpace(acct) + `; else echo "need curl or wget" >&2; exit 1; fi`
	if out, err := r.Run(ctx, get); err != nil {
		return fmt.Errorf("install acme.sh: %v: %s", err, out)
	}
	// Pin CA to Let's Encrypt (avoids ZeroSSL's mandatory registration).
	if out, err := r.Run(ctx, acmeBin+` --set-default-ca --server letsencrypt`); err != nil {
		return fmt.Errorf("set default CA: %v: %s", err, out)
	}
	return nil
}

// buildIssueCmd constructs the `acme.sh --issue` command for the given options.
// Exposed (lowercase, but unit-tested in-package) so the exact command line is
// verifiable without a live host.
func buildIssueCmd(o IssueOpts) (string, error) {
	domain := strings.TrimSpace(o.Domain)
	if domain == "" {
		return "", fmt.Errorf("domain is required")
	}
	base := acmeBin + " --issue -d " + shellQuote(domain) + " --server letsencrypt"
	switch o.Method {
	case MethodHTTP01:
		if strings.HasPrefix(domain, "*.") {
			return "", fmt.Errorf("wildcard domains require the Cloudflare DNS method")
		}
		return base + " --standalone --httpport 80", nil
	case MethodCFDNS:
		if strings.TrimSpace(o.CFToken) == "" {
			return "", fmt.Errorf("Cloudflare API token is required for the DNS method")
		}
		// CF_Token is consumed by acme.sh's dns_cf hook from the environment.
		return "export CF_Token=" + shellQuote(o.CFToken) + "; " + base + " --dns dns_cf", nil
	default:
		return "", fmt.Errorf("unsupported method %q", o.Method)
	}
}

// buildInstallCmd constructs the `acme.sh --install-cert` command that copies the
// issued cert/key to stable paths and records the reload command for renewals.
func buildInstallCmd(o IssueOpts, certPath, keyPath string) string {
	cmd := acmeBin + " --install-cert -d " + shellQuote(strings.TrimSpace(o.Domain)) +
		" --key-file " + shellQuote(keyPath) +
		" --fullchain-file " + shellQuote(certPath)
	if rc := strings.TrimSpace(o.ReloadCmd); rc != "" {
		cmd += " --reloadcmd " + shellQuote(rc)
	}
	return cmd
}

// certPaths returns the stable install paths for a domain under CertDir. A
// wildcard's leading "*." is normalized to "wildcard." for a valid filename.
func certPaths(certDir, domain string) (certPath, keyPath string) {
	dir := strings.TrimSpace(certDir)
	if dir == "" {
		dir = "/etc/sing-box/certs"
	}
	fn := strings.TrimSpace(domain)
	if strings.HasPrefix(fn, "*.") {
		fn = "wildcard." + strings.TrimPrefix(fn, "*.")
	}
	return path.Join(dir, fn+".crt"), path.Join(dir, fn+".key")
}

// Issue installs acme.sh if needed, requests the certificate, installs it to a
// stable path, and returns those paths. The caller stores the paths in the TLS
// profile (tls.certificate_path / key_path); renewal is handled by acme.sh's
// cron via the recorded reload command.
func Issue(ctx context.Context, r Runner, o IssueOpts) (*IssueResult, error) {
	issueCmd, err := buildIssueCmd(o)
	if err != nil {
		return nil, err
	}
	if !Installed(ctx, r) {
		if err := Install(ctx, r, o.Email); err != nil {
			return nil, err
		}
	}
	certPath, keyPath := certPaths(o.CertDir, o.Domain)
	// Ensure the target directory exists before install-cert writes into it.
	if out, err := r.Run(ctx, "mkdir -p "+shellQuote(path.Dir(certPath))); err != nil {
		return nil, fmt.Errorf("create cert dir: %v: %s", err, out)
	}
	if out, err := r.Run(ctx, issueCmd); err != nil {
		// acme.sh exits non-zero when a valid cert already exists and isn't near
		// renewal ("Domains not changed / skipping"); treat that as success.
		if !strings.Contains(out, "Skipping") && !strings.Contains(out, "not changed") {
			return nil, fmt.Errorf("acme.sh issue failed: %v: %s", err, out)
		}
	}
	if out, err := r.Run(ctx, buildInstallCmd(o, certPath, keyPath)); err != nil {
		return nil, fmt.Errorf("acme.sh install-cert failed: %v: %s", err, out)
	}
	return &IssueResult{CertPath: certPath, KeyPath: keyPath}, nil
}

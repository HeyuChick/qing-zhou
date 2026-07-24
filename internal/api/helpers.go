package api

import (
	"database/sql"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

var errSingboxNotEnabled = errors.New("sing-box 未启用")

func nsOr(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func atoi(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

// shellQuoteAPI wraps s in single quotes (escaping embedded ones) so it is safe
// to interpolate into a /bin/sh command line built in the API layer.
func shellQuoteAPI(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// normalizeBase cleans a user-entered panel address into scheme://host form.
// A value without a scheme is assumed to be HTTPS (the common panel setup behind
// TLS); enter an explicit http:// for plain-HTTP IP:port access. Trailing
// slashes are trimmed so callers can append paths directly.
func normalizeBase(v string) string {
	v = strings.TrimRight(strings.TrimSpace(v), "/")
	if v == "" {
		return ""
	}
	if !strings.Contains(v, "://") {
		v = "https://" + v
	}
	return v
}

// publicBase returns the externally-visible base URL (scheme://host). Precedence:
//  1. QZ_PUBLIC_BASE env — ops pin, always wins.
//  2. the panel's configured 访问地址 (public_base setting, set in 系统设置).
//  3. a trusted proxy's forwarded scheme/host, else r.Host.
//
// Honoring the DB setting lets an admin fix the base from the UI (used for
// subscription links, the probe installer, verify/reset email links, and the
// copyable sing-box install command) without editing env + restarting.
func (a *API) publicBase(r *http.Request) string {
	if b := os.Getenv("QZ_PUBLIC_BASE"); b != "" {
		return normalizeBase(b)
	}
	if a != nil && a.st != nil {
		if b, _ := a.st.GetSetting("public_base"); b != "" {
			return normalizeBase(b)
		}
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	// Only honor forwarded scheme/host from a trusted proxy. Otherwise a client
	// could set X-Forwarded-Host to poison the password-reset / verify links that
	// are emailed to a victim (→ account takeover). Set QZ_PUBLIC_BASE or the
	// 访问地址 setting to pin it.
	if peerTrusted(r) {
		if xf := r.Header.Get("X-Forwarded-Proto"); xf != "" {
			scheme = xf
		}
		if xh := r.Header.Get("X-Forwarded-Host"); xh != "" {
			host = xh
		}
	}
	return scheme + "://" + host
}

// setAuthCookie writes the auth cookie. secure marks it Secure (HTTPS-only) so
// the 7-day session token is never sent over plaintext HTTP; the caller derives
// secure from the effective external scheme (honoring a trusted proxy's
// X-Forwarded-Proto), since TLS is usually terminated at the reverse proxy.
// isHTTPS reports whether the externally-visible URL is HTTPS (used to gate the
// Secure cookie flag). Honors QZ_PUBLIC_BASE and a trusted proxy's forwarded
// scheme via publicBase.
func (a *API) isHTTPS(r *http.Request) bool {
	return strings.HasPrefix(a.publicBase(r), "https://")
}

func setAuthCookie(w http.ResponseWriter, tok string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(tokenTTL),
	})
}

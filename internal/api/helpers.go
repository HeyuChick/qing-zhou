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

// publicBase returns the externally-visible base URL (scheme://host), honoring
// a reverse proxy's forwarded headers or an explicit QZ_PUBLIC_BASE override.
func publicBase(r *http.Request) string {
	if b := os.Getenv("QZ_PUBLIC_BASE"); b != "" {
		return strings.TrimRight(b, "/")
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	// Only honor forwarded scheme/host from a trusted proxy. Otherwise a client
	// could set X-Forwarded-Host to poison the password-reset / verify links that
	// are emailed to a victim (→ account takeover). Set QZ_PUBLIC_BASE to pin it.
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
func isHTTPS(r *http.Request) bool {
	return strings.HasPrefix(publicBase(r), "https://")
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

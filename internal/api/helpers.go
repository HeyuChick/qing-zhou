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
	if xf := r.Header.Get("X-Forwarded-Proto"); xf != "" {
		scheme = xf
	}
	host := r.Host
	if xh := r.Header.Get("X-Forwarded-Host"); xh != "" {
		host = xh
	}
	return scheme + "://" + host
}

func setAuthCookie(w http.ResponseWriter, tok string) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(tokenTTL),
	})
}

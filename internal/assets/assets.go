// Package assets embeds static files the panel serves directly, independent of
// any frontend build.
//
// install-singbox.sh lives here rather than in frontend/ because `vite build`
// wipes frontend/dist on every run: a script kept there would vanish from the
// binary the first time someone built the frontend without restoring it. It
// isn't frontend code either — it's a shell script users curl straight from the
// panel (see AdminSettings.vue, which renders the one-click install command).
package assets

import _ "embed"

// Embedded as a string, not []byte: //go:embed into a []byte hands every caller
// a mutable view of the same backing array, so one careless write would corrupt
// the script for every subsequent request.
//
//go:embed install-singbox.sh
var installScript string

// InstallScript returns the sing-box one-click install script served at
// /install-singbox.sh.
func InstallScript() string {
	return installScript
}

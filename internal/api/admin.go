package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"qingzhou/internal/subconv"
)

// secretSettings are never returned in plaintext and cannot be cleared blindly.
var secretSettings = map[string]bool{
	"jwt_secret": true,
	"smtp_pass":  true,
}

// immutableSettings cannot be written through the settings API at all.
//
// update_repo names the GitHub repo the self-updater installs from. Left
// writable, an admin session (a stolen token, or XSS) could repoint it at an
// attacker's repo: GitHub publishes a correct sha256 for whatever asset is
// uploaded there, so every integrity check passes and the downloaded binary
// replaces the running one and is exec'd — persistent code execution as the
// panel's uid, which on a typical deployment is root. It stays overridable via
// QZ_UPDATE_REPO, which requires host access the attacker doesn't have.
var immutableSettings = map[string]bool{
	"jwt_secret":  true, // never rotate the signing key through the API
	"update_repo": true,
}

// settingEnv maps a setting key to the env var that overrides it (env wins in
// buildMailer). Used to surface the *effective* config in the panel.
var settingEnv = map[string]string{
	"public_base":    "QZ_PUBLIC_BASE",
	"smtp_host":      "QZ_SMTP_HOST",
	"smtp_port":      "QZ_SMTP_PORT",
	"smtp_user":      "QZ_SMTP_USER",
	"smtp_pass":      "QZ_SMTP_PASS",
	"smtp_from":      "QZ_SMTP_FROM",
	"smtp_from_name": "QZ_SMTP_FROM_NAME",
	"smtp_security":  "QZ_SMTP_SECURITY",
}

func (a *API) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	all, err := a.st.AllSettings()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取配置失败")
		return
	}
	// Surface the effective value when an env var overrides the DB setting, so
	// env-configured sing-box / SMTP shows up in the panel instead of looking empty.
	var envKeys []string
	for k, env := range settingEnv {
		if v := os.Getenv(env); v != "" {
			all[k] = v
			envKeys = append(envKeys, k)
		}
	}
	for k := range all {
		if secretSettings[k] && all[k] != "" {
			all[k] = "***" // mask but show that it is set
		}
	}
	// Surface built-in default templates when the DB value is empty, so the
	// admin can see and edit the effective config rather than a blank box.
	if all["sub_clash_template"] == "" {
		all["sub_clash_template"] = subconv.DefaultClashTemplate
	}
	if all["sub_singbox_template"] == "" {
		all["sub_singbox_template"] = subconv.DefaultSingboxTemplate
	}
	all["_env_keys"] = strings.Join(envKeys, ",") // which fields are env-controlled
	ok(w, all)
}

func (a *API) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var in map[string]string
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	for k, v := range in {
		if strings.HasPrefix(k, "_") {
			continue // UI-only fields like _env_keys
		}
		if immutableSettings[k] {
			continue // host-only settings; see immutableSettings
		}
		if secretSettings[k] && (v == "***" || v == "") {
			continue // keep current secret: masked sentinel or left blank
		}
		// If the submitted template equals the built-in default, store empty
		// so "留空用内置" semantics are preserved (future default updates
		// will still take effect).
		if k == "sub_clash_template" && v == subconv.DefaultClashTemplate {
			v = ""
		}
		if k == "sub_singbox_template" && v == subconv.DefaultSingboxTemplate {
			v = ""
		}
		if err := a.st.SetSetting(k, v); err != nil {
			fail(w, http.StatusInternalServerError, "保存配置失败")
			return
		}
	}
	a.handleGetSettings(w, r)
}

package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"qingzhou/internal/store"
	"qingzhou/internal/subconv"
)

// secretSettings are never returned in plaintext and cannot be cleared blindly.
var secretSettings = map[string]bool{
	"jwt_secret":   true,
	"smtp_pass":    true,
	"cf_api_token": true,
	// A GitHub PAT. It only lifts the unauthenticated rate limit on release
	// lookups, but it is still a bearer credential for the admin's account —
	// it must not come back out of the settings API the way a hostname does.
	"update_github_token": true,
	"telegram_bot_token":  true,
}

// clearableSecrets are secretSettings that an empty submission *clears* rather
// than leaves alone.
//
// The blanket rule below treats "" as "left blank, keep the current secret",
// which protects a required credential from being wiped by a half-filled form.
// That is wrong for an optional one: the GitHub token is a pure opt-in (without
// it release lookups just run anonymously), so an admin who pastes a bad token
// would otherwise be stuck — every update check fails 401 and there is no way
// back to the working anonymous path short of editing the DB. The form always
// loads "***" for a secret that is set, so "" here can only be a deliberate
// select-all-delete, never an omission.
var clearableSecrets = map[string]bool{
	"update_github_token": true,
	// Optional: emptying the token is how the admin turns the bot off.
	"telegram_bot_token": true,
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
	// Same precedence the updater itself applies (see New in router.go): env
	// wins over the DB value, so the panel has to show the env one as effective
	// or a host-configured token reads as "not set".
	"update_github_token":   "QZ_UPDATE_GITHUB_TOKEN",
	"telegram_bot_token":    "QZ_TELEGRAM_BOT_TOKEN",
	"telegram_bot_username": "QZ_TELEGRAM_BOT_USERNAME",
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
	for _, spec := range tgTplSpecs {
		k := tgTplSettingKey(spec.Key)
		if all[k] == "" {
			all[k] = defaultTGTemplates[spec.Key]
		}
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
	// Bot username is derived from getMe. If a real token submission changes,
	// ignore the form's stale username regardless of map iteration order and
	// clear the cache after all writes.
	tokenChanged := false
	if submitted, ok := in["telegram_bot_token"]; ok && submitted != "***" {
		current, _ := a.st.GetSetting("telegram_bot_token")
		tokenChanged = strings.TrimSpace(submitted) != strings.TrimSpace(current)
	}
	for k, v := range in {
		if tokenChanged && k == "telegram_bot_username" {
			continue
		}
		if strings.HasPrefix(k, "_") {
			continue // UI-only fields like _env_keys
		}
		if immutableSettings[k] {
			continue // host-only settings; see immutableSettings
		}
		if secretSettings[k] && (v == "***" || (v == "" && !clearableSecrets[k])) {
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
		v = normalizeTGTplSetting(k, v)
		if err := a.st.SetSetting(k, v); err != nil {
			fail(w, http.StatusInternalServerError, "保存配置失败")
			return
		}
		// This one is baked into every node's generated route table, so saving it
		// has to reach the nodes — otherwise the switch reads as broken until the
		// next unrelated edit happens to trigger a rebuild.
		//
		// Through sbRebuildLog, not a.sbctl directly: the controller is documented
		// as optional ("Safe to leave unset" on SetSbController) and is nil between
		// api.New and SetSbController, so reaching for it raw is a nil dereference
		// waiting for the one caller that never wires it.
		if k == store.SettingBlockPrivateEgress {
			a.sbRebuildLog()
		}
	}
	if tokenChanged {
		if err := a.st.SetSetting("telegram_bot_username", ""); err != nil {
			fail(w, http.StatusInternalServerError, "保存配置失败")
			return
		}
	}
	a.handleGetSettings(w, r)
}

// handleGetDefaultTemplates returns the built-in Clash/sing-box subscription
// templates, so the settings UI can show the actual default (and let the admin
// load it to edit) instead of a blank box. Saving either field back equal to its
// default clears the override — see handlePutSettings — so the built-in stays
// live and future updates take effect.
func (a *API) handleGetDefaultTemplates(w http.ResponseWriter, r *http.Request) {
	ok(w, J{
		"clash":    subconv.DefaultClashTemplate,
		"singbox":  subconv.DefaultSingboxTemplate,
		"telegram": defaultTGTemplateViews(),
	})
}

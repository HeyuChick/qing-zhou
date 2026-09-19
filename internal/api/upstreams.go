package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"qingzhou/internal/officialusage"
)

const (
	upstreamOCISetting        = "upstream_oci_config"
	upstreamCloudflareSetting = "upstream_cloudflare_config"
)

type upstreamView struct {
	Provider          string `json:"provider"`
	Configured        bool   `json:"configured"`
	TenancyOCID       string `json:"tenancy_ocid,omitempty"`
	UserOCID          string `json:"user_ocid,omitempty"`
	Fingerprint       string `json:"fingerprint,omitempty"`
	Region            string `json:"region,omitempty"`
	AccountID         string `json:"account_id,omitempty"`
	Limit             int64  `json:"limit"`
	PrivateKeySet     bool   `json:"private_key_set,omitempty"`
	AnalyticsTokenSet bool   `json:"analytics_token_set,omitempty"`
}

type upstreamSaveRequest struct {
	TenancyOCID       string `json:"tenancy_ocid"`
	UserOCID          string `json:"user_ocid"`
	Fingerprint       string `json:"fingerprint"`
	Region            string `json:"region"`
	PrivateKey        string `json:"private_key"`
	MonthlyLimitBytes int64  `json:"monthly_limit_bytes"`
	AccountID         string `json:"account_id"`
	AnalyticsToken    string `json:"analytics_token"`
	DailyRequestLimit int64  `json:"daily_request_limit"`
}

// handleAdminGetUpstreams returns only safe configuration state. The encrypted
// setting values and provider credentials never leave the server.
func (a *API) handleAdminGetUpstreams(w http.ResponseWriter, r *http.Request) {
	oci, err := a.loadOCIConfig()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取 OCI 上游配置失败")
		return
	}
	cf, err := a.loadCloudflareConfig()
	if err != nil {
		fail(w, http.StatusInternalServerError, "读取 Cloudflare 上游配置失败")
		return
	}
	ok(w, []upstreamView{ociView(oci), cloudflareView(cf)})
}

func (a *API) handleAdminPutUpstream(w http.ResponseWriter, r *http.Request) {
	var input upstreamSaveRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	switch chi.URLParam(r, "provider") {
	case "oci":
		current, err := a.loadOCIConfig()
		if err != nil {
			fail(w, http.StatusInternalServerError, "读取 OCI 上游配置失败")
			return
		}
		privateKey := strings.TrimSpace(input.PrivateKey)
		if privateKey == "" {
			privateKey = current.PrivateKey
		}
		config := officialusage.OCIConfig{
			TenancyOCID: input.TenancyOCID, UserOCID: input.UserOCID,
			Fingerprint: input.Fingerprint, Region: input.Region,
			PrivateKey: privateKey, MonthlyLimitBytes: input.MonthlyLimitBytes,
		}
		if err := config.Validate(); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := a.saveUpstream(upstreamOCISetting, config); err != nil {
			fail(w, http.StatusInternalServerError, "保存 OCI 上游配置失败")
			return
		}
		ok(w, ociView(config))
	case "cloudflare":
		current, err := a.loadCloudflareConfig()
		if err != nil {
			fail(w, http.StatusInternalServerError, "读取 Cloudflare 上游配置失败")
			return
		}
		token := strings.TrimSpace(input.AnalyticsToken)
		if token == "" {
			token = current.AnalyticsToken
		}
		config := officialusage.CloudflareConfig{AccountID: input.AccountID, AnalyticsToken: token, DailyRequestLimit: input.DailyRequestLimit}
		if err := config.Validate(); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := a.saveUpstream(upstreamCloudflareSetting, config); err != nil {
			fail(w, http.StatusInternalServerError, "保存 Cloudflare 上游配置失败")
			return
		}
		ok(w, cloudflareView(config))
	default:
		fail(w, http.StatusNotFound, "不支持的上游供应商")
	}
}

func (a *API) handleAdminDeleteUpstream(w http.ResponseWriter, r *http.Request) {
	setting, valid := upstreamSetting(chi.URLParam(r, "provider"))
	if !valid {
		fail(w, http.StatusNotFound, "不支持的上游供应商")
		return
	}
	if err := a.st.DeleteSetting(setting); err != nil {
		fail(w, http.StatusInternalServerError, "清除上游配置失败")
		return
	}
	ok(w, nil)
}

func (a *API) handleAdminRefreshUpstream(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	switch provider {
	case "oci":
		config, err := a.loadOCIConfig()
		if err != nil {
			fail(w, http.StatusInternalServerError, "读取 OCI 上游配置失败")
			return
		}
		usage := officialusage.FetchOCI(ctx, a.upstreamClient, config, time.Now())
		if !usage.Configured {
			fail(w, http.StatusBadRequest, "请先完整配置 OCI 上游")
			return
		}
		ok(w, usage)
	case "cloudflare":
		config, err := a.loadCloudflareConfig()
		if err != nil {
			fail(w, http.StatusInternalServerError, "读取 Cloudflare 上游配置失败")
			return
		}
		usage := officialusage.FetchCloudflare(ctx, a.upstreamClient, config, time.Now())
		if !usage.Configured {
			fail(w, http.StatusBadRequest, "请先完整配置 Cloudflare 上游")
			return
		}
		ok(w, usage)
	default:
		fail(w, http.StatusNotFound, "不支持的上游供应商")
	}
}

func (a *API) loadOCIConfig() (officialusage.OCIConfig, error) {
	return loadUpstream[officialusage.OCIConfig](a.st.GetSetting, upstreamOCISetting)
}

func (a *API) loadCloudflareConfig() (officialusage.CloudflareConfig, error) {
	return loadUpstream[officialusage.CloudflareConfig](a.st.GetSetting, upstreamCloudflareSetting)
}

func loadUpstream[T any](get func(string) (string, error), key string) (T, error) {
	var config T
	raw, err := get(key)
	if err != nil || raw == "" {
		return config, err
	}
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return config, errors.New("配置数据无效")
	}
	return config, nil
}

func (a *API) saveUpstream(key string, config any) error {
	raw, err := json.Marshal(config)
	if err != nil {
		return err
	}
	return a.st.SetSetting(key, string(raw))
}

func upstreamSetting(provider string) (string, bool) {
	switch provider {
	case "oci":
		return upstreamOCISetting, true
	case "cloudflare":
		return upstreamCloudflareSetting, true
	default:
		return "", false
	}
}

func ociView(config officialusage.OCIConfig) upstreamView {
	limit := config.MonthlyLimitBytes
	if limit <= 0 {
		limit = officialusage.DefaultOCIMonthlyLimitBytes
	}
	return upstreamView{
		Provider: "oci", Configured: config.Configured(), TenancyOCID: config.TenancyOCID,
		UserOCID: config.UserOCID, Fingerprint: config.Fingerprint, Region: config.Region,
		Limit: limit, PrivateKeySet: strings.TrimSpace(config.PrivateKey) != "",
	}
}

func cloudflareView(config officialusage.CloudflareConfig) upstreamView {
	limit := config.DailyRequestLimit
	if limit <= 0 {
		limit = officialusage.DefaultCFDailyRequestLimit
	}
	return upstreamView{
		Provider: "cloudflare", Configured: config.Configured(), AccountID: config.AccountID,
		Limit: limit, AnalyticsTokenSet: strings.TrimSpace(config.AnalyticsToken) != "",
	}
}

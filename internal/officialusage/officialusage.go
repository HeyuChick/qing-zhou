// Package officialusage reads provider-account usage directly from official
// provider APIs. It intentionally has no dependency on QingZhou's nodes or
// EdgeTunnel so the displayed numbers keep their original provider scope.
package officialusage

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultOCIMonthlyLimitBytes int64 = 10_000_000_000_000
	DefaultCFDailyRequestLimit  int64 = 100_000
)

// OCIConfig is the minimum OCI API-key profile required by the Usage API.
// PrivateKey is never serialized back by the admin API.
type OCIConfig struct {
	TenancyOCID       string `json:"tenancy_ocid"`
	UserOCID          string `json:"user_ocid"`
	Fingerprint       string `json:"fingerprint"`
	Region            string `json:"region"`
	PrivateKey        string `json:"private_key"`
	MonthlyLimitBytes int64  `json:"monthly_limit_bytes"`
}

// CloudflareConfig holds a dedicated Account Analytics token. It is distinct
// from the DNS token QingZhou can use for ACME.
type CloudflareConfig struct {
	AccountID         string `json:"account_id"`
	AnalyticsToken    string `json:"analytics_token"`
	DailyRequestLimit int64  `json:"daily_request_limit"`
}

// Usage is a provider-account usage snapshot. Remaining is derived by QingZhou
// from the configured allowance; neither provider API returns one shared value.
type Usage struct {
	Configured               bool   `json:"configured"`
	Success                  bool   `json:"success"`
	Used                     int64  `json:"used"`
	Limit                    int64  `json:"limit"`
	Remaining                int64  `json:"remaining"`
	Unit                     string `json:"unit"`
	Period                   string `json:"period"`
	Source                   string `json:"source"`
	QueryEnd                 string `json:"query_end,omitempty"`
	ItemCount                int    `json:"item_count,omitempty"`
	TransferItemCount        int    `json:"transfer_item_count,omitempty"`
	RecognizedTransferItems  int    `json:"recognized_transfer_items,omitempty"`
	SkippedTransferItemCount int    `json:"skipped_transfer_items,omitempty"`
	OverageDetected          bool   `json:"overage_detected,omitempty"`
	Warning                  string `json:"warning,omitempty"`
	UpdatedAt                string `json:"updated_at,omitempty"`
	Error                    string `json:"error,omitempty"`
}

func (c OCIConfig) normalized() OCIConfig {
	c.TenancyOCID = strings.TrimSpace(c.TenancyOCID)
	c.UserOCID = strings.TrimSpace(c.UserOCID)
	c.Fingerprint = strings.TrimSpace(c.Fingerprint)
	c.Region = strings.ToLower(strings.TrimSpace(c.Region))
	if c.MonthlyLimitBytes <= 0 {
		c.MonthlyLimitBytes = DefaultOCIMonthlyLimitBytes
	}
	return c
}

func (c CloudflareConfig) normalized() CloudflareConfig {
	c.AccountID = strings.TrimSpace(c.AccountID)
	c.AnalyticsToken = strings.TrimSpace(c.AnalyticsToken)
	if c.DailyRequestLimit <= 0 {
		c.DailyRequestLimit = DefaultCFDailyRequestLimit
	}
	return c
}

func (c OCIConfig) Configured() bool {
	c = c.normalized()
	return c.TenancyOCID != "" && c.UserOCID != "" && c.Fingerprint != "" && c.Region != "" && strings.TrimSpace(c.PrivateKey) != ""
}

func (c CloudflareConfig) Configured() bool {
	c = c.normalized()
	return c.AccountID != "" && c.AnalyticsToken != ""
}

var ociRegion = regexp.MustCompile(`^[a-z][a-z0-9-]+-[a-z]+-[0-9]+$`)

// Validate checks configuration supplied from the management form before it is
// encrypted and stored. It deliberately permits only the fixed OCI API host.
func (c OCIConfig) Validate() error {
	c = c.normalized()
	if c.TenancyOCID == "" || c.UserOCID == "" || c.Fingerprint == "" || c.Region == "" || strings.TrimSpace(c.PrivateKey) == "" {
		return errors.New("请完整填写 OCI 租户、用户、指纹、区域和 API 私钥")
	}
	if !ociRegion.MatchString(c.Region) {
		return errors.New("OCI 区域格式不正确，例如 ap-tokyo-1")
	}
	if c.MonthlyLimitBytes < 1 || c.MonthlyLimitBytes > 1_000_000_000_000_000 {
		return errors.New("OCI 月度上限必须在 1 B 到 1000 TB 之间")
	}
	_, err := parsePrivateKey(c.PrivateKey)
	if err != nil {
		return fmt.Errorf("OCI API 私钥无效: %w", err)
	}
	return nil
}

func (c CloudflareConfig) Validate() error {
	c = c.normalized()
	if c.AccountID == "" || c.AnalyticsToken == "" {
		return errors.New("请填写 Cloudflare Account ID 和 Analytics Token")
	}
	if c.DailyRequestLimit < 1 || c.DailyRequestLimit > 10_000_000_000 {
		return errors.New("Cloudflare 每日请求上限必须在 1 到 100 亿之间")
	}
	return nil
}

// FetchOCI reads current-UTC-month data-transfer usage from OCI's Usage API.
func FetchOCI(ctx context.Context, client *http.Client, config OCIConfig, now time.Time) Usage {
	config = config.normalized()
	base := Usage{Configured: config.Configured(), Limit: config.MonthlyLimitBytes, Unit: "bytes", Period: "本月（UTC）", Source: "OCI Usage API（已返回用量，非实时余额）"}
	if !base.Configured {
		base.Error = "未配置"
		return base
	}
	if err := config.Validate(); err != nil {
		base.Error = err.Error()
		return base
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	now = now.UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	base.QueryEnd = periodEnd.Format(time.RFC3339)
	base.UpdatedAt = now.Format(time.RFC3339)
	if !periodEnd.After(monthStart) {
		base.Error = "本月尚无完整 UTC 查询日，余额待确认"
		return base
	}
	base.Period = "本月（UTC，查询至今日 00:00）"
	host := "usageapi." + config.Region + ".oci.oraclecloud.com"
	var pageToken string
	seenPages := map[string]bool{}
	var report ociTransferReport
	for {
		request := map[string]any{
			"tenantId":          config.TenancyOCID,
			"timeUsageStarted":  monthStart.Format(time.RFC3339),
			"timeUsageEnded":    periodEnd.Format(time.RFC3339),
			"granularity":       "DAILY",
			"isAggregateByTime": true,
			"queryType":         "USAGE_ONLY",
			"groupBy":           []string{"service", "skuName", "unit"},
		}
		body, err := json.Marshal(request)
		if err != nil {
			base.Error = "构造 OCI 请求失败"
			return base
		}
		query := url.Values{"limit": []string{"1000"}}
		if pageToken != "" {
			query.Set("page", pageToken)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+host+"/20200107/usage?"+query.Encode(), bytes.NewReader(body))
		if err != nil {
			base.Error = "构造 OCI 请求失败"
			return base
		}
		if err := signOCIRequest(req, body, config); err != nil {
			base.Error = "签名 OCI 请求失败: " + err.Error()
			return base
		}
		resp, err := client.Do(req)
		if err != nil {
			base.Error = "OCI Usage API 请求失败: " + err.Error()
			return base
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
		resp.Body.Close()
		if readErr != nil || len(data) > 2<<20 {
			base.Error = "读取 OCI Usage API 响应失败"
			return base
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			base.Error = fmt.Sprintf("OCI Usage API 返回 HTTP %d", resp.StatusCode)
			return base
		}
		pageReport, parseErr := parseOCITransferReport(data)
		if parseErr != nil {
			base.Error = "解析 OCI Usage API 响应失败: " + parseErr.Error()
			return base
		}
		if err := report.add(pageReport); err != nil {
			base.Error = err.Error()
			return base
		}
		next := strings.TrimSpace(resp.Header.Get("opc-next-page"))
		if next == "" {
			break
		}
		if seenPages[next] || len(seenPages) >= 100 {
			base.Error = "OCI Usage API 分页循环或超出限制，余额待确认"
			return base
		}
		seenPages[next] = true
		pageToken = next
	}
	base.ItemCount = report.ItemCount
	base.TransferItemCount = report.TransferItemCount
	base.RecognizedTransferItems = report.RecognizedTransferItems
	base.SkippedTransferItemCount = report.SkippedTransferItemCount
	base.OverageDetected = report.OverageDetected
	if report.TransferItemCount == 0 {
		base.Error = "未返回可识别的出站计量条目，不能认定用量为零；余额待确认"
		return base
	}
	if report.SkippedTransferItemCount > 0 {
		base.Error = fmt.Sprintf("OCI 返回了 %d 条出站计量，但其中 %d 条的单位或数量无法识别，已拒绝计算余额", report.TransferItemCount, report.SkippedTransferItemCount)
		return base
	}
	base.Success = true
	base.Used = report.UsedBytes
	base.Remaining = remaining(base.Limit, report.UsedBytes)
	if report.OverageDetected {
		base.Remaining = 0
	}
	base.Warning = "按 OCI 已返回的免费层级与超额层级出站条目、以及配置额度计算；查询结束时间不代表数据已完整入账。"
	return base
}

func signOCIRequest(req *http.Request, body []byte, config OCIConfig) error {
	key, err := parsePrivateKey(config.PrivateKey)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(body)
	date := time.Now().UTC().Format(http.TimeFormat)
	req.Header.Set("Date", date)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Length", strconv.Itoa(len(body)))
	req.Header.Set("X-Content-SHA256", base64.StdEncoding.EncodeToString(digest[:]))

	headers := "(request-target) date host content-length content-type x-content-sha256"
	signingString := strings.Join([]string{
		"(request-target): " + strings.ToLower(req.Method) + " " + req.URL.RequestURI(),
		"date: " + date,
		"host: " + req.URL.Host,
		"content-length: " + strconv.Itoa(len(body)),
		"content-type: application/json",
		"x-content-sha256: " + req.Header.Get("X-Content-SHA256"),
	}, "\n")
	hash := sha256.Sum256([]byte(signingString))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	if err != nil {
		return err
	}
	keyID := config.TenancyOCID + "/" + config.UserOCID + "/" + config.Fingerprint
	req.Header.Set("Authorization", fmt.Sprintf(`Signature version="1",keyId="%s",algorithm="rsa-sha256",headers="%s",signature="%s"`, keyID, headers, base64.StdEncoding.EncodeToString(sig)))
	return nil
}

func parsePrivateKey(value string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(value)))
	if block == nil {
		return nil, errors.New("未找到 PEM 私钥")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("仅支持 RSA PKCS#1 或 PKCS#8 私钥")
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("仅支持 RSA 私钥")
	}
	return rsaKey, nil
}

type ociResponse struct {
	Items []ociUsageItem `json:"items"`
}

type ociUsageItem struct {
	Service          string      `json:"service"`
	SkuName          string      `json:"skuName"`
	Unit             string      `json:"unit"`
	AttributedUsage  string      `json:"attributedUsage"`
	ComputedQuantity json.Number `json:"computedQuantity"`
}

type ociTransferReport struct {
	UsedBytes                int64
	ItemCount                int
	TransferItemCount        int
	RecognizedTransferItems  int
	SkippedTransferItemCount int
	OverageDetected          bool
}

func (r *ociTransferReport) add(other ociTransferReport) error {
	if other.UsedBytes > math.MaxInt64-r.UsedBytes {
		return errors.New("OCI 用量超出可表示范围")
	}
	r.UsedBytes += other.UsedBytes
	r.ItemCount += other.ItemCount
	r.TransferItemCount += other.TransferItemCount
	r.RecognizedTransferItems += other.RecognizedTransferItems
	r.SkippedTransferItemCount += other.SkippedTransferItemCount
	r.OverageDetected = r.OverageDetected || other.OverageDetected
	return nil
}

func parseOCITransferBytes(data []byte) (int64, error) {
	report, err := parseOCITransferReport(data)
	return report.UsedBytes, err
}

func parseOCITransferReport(data []byte) (ociTransferReport, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var response ociResponse
	if err := decoder.Decode(&response); err != nil {
		return ociTransferReport{}, err
	}
	if response.Items == nil {
		return ociTransferReport{}, errors.New("OCI 响应缺少 items")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return ociTransferReport{}, errors.New("OCI 响应包含额外内容")
	}
	report := ociTransferReport{ItemCount: len(response.Items)}
	for _, item := range response.Items {
		if !isTransferUsage(item.Service, item.SkuName) {
			continue
		}
		report.TransferItemCount++
		lowerSKU := strings.ToLower(item.SkuName)
		if !strings.Contains(lowerSKU, "outbound") && !strings.Contains(lowerSKU, "egress") && !strings.Contains(lowerSKU, "transfer out") && !strings.Contains(lowerSKU, "outgoing") {
			report.SkippedTransferItemCount++
			continue
		}
		quantity, err := usageQuantity(item.AttributedUsage, item.ComputedQuantity)
		if err != nil {
			return ociTransferReport{}, err
		}
		bytes, ok := quantityToBytes(quantity, item.Unit)
		if !ok {
			report.SkippedTransferItemCount++
			continue
		}
		if bytes > math.MaxInt64-report.UsedBytes {
			return ociTransferReport{}, errors.New("OCI 用量超出可表示范围")
		}
		report.UsedBytes += bytes
		report.RecognizedTransferItems++
		if strings.Contains(strings.ToLower(item.SkuName), "over 10 tb") {
			report.OverageDetected = true
		}
	}
	return report, nil
}

func isTransferUsage(service, sku string) bool {
	s := strings.ToLower(strings.TrimSpace(service + " " + sku))
	if strings.Contains(s, "inbound") || strings.Contains(s, "data transfer in") || strings.Contains(s, "transfer in") {
		return false
	}
	return strings.Contains(s, "outbound") || strings.Contains(s, "egress") || strings.Contains(s, "data transfer out") || strings.Contains(s, "transfer out") || strings.Contains(s, "outgoing")
}

func usageQuantity(attributed string, computed json.Number) (*big.Rat, error) {
	v := strings.TrimSpace(attributed)
	if v == "" {
		v = strings.TrimSpace(computed.String())
	}
	if v == "" {
		return nil, errors.New("OCI 用量条目缺少数值")
	}
	n, ok := new(big.Rat).SetString(v)
	if !ok || n.Sign() < 0 {
		return nil, errors.New("OCI 用量数值无效")
	}
	return n, nil
}

func quantityToBytes(quantity *big.Rat, unit string) (int64, bool) {
	rawUnit := strings.ToLower(strings.TrimSpace(unit))
	normalizedUnit := strings.Join(strings.Fields(rawUnit), " ")
	if normalizedUnit == "gigabyte outbound data transfer per month" || normalizedUnit == "gigabytes outbound data transfer per month" || normalizedUnit == "gb month" || normalizedUnit == "gb months" {
		rawUnit = "gb"
	}
	if strings.Contains(rawUnit, "storage") || strings.Contains(rawUnit, "capacity") || strings.Contains(rawUnit, "per hour") || strings.Contains(rawUnit, "/hour") || strings.Contains(rawUnit, "rate") {
		return 0, false
	}
	u := strings.ToUpper(rawUnit)
	multipliers := map[string]int64{
		"B": 1, "BYTE": 1, "BYTES": 1,
		"KB": 1_000, "KILOBYTE": 1_000, "KILOBYTES": 1_000,
		"MB": 1_000_000, "MEGABYTE": 1_000_000, "MEGABYTES": 1_000_000,
		"GB": 1_000_000_000, "GIGABYTE": 1_000_000_000, "GIGABYTES": 1_000_000_000,
		"TB": 1_000_000_000_000, "TERABYTE": 1_000_000_000_000, "TERABYTES": 1_000_000_000_000,
		"KIB": 1 << 10, "MIB": 1 << 20, "GIB": 1 << 30, "TIB": 1 << 40,
	}
	m, ok := multipliers[u]
	if !ok {
		return 0, false
	}
	numerator := new(big.Int).Mul(quantity.Num(), big.NewInt(m))
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, quantity.Denom(), remainder)
	if new(big.Int).Lsh(remainder, 1).Cmp(quantity.Denom()) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return 0, false
	}
	return quotient.Int64(), true
}

// FetchCloudflare reads the current UTC day's Pages Functions and Workers
// invocation total from Cloudflare Account Analytics GraphQL.
func FetchCloudflare(ctx context.Context, client *http.Client, config CloudflareConfig, now time.Time) Usage {
	config = config.normalized()
	base := Usage{Configured: config.Configured(), Limit: config.DailyRequestLimit, Unit: "requests", Period: "今日（UTC）", Source: "Cloudflare Analytics GraphQL"}
	if !base.Configured {
		base.Error = "未配置"
		return base
	}
	if err := config.Validate(); err != nil {
		base.Error = err.Error()
		return base
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	now = now.UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	body, err := json.Marshal(map[string]any{
		"query":     `query getBillingMetrics($AccountID: String!, $filter: AccountWorkersInvocationsAdaptiveFilter_InputObject) { viewer { accounts(filter: {accountTag: $AccountID}) { pagesFunctionsInvocationsAdaptiveGroups(limit: 1000, filter: $filter) { sum { requests } } workersInvocationsAdaptive(limit: 10000, filter: $filter) { sum { requests } } } } }`,
		"variables": map[string]any{"AccountID": config.AccountID, "filter": map[string]string{"datetime_geq": dayStart.Format(time.RFC3339), "datetime_leq": now.Format(time.RFC3339)}},
	})
	if err != nil {
		base.Error = "构造 Cloudflare 请求失败"
		return base
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.cloudflare.com/client/v4/graphql", bytes.NewReader(body))
	if err != nil {
		base.Error = "构造 Cloudflare 请求失败"
		return base
	}
	req.Header.Set("Authorization", "Bearer "+config.AnalyticsToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		base.Error = "Cloudflare Analytics 请求失败: " + err.Error()
		return base
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		base.Error = "读取 Cloudflare Analytics 响应失败"
		return base
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		base.Error = fmt.Sprintf("Cloudflare Analytics 返回 HTTP %d", resp.StatusCode)
		return base
	}
	used, err := parseCloudflareRequests(data)
	if err != nil {
		base.Error = "解析 Cloudflare Analytics 响应失败: " + err.Error()
		return base
	}
	base.Success = true
	base.Used = used
	base.Remaining = remaining(base.Limit, used)
	base.UpdatedAt = now.Format(time.RFC3339)
	return base
}

func parseCloudflareRequests(data []byte) (int64, error) {
	var response struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Data struct {
			Viewer struct {
				Accounts []struct {
					Pages []struct {
						Sum struct {
							Requests int64 `json:"requests"`
						} `json:"sum"`
					} `json:"pagesFunctionsInvocationsAdaptiveGroups"`
					Workers []struct {
						Sum struct {
							Requests int64 `json:"requests"`
						} `json:"sum"`
					} `json:"workersInvocationsAdaptive"`
				} `json:"accounts"`
			} `json:"viewer"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return 0, err
	}
	if len(response.Errors) > 0 {
		return 0, errors.New(response.Errors[0].Message)
	}
	if len(response.Data.Viewer.Accounts) == 0 {
		return 0, errors.New("未找到 Cloudflare 账户数据")
	}
	var total int64
	for _, group := range response.Data.Viewer.Accounts[0].Pages {
		if group.Sum.Requests < 0 || group.Sum.Requests > math.MaxInt64-total {
			return 0, errors.New("Cloudflare 请求数无效")
		}
		total += group.Sum.Requests
	}
	for _, group := range response.Data.Viewer.Accounts[0].Workers {
		if group.Sum.Requests < 0 || group.Sum.Requests > math.MaxInt64-total {
			return 0, errors.New("Cloudflare 请求数无效")
		}
		total += group.Sum.Requests
	}
	return total, nil
}

func remaining(limit, used int64) int64 {
	if used >= limit {
		return 0
	}
	return limit - used
}

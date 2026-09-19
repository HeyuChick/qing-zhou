package officialusage

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func testPrivateKey(t *testing.T, pkcs8 bool) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var data []byte
	var kind string
	if pkcs8 {
		data, err = x509.MarshalPKCS8PrivateKey(key)
		kind = "PRIVATE KEY"
	} else {
		data = x509.MarshalPKCS1PrivateKey(key)
		kind = "RSA PRIVATE KEY"
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: data}))
}

func testClient(fn roundTripFunc) *http.Client { return &http.Client{Transport: fn} }

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestFetchOCIReadsTransferUsageAndSignsRequest(t *testing.T) {
	key := testPrivateKey(t, false)
	now := time.Date(2026, 9, 15, 8, 30, 0, 0, time.UTC)
	client := testClient(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "usageapi.ap-tokyo-1.oci.oraclecloud.com" || r.URL.Path != "/20200107/usage" {
			t.Fatalf("unexpected OCI endpoint %s", r.URL)
		}
		if got := r.URL.Query().Get("limit"); got != "1000" {
			t.Fatalf("limit=%q", got)
		}
		if got := r.Header.Get("Authorization"); !strings.Contains(got, `algorithm="rsa-sha256"`) || strings.Contains(got, key) {
			t.Fatalf("bad authorization header %q", got)
		}
		if got := r.Header.Get("X-Content-SHA256"); got == "" {
			t.Fatal("missing body digest")
		}
		var requestBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatal(err)
		}
		if got := requestBody["timeUsageEnded"]; got != "2026-09-15T00:00:00Z" {
			t.Fatalf("timeUsageEnded=%v", got)
		}
		return jsonResponse(200, `{"items":[
			{"service":"Networking","skuName":"Data Transfer Out","unit":"GB","attributedUsage":"1.5"},
			{"service":"Compute","skuName":"OCPU Hour","unit":"HOUR","attributedUsage":"99"},
			{"service":"Networking","skuName":"Outbound Transfer","unit":"MiB","computedQuantity":2}
		]}`), nil
	})
	usage := FetchOCI(context.Background(), client, OCIConfig{
		TenancyOCID: "ocid1.tenancy.oc1..example", UserOCID: "ocid1.user.oc1..example",
		Fingerprint: "aa:bb", Region: "ap-tokyo-1", PrivateKey: key, MonthlyLimitBytes: 2_000_000_000,
	}, now)
	if !usage.Success || usage.Used != 1_502_097_152 || usage.Remaining != 497_902_848 {
		t.Fatalf("unexpected OCI usage %#v", usage)
	}
}

func TestFetchOCIPaginatesAndPrefersAttributedUsage(t *testing.T) {
	key := testPrivateKey(t, false)
	page := 0
	client := testClient(func(r *http.Request) (*http.Response, error) {
		page++
		if page == 1 {
			if got := r.URL.Query().Get("page"); got != "" {
				t.Fatalf("first page token=%q", got)
			}
			response := jsonResponse(200, `{"items":[{"service":"Networking","skuName":"Data Transfer Out","unit":"GB","attributedUsage":"999","computedQuantity":2}]}`)
			response.Header.Set("opc-next-page", "next-page-token")
			return response, nil
		}
		if got := r.URL.Query().Get("page"); got != "next-page-token" {
			t.Fatalf("second page token=%q", got)
		}
		return jsonResponse(200, `{"items":[{"service":"Networking","skuName":"Data Transfer In","unit":"GB","computedQuantity":100},{"service":"Networking","skuName":"Egress","unit":"MiB","computedQuantity":3}]}`), nil
	})
	usage := FetchOCI(context.Background(), client, OCIConfig{
		TenancyOCID: "tenancy", UserOCID: "user", Fingerprint: "aa:bb", Region: "ap-tokyo-1", PrivateKey: key, MonthlyLimitBytes: 3_000_000_000,
	}, time.Date(2026, 9, 15, 8, 30, 0, 0, time.UTC))
	if !usage.Success || usage.Used != 999_003_145_728 || usage.ItemCount != 3 || usage.TransferItemCount != 2 || usage.RecognizedTransferItems != 2 || page != 2 {
		t.Fatalf("unexpected OCI usage %#v pages=%d", usage, page)
	}
}

func TestFetchOCIRejectsUnrecognizedTransferUnit(t *testing.T) {
	key := testPrivateKey(t, false)
	client := testClient(func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"items":[{"service":"Networking","skuName":"Data Transfer Out","unit":"GB-MONTH","computedQuantity":1}]}`), nil
	})
	usage := FetchOCI(context.Background(), client, OCIConfig{
		TenancyOCID: "tenancy", UserOCID: "user", Fingerprint: "aa:bb", Region: "ap-tokyo-1", PrivateKey: key,
	}, time.Date(2026, 9, 15, 8, 30, 0, 0, time.UTC))
	if usage.Success || !strings.Contains(usage.Error, "无法识别") {
		t.Fatalf("unexpected OCI usage %#v", usage)
	}
}

func TestFetchOCIParsesOracleOutboundGBMonths(t *testing.T) {
	key := testPrivateKey(t, false)
	client := testClient(func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"items":[{"service":"Virtual Cloud Network","skuName":"Outbound Data Transfer Zone 2","unit":"GB Months","attributedUsage":"172.233401500476"}]}`), nil
	})
	usage := FetchOCI(context.Background(), client, OCIConfig{
		TenancyOCID: "tenancy", UserOCID: "user", Fingerprint: "aa:bb", Region: "ap-tokyo-1", PrivateKey: key,
		MonthlyLimitBytes: 10_000_000_000_000,
	}, time.Date(2026, 9, 16, 8, 30, 0, 0, time.UTC))
	want := int64(172_233_401_500)
	if !usage.Success || usage.Used != want || usage.Remaining != 9_827_766_598_500 {
		t.Fatalf("unexpected Oracle GB Months usage %#v", usage)
	}
}

func TestFetchOCIRefusesUnprovenBalances(t *testing.T) {
	config := OCIConfig{TenancyOCID: "tenancy", UserOCID: "user", Fingerprint: "aa:bb", Region: "ap-tokyo-1", PrivateKey: testPrivateKey(t, false)}
	for _, body := range []string{
		`{}`, `{"items":null}`, `{"items":[]}`, `{"items":[]} {}`,
		`{"items":[{"service":"Compute","skuName":"OCPU","unit":"HOUR","computedQuantity":5}]}`,
		`{"items":[{"service":"Networking","skuName":"Data Transfer","unit":"GB","computedQuantity":5}]}`,
		`{"items":[{"service":"Networking","skuName":"Data Transfer Out","unit":"GB/s","computedQuantity":5}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			client := testClient(func(*http.Request) (*http.Response, error) { return jsonResponse(200, body), nil })
			usage := FetchOCI(context.Background(), client, config, time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC))
			if usage.Success || usage.Error == "" || usage.Remaining != 0 {
				t.Fatalf("unexpected usable balance: %#v", usage)
			}
		})
	}
}

func TestOCIUnitConversionIsExactAndStrict(t *testing.T) {
	for _, test := range []struct {
		quantity string
		unit     string
		want     int64
		valid    bool
	}{
		{"0.000000001", "GB", 1, true},
		{"2", "Gigabytes", 2_000_000_000, true},
		{"2", "Gigabyte outbound data transfer per month", 2_000_000_000, true},
		{"2", "Gigabytes outbound data transfer per month", 2_000_000_000, true},
		{"1", "MiB", 1_048_576, true},
		{"1", "GB/s", 0, false},
		{"1", "Gigabyte per hour", 0, false},
		{"1", "unknown-bytes", 0, false},
		{"1", "GB-MONTH", 0, false},
	} {
		quantity, err := usageQuantity("", json.Number(test.quantity))
		if err != nil {
			t.Fatal(err)
		}
		got, valid := quantityToBytes(quantity, test.unit)
		if got != test.want || valid != test.valid {
			t.Fatalf("%s %s: got %d/%v", test.quantity, test.unit, got, valid)
		}
	}
}

func TestFetchOCIRejectsPaginationCycles(t *testing.T) {
	calls := 0
	client := testClient(func(*http.Request) (*http.Response, error) {
		calls++
		response := jsonResponse(200, `{"items":[{"skuName":"Data Transfer Out","unit":"GB","computedQuantity":1}]}`)
		response.Header.Set("opc-next-page", []string{"first", "second", "first"}[(calls-1)%3])
		return response, nil
	})
	config := OCIConfig{TenancyOCID: "tenancy", UserOCID: "user", Fingerprint: "aa:bb", Region: "ap-tokyo-1", PrivateKey: testPrivateKey(t, false)}
	usage := FetchOCI(context.Background(), client, config, time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC))
	if usage.Success || calls != 3 || !strings.Contains(usage.Error, "分页循环") {
		t.Fatalf("usage=%#v calls=%d", usage, calls)
	}
}

func TestFetchOCIOverageMakesFreeBalanceZero(t *testing.T) {
	client := testClient(func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"items":[{"service":"Networking","skuName":"Outbound Data Transfer - Originating in APAC - First 10 TB / Month","unit":"Gigabyte outbound data transfer per month","attributedUsage":"9999"},{"service":"Networking","skuName":"Outbound Data Transfer - Originating in APAC - Over 10 TB / Month","unit":"Gigabyte outbound data transfer per month","attributedUsage":"1"}]}`), nil
	})
	config := OCIConfig{TenancyOCID: "tenancy", UserOCID: "user", Fingerprint: "aa:bb", Region: "ap-tokyo-1", PrivateKey: testPrivateKey(t, false), MonthlyLimitBytes: 10_000_000_000_000}
	usage := FetchOCI(context.Background(), client, config, time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC))
	if !usage.Success || !usage.OverageDetected || usage.Remaining != 0 || usage.Used != 10_000_000_000_000 {
		t.Fatalf("unexpected OCI overage usage %#v", usage)
	}
}

func TestFetchOCIOnFirstUTCDayDoesNotClaimFullBalance(t *testing.T) {
	usage := FetchOCI(context.Background(), nil, OCIConfig{
		TenancyOCID: "tenancy", UserOCID: "user", Fingerprint: "aa:bb", Region: "ap-tokyo-1", PrivateKey: testPrivateKey(t, false), MonthlyLimitBytes: 99,
	}, time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))
	if usage.Success || usage.Remaining != 0 || !strings.Contains(usage.Error, "余额待确认") {
		t.Fatalf("unexpected first-day usage %#v", usage)
	}
}

func TestOCIPrivateKeyParsesPKCS8(t *testing.T) {
	if _, err := parsePrivateKey(testPrivateKey(t, true)); err != nil {
		t.Fatalf("PKCS#8 key: %v", err)
	}
}

func TestParseOCITransferBytesSkipsUnsupportedUnits(t *testing.T) {
	used, err := parseOCITransferBytes([]byte(`{"items":[
		{"service":"Networking","skuName":"Outbound Data Transfer","unit":"GB-MONTH","attributedUsage":"1"},
		{"service":"Networking","skuName":"Egress","unit":"TB","attributedUsage":"0.25"}
	]}`))
	if err != nil || used != 250_000_000_000 {
		t.Fatalf("used=%d err=%v", used, err)
	}
}

func TestFetchCloudflareAddsPagesAndWorkers(t *testing.T) {
	now := time.Date(2026, 9, 15, 8, 30, 0, 0, time.UTC)
	client := testClient(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("Authorization"); got != "Bearer analytics-token" {
			t.Fatalf("unexpected auth %q", got)
		}
		return jsonResponse(200, `{"data":{"viewer":{"accounts":[{"pagesFunctionsInvocationsAdaptiveGroups":[{"sum":{"requests":12}}],"workersInvocationsAdaptive":[{"sum":{"requests":3}},{"sum":{"requests":5}}]}]}}}`), nil
	})
	usage := FetchCloudflare(context.Background(), client, CloudflareConfig{AccountID: "account-id", AnalyticsToken: "analytics-token", DailyRequestLimit: 100}, now)
	if !usage.Success || usage.Used != 20 || usage.Remaining != 80 {
		t.Fatalf("unexpected CF usage %#v", usage)
	}
}

func TestFetchCloudflareReportsGraphQLErrors(t *testing.T) {
	client := testClient(func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"errors":[{"message":"forbidden"}]}`), nil
	})
	usage := FetchCloudflare(context.Background(), client, CloudflareConfig{AccountID: "account-id", AnalyticsToken: "analytics-token"}, time.Now())
	if usage.Success || !strings.Contains(usage.Error, "forbidden") {
		t.Fatalf("unexpected CF error %#v", usage)
	}
}

func TestFetchCloudflareReportsHTTPFailures(t *testing.T) {
	client := testClient(func(*http.Request) (*http.Response, error) { return jsonResponse(403, `{}`), nil })
	usage := FetchCloudflare(context.Background(), client, CloudflareConfig{AccountID: "account-id", AnalyticsToken: "analytics-token"}, time.Now())
	if usage.Success || !strings.Contains(usage.Error, "HTTP 403") {
		t.Fatalf("unexpected CF error %#v", usage)
	}
}

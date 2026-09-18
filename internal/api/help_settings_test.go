package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHelpDocsSettingsRoundTripIntoPublicConfig(t *testing.T) {
	a, st := newUserEditAPI(t)
	w := putSettings(a, `{"help_docs_mode":"external","help_docs_url":" https://docs.example.com/guide "}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if got, _ := st.GetSetting("help_docs_url"); got != "https://docs.example.com/guide" {
		t.Fatalf("stored URL = %q", got)
	}

	public := httptest.NewRecorder()
	a.handleConfig(public, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	var response struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(public.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data["help_docs_mode"] != "external" || response.Data["help_docs_url"] != "https://docs.example.com/guide" {
		t.Fatalf("public help config = %#v", response.Data)
	}
}

func TestHelpDocsSettingsRejectUnsafeOrMissingExternalURLBeforeWriting(t *testing.T) {
	a, st := newUserEditAPI(t)
	if err := st.SetSetting("site_name", "before"); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"site_name":"after","help_docs_mode":"external","help_docs_url":"javascript:alert(1)"}`,
		`{"site_name":"after","help_docs_mode":"external","help_docs_url":""}`,
		`{"site_name":"after","help_docs_mode":"somewhere","help_docs_url":"https://docs.example.com"}`,
	} {
		w := httptest.NewRecorder()
		a.handlePutSettings(w, httptest.NewRequest(http.MethodPut, "/api/admin/settings", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status %d, response %s", body, w.Code, w.Body.String())
		}
		if got, _ := st.GetSetting("site_name"); got != "before" {
			t.Fatalf("invalid help config partially wrote settings: site_name=%q", got)
		}
	}
}

func TestBrandIconSettingsRoundTripIntoPublicConfig(t *testing.T) {
	a, st := newUserEditAPI(t)
	const icon = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUg=="
	if w := putSettings(a, `{"site_name":"品牌面板","brand_icon_data_uri":"`+icon+`"}`); w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if got, _ := st.GetSetting(brandIconSetting); got != icon {
		t.Fatalf("stored icon = %q", got)
	}

	public := httptest.NewRecorder()
	a.handleConfig(public, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	var response struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(public.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data["brand_icon_data_uri"] != icon {
		t.Fatalf("public icon = %#v", response.Data["brand_icon_data_uri"])
	}
}

func TestBrandIconSettingsRejectInvalidDataBeforeWriting(t *testing.T) {
	a, st := newUserEditAPI(t)
	if err := st.SetSetting("site_name", "before"); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"site_name":"after","brand_icon_data_uri":"data:image/svg+xml;base64,PHN2Zz4="}`,
		`{"site_name":"after","brand_icon_data_uri":"data:image/png;base64,not-base64"}`,
		`{"site_name":"after","brand_icon_data_uri":"data:image/jpeg;base64,iVBORw0KGgo="}`,
	} {
		w := putSettings(a, body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status %d, response %s", body, w.Code, w.Body.String())
		}
		if got, _ := st.GetSetting("site_name"); got != "before" {
			t.Fatalf("site name changed to %q despite invalid icon", got)
		}
	}
}

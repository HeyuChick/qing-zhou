package subconv

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"qingzhou/internal/singbox"
)

type websocketOptions struct {
	path            string
	headers         map[string][]string
	maxEarlyData    int
	earlyDataHeader string
}

func (p *Proxy) transportNetwork() string {
	if p.Protocol == "vmess" {
		return str(p.VMess["net"])
	}
	return p.param("type")
}
func (p *Proxy) wsParam(keys ...string) string {
	if value := p.param(keys...); value != "" {
		return value
	}
	for _, key := range keys {
		if value := str(p.VMess[key]); value != "" {
			return value
		}
	}
	return ""
}

// URI/VMess JSON do not have a universal arbitrary-headers/early-data schema.
// qz-ws-headers is an explicit lossless envelope for Qingzhou native-format
// conversion. Legacy host/path and max_early_data keys remain accepted.
func (p *Proxy) websocket() (websocketOptions, error) {
	w := websocketOptions{path: p.wsParam("path"), earlyDataHeader: p.wsParam("early_data_header_name", "eh")}
	var value any
	if encoded := p.param("qz-ws-headers"); encoded != "" {
		if err := json.Unmarshal([]byte(encoded), &value); err != nil {
			return w, fmt.Errorf("invalid WebSocket header envelope")
		}
	} else if p.VMess != nil {
		value = p.VMess["qz-ws-headers"]
	}
	var err error
	w.headers, err = singbox.NormalizeWSHeaders(value)
	if err != nil {
		return w, err
	}
	if w.headers == nil {
		w.headers = map[string][]string{}
	}
	if host := p.wsParam("host"); host != "" {
		if existing := w.headers["Host"]; len(existing) > 0 && existing[0] != host {
			return w, fmt.Errorf("conflicting WebSocket Host")
		}
		w.headers["Host"] = []string{host}
	}
	w.headers, err = singbox.NormalizeWSHeaders(w.headers)
	if err != nil {
		return w, err
	}
	if value := p.wsParam("max_early_data", "ed"); value != "" {
		size, e := strconv.ParseUint(value, 10, 32)
		if e != nil {
			return w, fmt.Errorf("invalid WebSocket early data size")
		}
		w.maxEarlyData = int(size)
	}
	if w.earlyDataHeader != "" {
		if _, err = singbox.NormalizeWSHeaders(map[string]string{w.earlyDataHeader: ""}); err != nil {
			return w, err
		}
	}
	if strings.ContainsAny(w.path, "\r\n\x00") {
		return w, fmt.Errorf("invalid WebSocket path")
	}
	// Path query is application data. Never strip ?ed=, re-escape %2F, or infer
	// early-data semantics from it; explicit transport fields are unambiguous.
	return w, nil
}

func (w websocketOptions) singbox() map[string]any {
	out := map[string]any{"type": "ws"}
	if w.path != "" {
		out["path"] = w.path
	}
	headers := map[string]any{}
	for key, values := range w.headers {
		if len(values) == 1 {
			headers[key] = values[0]
		} else {
			headers[key] = values
		}
	}
	if len(headers) > 0 {
		out["headers"] = headers
	}
	if w.maxEarlyData > 0 {
		out["max_early_data"] = w.maxEarlyData
		if w.earlyDataHeader != "" {
			out["early_data_header_name"] = w.earlyDataHeader
		}
	}
	return out
}
func (w websocketOptions) singleHeaders() (map[string]any, bool) {
	headers := map[string]any{}
	for key, values := range w.headers {
		if len(values) != 1 {
			return nil, false
		}
		headers[key] = values[0]
	}
	return headers, true
}

func clashWSToQuery(m map[string]any, q url.Values) bool {
	wo, ok := m["ws-opts"].(map[string]any)
	if !ok {
		return true
	}
	headers, err := singbox.NormalizeWSHeaders(wo["headers"])
	if err != nil {
		return false
	}
	if path := str(wo["path"]); path != "" {
		q.Set("path", path)
	}
	if host := headers["Host"]; len(host) > 0 {
		q.Set("host", host[0])
	}
	extended := false
	for key := range headers {
		if key != "Host" {
			extended = true
		}
	}
	if extended {
		raw, _ := json.Marshal(headers)
		q.Set("qz-ws-headers", string(raw))
	}
	if ed := str(wo["max-early-data"]); ed != "" {
		q.Set("max_early_data", ed)
	}
	if header := str(wo["early-data-header-name"]); header != "" {
		q.Set("early_data_header_name", header)
	}
	return true
}

func (w websocketOptions) mihomoNormalizesPath() bool {
	u, err := url.Parse(w.path)
	if err != nil {
		return true
	}
	if value := u.Query().Get("ed"); value != "" {
		if _, err := strconv.Atoi(value); err == nil {
			return true
		}
	}
	normalized := url.URL{Path: u.Path}
	return u.EscapedPath() != normalized.EscapedPath()
}

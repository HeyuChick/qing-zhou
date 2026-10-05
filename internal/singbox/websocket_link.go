package singbox

import (
	"encoding/json"
	"fmt"
	"net/http"

	"golang.org/x/net/http/httpguts"
)

// NormalizeWSHeaders accepts sing-box's scalar-or-list header representation.
// Case collisions and ambiguous Host arrays are rejected rather than picking a
// map-iteration-dependent routing target. It never folds distinct header lines.
func NormalizeWSHeaders(value any) (map[string][]string, error) {
	if value == nil {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var input map[string]any
	if err = json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("invalid WebSocket headers")
	}
	out := map[string][]string{}
	for key, value := range input {
		if !httpguts.ValidHeaderFieldName(key) {
			return nil, fmt.Errorf("invalid WebSocket header name")
		}
		canonical := http.CanonicalHeaderKey(key)
		if _, exists := out[canonical]; exists {
			return nil, fmt.Errorf("ambiguous WebSocket header casing")
		}
		var values []string
		switch v := value.(type) {
		case string:
			values = []string{v}
		case []any:
			for _, item := range v {
				s, ok := item.(string)
				if !ok {
					return nil, fmt.Errorf("invalid WebSocket header value")
				}
				values = append(values, s)
			}
		default:
			return nil, fmt.Errorf("invalid WebSocket header value")
		}
		if len(values) == 0 || canonical == "Host" && len(values) != 1 {
			return nil, fmt.Errorf("ambiguous WebSocket header values")
		}
		for _, v := range values {
			if !httpguts.ValidHeaderFieldValue(v) {
				return nil, fmt.Errorf("invalid WebSocket header value")
			}
		}
		out[canonical] = values
	}
	return out, nil
}

// Host already has a conventional URI/VMess field. Do not add an extension
// for Host-only historical links, keeping their canonical identity stable.
func extendedWSHeaders(headers map[string][]string) bool {
	for name, values := range headers {
		if name != "Host" || len(values) != 1 {
			return true
		}
	}
	return false
}

package store

// Mirror profile precedence over inline options without treating tls_id=0 as
// proof of plaintext. Unknown inline shapes are omitted from public export;
// they must never be advertised with security=none by accident.
func shareLinkTLSState(tlsID int64, profile, options map[string]interface{}) (enabled, disabled bool, server map[string]interface{}, valid bool) {
	if tlsID != 0 {
		if value, ok := profile["enabled"].(bool); ok && !value {
			return false, true, profile, true
		}
		return true, false, profile, true // Unresolved profiles must never cause a plaintext downgrade
	}
	raw, exists := options["tls"]
	if !exists || raw == nil {
		return false, true, nil, true
	}
	inline, ok := raw.(map[string]interface{})
	if !ok {
		return false, false, nil, false
	}
	value, ok := inline["enabled"].(bool)
	if !ok {
		return false, false, nil, false
	}
	return value, !value, inline, true
}

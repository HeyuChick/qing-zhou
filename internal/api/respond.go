package api

import (
	"encoding/json"
	"net/http"
)

// J is a convenience alias for ad-hoc JSON objects.
type J = map[string]any

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// ok writes the unified success envelope {code:0, msg:"", data:...}.
func ok(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, J{"code": 0, "msg": "", "data": data})
}

// fail writes the unified error envelope with the HTTP status as code.
func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, J{"code": status, "msg": msg, "data": nil})
}

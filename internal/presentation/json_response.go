package presentation

import (
	"encoding/json"
	"net/http"
)

func writeJSON(w http.ResponseWriter, status int, contentType string, body interface{}) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

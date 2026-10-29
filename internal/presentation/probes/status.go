package probes

import (
	"net/http"

	"keyloop-coding-challenge/internal/presentation/httpkit"
)

// statusHandler serves the consumer status. It is unauthenticated and includes raw
// error strings, so the projector binds its probe server to localhost by default.
func statusHandler(status ConsumerStatus) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		httpkit.WriteJSON(w, http.StatusOK, status())
	}
}

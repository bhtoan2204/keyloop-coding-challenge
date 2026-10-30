package presentation_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"keyloop-coding-challenge/internal/presentation"
)

func TestMetricsEndpoints(t *testing.T) {
	api := httptest.NewServer(presentation.NewHandler(stubService{}, &stubQueries{}, nil, discard))
	t.Cleanup(api.Close)
	do(t, api, call{method: "GET", path: vehicles + "?agng=true"})
	body := do(t, api, call{method: "GET", path: "/metrics"}).rawBody
	for _, want := range []string{
		`inventory_http_requests_total{method="GET",route="GET /v1/dealerships/{dealershipId}/vehicles",status="400"}`,
		`inventory_http_problems_total{code="INVALID_QUERY",route="GET /v1/dealerships/{dealershipId}/vehicles"}`,
		`inventory_http_request_duration_seconds_bucket{method="GET",route="GET /v1/dealerships/{dealershipId}/vehicles"`,
		"go_goroutines",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("API /metrics lacks %s", want)
		}
	}
}

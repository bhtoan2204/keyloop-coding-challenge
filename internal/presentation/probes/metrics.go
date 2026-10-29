package probes

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// RegisterMetrics adds GET /metrics in the Prometheus text format.
func RegisterMetrics(mux *http.ServeMux, gatherer prometheus.Gatherer) {
	mux.Handle("GET /metrics", promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{}))
}

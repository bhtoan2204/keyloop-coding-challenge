package httpkit

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Labels stay bounded: routes are ServeMux patterns ("POST /v1/dealerships/{dealershipId}/vehicles"),
// never raw paths (the routers' catch-all makes every unknown path "/"), and methods
// outside the standard set are folded into "OTHER".
var (
	requestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "inventory_http_requests_total",
		Help: "HTTP requests by route pattern and status code.",
	}, []string{"method", "route", "status"})
	requestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "inventory_http_request_duration_seconds",
		Help:    "HTTP request latency by route pattern.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"})
	problemsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "inventory_http_problems_total",
		Help: "Problem responses by route pattern and problem code (e.g. VEHICLE_NOT_AGING, VERSION_MISMATCH).",
	}, []string{"route", "code"})
)

func observeRequest(r *http.Request, status int, elapsed time.Duration) {
	route := routeLabel(r)
	method := methodLabel(r.Method)
	requestsTotal.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
	requestDuration.WithLabelValues(method, route).Observe(elapsed.Seconds())
}

// methodLabel bounds the method label: the method is client-controlled, and every
// distinct value would create permanent series.
func methodLabel(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return method
	}
	return "OTHER"
}

func observeProblem(r *http.Request, code string) {
	problemsTotal.WithLabelValues(routeLabel(r), code).Inc()
}

func routeLabel(r *http.Request) string {
	if r.Pattern == "" {
		return "unmatched"
	}
	return r.Pattern
}

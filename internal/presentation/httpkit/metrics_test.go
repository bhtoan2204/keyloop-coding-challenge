package httpkit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestMetricLabelsAreBounded: neither the client-chosen method nor an unmatched path
// may create new series.
func TestMetricLabelsAreBounded(t *testing.T) {
	before := testutil.CollectAndCount(requestsTotal)
	for _, method := range []string{"ZZMETHOD1", "ZZMETHOD2", "PROPFIND"} {
		r := httptest.NewRequest(method, "/random/path/"+method, nil)
		observeRequest(r, http.StatusNotFound, time.Millisecond)
	}
	if got := testutil.ToFloat64(requestsTotal.WithLabelValues("OTHER", "unmatched", "404")); got < 3 {
		t.Fatalf("unknown methods not folded into OTHER: %v", got)
	}
	if added := testutil.CollectAndCount(requestsTotal) - before; added > 1 {
		t.Fatalf("%d new series for 3 unknown methods on 3 paths, want at most 1", added)
	}
}

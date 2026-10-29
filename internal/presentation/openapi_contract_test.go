package presentation_test

import (
	"bufio"
	"bytes"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"keyloop-coding-challenge/api"
	"keyloop-coding-challenge/internal/presentation"
)

var (
	specPath   = regexp.MustCompile(`^  (/\S*):$`)
	specMethod = regexp.MustCompile(`^    (get|post|put|patch|delete):$`)
)

// specOperations lists "METHOD /path" for every operation in api/openapi.yaml.
func specOperations(t *testing.T) []string {
	t.Helper()
	var ops []string
	path, inPaths := "", false
	scanner := bufio.NewScanner(bytes.NewReader(api.OpenAPI))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "paths:":
			inPaths = true
		case inPaths && len(line) > 0 && line[0] != ' ':
			inPaths = false
		case inPaths && specPath.MatchString(line):
			path = specPath.FindStringSubmatch(line)[1]
		case inPaths && specMethod.MatchString(line):
			ops = append(ops, strings.ToUpper(specMethod.FindStringSubmatch(line)[1])+" "+path)
		}
	}
	if len(ops) == 0 {
		t.Fatal("no operations found in api/openapi.yaml")
	}
	return ops
}

// TestOpenAPIOperationsAreRouted: every documented operation reaches a real route; an
// endpoint removed or renamed in the code without updating the contract fails here.
func TestOpenAPIOperationsAreRouted(t *testing.T) {
	srv := httptest.NewServer(presentation.NewHandler(stubService{}, &stubQueries{}, nil, discard))
	t.Cleanup(srv.Close)
	sample := strings.NewReplacer("{dealershipId}", "d-001", "{vehicleId}", "v1", "{actionId}", "a1")
	ops := specOperations(t)
	for _, op := range ops {
		method, path, _ := strings.Cut(op, " ")
		got := do(t, srv, call{method: method, path: sample.Replace(path)})
		if got.status == 405 || got.code() == "NOT_FOUND" {
			t.Errorf("%s is documented but not routed: %d %s", op, got.status, got.rawBody)
		}
	}
	if len(ops) != 14 {
		t.Errorf("%d documented operations, want 14 (5 commands, 4 queries, 5 operational): %v", len(ops), ops)
	}
}

func TestOpenAPIDocumentIsServed(t *testing.T) {
	srv := httptest.NewServer(presentation.NewHandler(stubService{}, &stubQueries{}, nil, discard))
	t.Cleanup(srv.Close)
	spec := do(t, srv, call{method: "GET", path: "/openapi.yaml"})
	if spec.status != 200 || spec.header.Get("Content-Type") != "application/yaml" || spec.rawBody != string(api.OpenAPI) {
		t.Fatalf("/openapi.yaml: %d %s", spec.status, spec.header.Get("Content-Type"))
	}
	ui := do(t, srv, call{method: "GET", path: "/docs"})
	if ui.status != 200 || !strings.Contains(ui.rawBody, `url: "/openapi.yaml"`) {
		t.Fatalf("/docs: %d %.200s", ui.status, ui.rawBody)
	}
	csp := ui.header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "'sha256-") || strings.Contains(csp, "unsafe-eval") {
		t.Fatalf("/docs CSP %q", csp)
	}
	if strings.Count(ui.rawBody, `integrity="sha384-`) != 2 {
		t.Fatal("/docs loads CDN assets without Subresource Integrity")
	}
}

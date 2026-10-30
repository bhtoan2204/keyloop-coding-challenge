//go:build e2e

// Package e2e_test drives the Keyloop business flow against a running system (the
// docker compose stack or `make run-api`): commands through the API, reads from the
// read model the same transaction updated. It fails when E2E_BASE_URL's API is
// unreachable.
package e2e_test

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

type client struct {
	t    *testing.T
	base string
}

type response struct {
	status  int
	header  http.Header
	body    map[string]interface{}
	rawBody string
}

func newClient(t *testing.T) *client {
	t.Helper()
	base := os.Getenv("E2E_BASE_URL")
	if base == "" {
		base = "http://127.0.0.1:8080"
	}
	c := &client{
		t:    t,
		base: base,
	}
	if r := c.do("GET", "/readyz", nil, nil); r.status != http.StatusOK {
		t.Fatalf("API at %s is not ready (%d): start the stack first (README)", base, r.status)
	}
	return c
}

// with returns the client bound to a subtest, so a failure is reported (and stops) there.
func (c *client) with(t *testing.T) *client {
	return &client{
		t:    t,
		base: c.base,
	}
}

func (c *client) do(method, path string, body interface{}, headers map[string]string) response {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		c.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Actor-Id", "e2e-manager")
	for k, v := range headers {
		if v == "" {
			req.Header.Del(k)
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := response{
		status:  resp.StatusCode,
		header:  resp.Header,
		rawBody: string(raw),
	}
	_ = json.Unmarshal(raw, &out.body)
	return out
}

// expect fails unless the response has the status (and, for problems, the code).
func (c *client) expect(r response, status int, code string) response {
	c.t.Helper()
	if r.status != status || (code != "" && r.body["code"] != code) {
		c.t.Fatalf("got %d %s, want %d %s", r.status, r.rawBody, status, code)
	}
	return r
}

// immediately fails unless cond already holds: commands update the read model in their
// own transaction, so a read after a successful write must see it without waiting.
func (c *client) immediately(what string, cond func() bool) {
	c.t.Helper()
	if !cond() {
		c.t.Fatalf("read model does not show %s right after the command", what)
	}
}

func randomTag(t *testing.T) string {
	t.Helper()
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

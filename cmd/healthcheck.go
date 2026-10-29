package main

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// runHealthcheck GETs url and fails unless it answers 2xx. Container health checks use
// it because the runtime image has no shell or curl.
func runHealthcheck(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s: status %d", url, resp.StatusCode)
	}
	return nil
}

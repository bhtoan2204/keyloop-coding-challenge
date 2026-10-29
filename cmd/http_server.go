package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// serveUntilCanceled serves until ctx is cancelled (SIGINT/SIGTERM), then shuts the
// server down gracefully, letting in-flight requests finish for up to 10 seconds.
func serveUntilCanceled(ctx context.Context, log *slog.Logger, srv *http.Server) error {
	errs := make(chan error, 1)
	go func() {
		log.Info("http server listening", "addr", srv.Addr)
		errs <- srv.ListenAndServe()
	}()
	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	if err := <-errs; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	log.Info("stopped")
	return nil
}

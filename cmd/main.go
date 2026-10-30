// Command main runs the inventory service.
//
//	main migrate              apply the event_store and read_model migrations (admin role)
//	main api                  serve the REST API (commands: writer role; queries: read-only role)
//	main rebuild-projections  rebuild read_model from the full event_store history (rebuild role)
//	main healthcheck URL      exit 0 when URL answers 2xx (container health checks)
//
// Configuration comes from the environment; see .env.example.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	_ "time/tzdata" // dealership time zones must resolve in minimal containers too
)

const usage = "usage: main api|migrate|rebuild-projections|healthcheck URL"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel()}))
	if len(args) == 2 && args[0] == "healthcheck" {
		if err := runHealthcheck(context.Background(), args[1]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch args[0] {
	case "api":
		err = runAPI(ctx, log)
	case "migrate":
		err = runMigrate(ctx, log)
	case "rebuild-projections":
		err = runRebuild(ctx, log)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q; %s\n", args[0], usage)
		return 2
	}
	if err != nil {
		log.Error("exiting", "command", args[0], "error", err.Error())
		return 1
	}
	return 0
}

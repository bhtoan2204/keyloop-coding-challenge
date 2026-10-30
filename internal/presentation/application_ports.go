package presentation

import (
	"keyloop-coding-challenge/internal/presentation/commands"
	"keyloop-coding-challenge/internal/presentation/probes"
	"keyloop-coding-challenge/internal/presentation/queries"
)

// The service ports are declared by the packages that use them; these aliases keep
// the presentation entry points' signatures for existing callers.
type (
	// CommandService is implemented by *command.Service.
	CommandService = commands.Service
	// QueryService is implemented by *query.Service.
	QueryService = queries.Service
	// ReadinessCheck reports whether the process's dependencies are reachable.
	ReadinessCheck = probes.ReadinessCheck
)

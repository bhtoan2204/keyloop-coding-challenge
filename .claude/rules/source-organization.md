---
paths:
  - "**/*.go"
---
# Source organization and readability rules

Apply to all Go code in this repository, new and changed.

## One responsibility per file
- Each file has one clear, cohesive responsibility; its name says which.
- Keep these apart: command/query definitions, shared contracts (ports, errors), transport DTOs,
  response mappers, HTTP handlers, middleware, routing, request parsing, error mapping.
- Prefer **one business operation per file** when operations evolve independently
  (e.g. `stock_vehicle.go`, `propose_action_handler.go`, `vehicle_inventory_sell.go`).
  Keep a small set of closely related types together when that reads better.
- No catch-all files (`commands.go`, `queries.go`, `handlers.go`, `utils.go`, `helpers.go`,
  `common.go`) that collect unrelated code. Shared helpers go in a file named after their real
  responsibility (`query_params.go`, `request_body.go`, `vehicle_rows.go`).
- Infrastructure: keep queries, row scanning, transactions/locking and migrations in separate files.
- Split files within the same package first. Create a new package only for a real boundary
  (bounded context, dependency direction, import cycle), never just to make files smaller.
- No package per command, no generic command bus, no interface without a second implementation
  or a test need, no speculative abstraction.

## Size: review triggers, not limits
- A Go file approaching 250–300 lines: check it for multiple responsibilities.
- A function approaching 40–60 lines: check for separable concerns (parsing, mapping, I/O, policy).
- A cohesive longer file is fine when splitting would make navigation worse (e.g. a table of
  migrations, a long table-driven test). Never split into meaningless tiny files to hit a number.

## Readability over fewer lines
- Multiline struct literals with several fields: **one field per line**. gofmt does not split
  fields that share a line; insert the line breaks yourself, then run gofmt.

      command.StockVehicle{
          DealershipID: r.PathValue("dealershipId"),
          VehicleID:    req.VehicleID,
          VIN:          req.VIN,
      }

- Same for constructors, long calls (one argument per line when it does not fit comfortably),
  error mapping tables, SQL argument lists, test fixture builders, configuration structs, event
  payload construction and response DTO mapping.
- Short, clear literals may stay on one line (`&Result{Vehicle: v, BusinessDate: bd}`).
  Table-driven test cases may keep one case per line.
- No multiple unrelated assignments or operations on one line.
- Prefer guard clauses over nested `if` chains. Use an intermediate variable when it names
  intent or removes a nested expression (`cmd := command.ProposeAction{...}`; then call).

## Comments
- Explain *why* when it is not obvious; do not narrate the code.
- Keep comments about invariants, transaction boundaries, locking and concurrency.
- Remove stale section-divider comments when code moves.

## Tests
- Organise tests by behaviour/feature (file names say what is tested).
- Moving or reformatting tests must keep every assertion.

## When refactoring structure
- Behaviour-preserving only: same exported API, events, SQL, HTTP contracts and error mapping.
- Move whole declarations (with their comments); fix imports with
  `goimports -local keyloop-coding-challenge`.
- Verify: `go doc -all` of each package unchanged, same test list, full regression green.

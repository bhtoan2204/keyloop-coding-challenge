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
  responsibility (`query_params.go`, `request_body.go`, `vehicle_filters.go`, `mappers.go`).
- Infrastructure: keep queries, row scanning, transactions/locking and migrations in separate files.
- Split files within the same package first. Create a new package only for a real boundary
  (see "Package organization"), never just to make files smaller.
- No package per command, no generic command bus, no interface without a second implementation
  or a test need, no speculative abstraction.

## Reusability
1. Prefer reusable, composable functions over duplicated use-case logic.
2. Design components around stable responsibilities and explicit interfaces.
3. Extract shared query predicates, mappers, validation and transaction helpers when there are real
   repeated uses (e.g. `agingCutoff` serves both the list and the summary query).
4. Prefer constructor-based dependency injection over hard-coded concrete dependencies.
5. Write repository implementations against narrowly scoped persistence contracts.
6. Use Go generics only when several concrete types share the same real algorithm and type safety
   benefits.
7. Avoid `interface{}`/`any`, reflection and generic CRUD frameworks without clear justification.
8. Keep business-specific policies explicit and discoverable (named scopes, named constants).
9. Do not couple query filters to HTTP parsing or to GORM persistence details: filters are application
   values (`query.VehicleFilter`), scopes translate them in infrastructure.
10. New read models and repository implementations must be addable without rewriting domain logic
    (add a projector, a model, a repository and a migration; replay the history with a rebuild).

## Separation of concerns
- Repository and port interfaces belong to their domain/application consumers (`command.VehicleStore`,
  `query.ReadModel`, `projection.Store`); infrastructure implements them.
- Database models belong to infrastructure (`viewstore/models`); application models do not import GORM.
- SQL belongs to infrastructure. Transaction boundaries belong to infrastructure coordination
  (`AggregateStore`, `viewstore.Postgres`, `RunExclusive`), never to an individual repository.
- Business invariants belong to the domain. HTTP parsing and mapping belong to presentation.
- Mapping between persistence and application models is explicit and type-safe (one function per
  direction, no reflection).

## Package organization
- Group closely related files into cohesive packages. A directory that collects many
  independent features (dozens of unrelated files) must be reviewed for a package split.
- Prefer meaningful boundaries: commands, queries, shared transport infrastructure,
  operational probes; in other layers, the same idea (per bounded context, per adapter).
- Never a package per file or per handler, and never a package only to meet a file count.
- Descriptive package names; one clear dependency direction:
  - a child package never imports its parent composition package;
  - shared helpers (e.g. `presentation/httpkit`) never import the feature packages that use them;
  - feature packages do not import each other sideways;
  - no import cycles.
- Small, intentional package APIs: export only what another package calls. Each feature
  package receives only the dependencies it needs (no "god" struct with every service).
- Keep existing public entry points stable when moving code; use a compatibility facade
  (type aliases, thin constructors) in the old package rather than breaking callers.
- Pin the direction with a test when the compiler cannot (it only rejects cycles), as
  `presentation/package_dependencies_test.go` does.
- Apply the same principles to application, domain, infrastructure and messaging packages
  as they grow.

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
- Keep long calls readable (one argument per line when they do not fit comfortably).
- No multiple unrelated assignments or operations on one line.
- Prefer understandable code over compact code; no speculative abstraction.
- Prefer meaningful identifiers over abbreviations (`dealershipID`, `businessDate`, not `d`, `bd`)
  outside very short scopes.
- Run `gofmt` and `goimports -local keyloop-coding-challenge` after editing.
- Prefer guard clauses over nested `if` chains. Use an intermediate variable when it names
  intent or removes a nested expression (`cmd := command.ProposeAction{...}`; then call).

## Query construction
- Compose queries from small named scopes (`inDealership`, `withStatus`, `withAgeBounds`, `afterKeyset`,
  `orderByAge`) instead of accumulating unrelated SQL fragments in one method.
- Every user value is a bound parameter; identifiers and ORDER BY are fixed in code, never taken from input.
- PostgreSQL-specific SQL (`FILTER`, advisory locks, row comparisons) is fine where it is justified; check
  the plan (`EXPLAIN`) when a predicate changes. A prettier abstraction with a worse plan is a regression.
- Avoid deeply nested conditional query construction.

## Anti-overengineering
- No abstraction without a real responsibility; no repository interface that only wraps GORM methods.
- No package per struct, no generic command bus, no universal query engine, no unnecessary adapter layers.
- No arbitrary file-count constraints. The goal is reusable, understandable code, not maximum abstraction.

## Comments
A comment must say something the code cannot. Keep (shortened if long):
- non-obvious business invariants;
- transaction boundaries and atomicity guarantees;
- concurrency, locks, deadlocks, optimistic concurrency;
- event replay, snapshots, schema compatibility;
- idempotency, recovery and rebuild semantics;
- security-sensitive decisions, non-obvious performance trade-offs, intentional workarounds;
- exported API contracts that the signature does not make obvious.

Remove or shorten: comments that repeat the function name or the type signature, narrate
simple assignments or calls, divide sections, or introduce self-explanatory DTOs and
helpers at length. Prefer a clear name or a small function to an explanatory comment. Do not
delete comments wholesale: keep GoDoc that states a contract and every explanation of a
subtle correctness guarantee, and update comments that a move made stale.

## Tests
- Organise tests by behaviour/feature (file names say what is tested).
- Moving or reformatting tests must keep every assertion.

## When refactoring structure
- Behaviour-preserving only: same exported API, events, SQL, HTTP contracts and error mapping.
- Move whole declarations (with their comments); fix imports with
  `goimports -local keyloop-coding-challenge`.
- Verify: `go doc -all` of each package unchanged, same test list, full regression green.

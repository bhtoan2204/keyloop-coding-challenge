---
paths:
  - "cmd/**/*.go"
  - "internal/**/*.go"
  - "package/**/*.go"
  - "go.mod"
---
# Architecture rules

- **Reuse first.** Before adding a type, interface or package, search for an existing one
  (`AggregateRoot`, `AggregateStore`, `repos.EventStore`, serializer, `Projection`, `Projector`,
  `ViewStore`). Extend it. Never create a parallel framework, store or aggregate base.
- **Package layout is fixed:** `internal/{domain,application,infrastructure,presentation}`,
  `package/eventsourcing`, `cmd/main.go`. Fill the existing empty packages; do not move files.
- **Dependency direction:** presentation → application → domain. Infrastructure implements
  interfaces that application declares. `internal/domain/**` imports only the standard library,
  `internal/domain`, `internal/domain/events` and `package/eventsourcing`.
- **Domain purity:** no I/O, SQL, HTTP, logging or `time.Now()` in `internal/domain/**`. The
  business date and generated IDs come in through command parameters.
- **Minimal change:** prefer a localized edit to a rewrite. A refactor needs a stated reason
  (failing test, reproduced bug, or a compatibility need) and keeps public APIs where possible.
  `package/eventsourcing` changes require a failing test first.
- **No speculative abstraction:** one implementation = no interface, unless a test fake needs it.
  No generic repositories, registries or plugin points.
- **Explicit transactions:** write-side multi-step persistence goes through `WithTransaction`.
  Never span a transaction across MySQL and Postgres.
- **Context:** every I/O function takes `ctx context.Context` first and passes it down.
- **Errors:** wrap with `%w` and context (aggregate ID, version). Domain errors are sentinels in
  `internal/domain/errors.go`; map them to HTTP only in `internal/presentation`.
- **Dependencies:** add a module only in the phase that plans it (`docs/07`). No Redis or extra
  services. Do not wire `OutboxProjector`, `cache` or `package/redis`, and do not delete them.
- **Docs follow code:** when the implementation deviates from `docs/0x`, record it in that doc.

# Testing rules

- **Every business invariant has a test that rejects and one that accepts.** Rejections assert the
  error with `errors.Is`, that no pending event was recorded, and that state is unchanged.
- **Boundaries are explicit:** aging at 89/90/91 days, leap years, year boundary, time zones, DST.
- **Replay:** commands → `LoadFromHistory` into a fresh aggregate must give the same state; invalid
  sequences must fail with `domain.ErrInvalidEventSequence`; replay creates no pending events.
- **Snapshots:** JSON round-trip compares the whole aggregate struct; at least one test restores
  through the real `AggregateStore` after the covered events are gone.
- **Persistence:** SQLite tests (attached database `event_store`, `internal/testsupport/sqlitetest`)
  are fast checks, not proof of PostgreSQL behaviour. PostgreSQL behaviour (SQLSTATEs, locking,
  advisory locks, DDL, grants) needs database-backed tests behind `-tags integration` using
  `PG_TEST_DSN` (each test creates and drops its own database and roles, `internal/testsupport/pgtest`).
- **Concurrency & transactions:** N goroutines on one aggregate → exactly one winner, losers get
  `ErrConcurrencyConflict`, versions contiguous. Inject a failure mid-transaction (including in the
  projection) → no event, no CAS, no read-model change, pending events kept.
- **Always run** `gofmt -l .`, `go vet ./...`, `go test -count=1 ./...`, `go test -race -count=1 ./...`.
- **Tests must be able to fail.** For important rules, mutate the rule on a scratch copy and confirm a
  test fails. Never weaken an assertion to make a test pass; if a legacy test contradicts a valid
  invariant, adapt it, keep its intent, and say so.
- **Deterministic tests:** no sleeps for ordering, no wall-clock assertions, fixed business dates.
- **Report honestly:** quote the commands and their real output. A test that was not run in this
  session is "not run", never "passing". Label unverified claims as hypotheses.

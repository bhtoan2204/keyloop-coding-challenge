---
name: keyloop-test
description: Run and assess the Keyloop backend test suite — executes gofmt, vet, unit, race and (when DSNs are set) integration tests, then checks coverage of business invariants and event-sourcing edge cases (transactions, replay, OCC, snapshots) and reports real results and gaps. Use after implementing a change or before claiming a phase is done.
argument-hint: "[package pattern, default ./...]"
disallowed-tools: Edit, Write, NotebookEdit
---
# keyloop-test

Scope: `$ARGUMENTS` (if empty, use `./...`). Report only what was actually executed in this session.

## 1. Run (quote each command and its exit status / summary lines)
```bash
gofmt -l .
go vet ./...
go test -count=1 <scope>
go test -race -count=1 <scope>
go test -count=1 -v <scope> 2>&1 | grep -cE '^\s*--- PASS'   # pass count
go test -count=1 -v <scope> 2>&1 | grep -E -- '--- (FAIL|SKIP)'
```
If `MYSQL_TEST_DSN` / `PG_TEST_DSN` are set and `-tags integration` tests exist, run
`go test -tags integration -count=1 <scope>`; otherwise state "integration tests: not run (reason)".
If a command cannot run (missing cgo, Docker, DSN), say so — never mark it as passed.

## 2. Gap analysis
Check each item and answer covered / partly / missing with the test name:
- Invariants (`CLAUDE.md` list): reject + accept case, no pending event and unchanged state on reject.
- Aging: 89/90/91 days, leap year, year boundary, time zone, DST; sold never aging.
- Replay: command-vs-replay equivalence, invalid sequences, version gaps, no pending after replay.
- Snapshots: whole-struct JSON round trip; restore through `AggregateStore` with covered events removed.
- Persistence: multi-event atomic save, rollback on mid-transaction failure, duplicate version,
  aggregate-type mismatch; MySQL-backed versions of these (SQLite alone is not proof).
- OCC: concurrent saves → one winner, `ErrConcurrencyConflict` for the rest, contiguous versions.
- Projections (when present): duplicate skip, gap halt, unknown event, state+version in one tx, rebuild equality.

## 3. Optional mutation check
For a critical rule, on a scratch copy outside the repo (`git archive HEAD | tar -x -C <scratch>`
plus the working-tree changes), flip the rule and confirm a test fails. Never mutate the repo.

## 4. Report
Commands + results table, gap table, recommended tests (names + what they assert). Do not edit files.

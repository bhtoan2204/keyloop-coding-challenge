---
name: keyloop-security
description: Security review of the Keyloop inventory backend — input validation, SQL injection and query construction, PostgreSQL roles and append-only grants, secrets and credentials in code/config/logs, API trust boundaries and actor identity (X-Actor-Id), dependency and container security. Reports findings without changing anything. Use when SQL, HTTP handlers, config, Compose/Dockerfile or dependencies change.
argument-hint: "[path or base ref, default whole repo]"
disallowed-tools: Edit, Write, NotebookEdit
---
# keyloop-security

Scope: `$ARGUMENTS` (if empty, the whole repository). Report only; never rotate secrets, delete files or rewrite history.

## Checks
1. **Input validation:** every HTTP field validated at the boundary and again by domain invariants
   (VIN, lengths, enums, dates, page size ≤ 100, cursor tampering → 400). No panics on bad input.
2. **SQL safety:** all queries parameterised (`?` / `$n`); no `fmt.Sprintf` with user data in SQL;
   ORDER BY / sort fields chosen from an allowlist; LIKE patterns escaped.
   `grep -rn "Sprintf" --include=*.go internal | grep -i -E "select|insert|update|delete|order by"`.
3. **Database privileges** (`deploy/postgres/*.sql`): the writer `inventory_app` has no
   UPDATE/DELETE/TRUNCATE on `event_store.es_event` and no DDL; the reader `inventory_reader` has
   SELECT on the read-model tables and `event_store.dealership` only (not `es_event`); the rebuild
   role `inventory_rebuild` can read `es_event` but not write events; none is a superuser; the admin
   (`POSTGRES_USER`) is used only by `migrate` and the grant jobs; no admin DSN in the `api` config.
4. **Secrets:** search tracked and untracked files for credentials, tokens, private keys, DSNs with
   passwords (`git grep -n -I -E "(password|passwd|secret|token|api[_-]?key|BEGIN .*PRIVATE)"`).
   `.env` must not contain real secrets; only `.env.example` documents variables.
5. **Sensitive logging:** no request bodies, DSNs, tokens or full PII in logs; VIN is acceptable.
6. **Trust boundaries & identity:** `X-Actor-Id` is an unauthenticated stand-in (docs/01 A8) — check
   it is required on commands, recorded in metadata, never trusted for authorization decisions,
   and that the limitation is documented. Dealership scoping: a vehicle from another dealership → 404.
7. **Error exposure:** 500 responses hide internals; problem+json carries a request ID only.
8. **Dependencies:** `go list -m all`; flag unpinned/unused modules; run `govulncheck ./...` only if
   already installed (do not install tools) — otherwise state "not run".
9. **Containers (when present):** pinned image tags, non-root user, no secrets in images or
   Compose, ports bound only where needed, healthchecks, resource limits.

## Report
Findings grouped **Critical / Major / Minor**, each with `path:line`, risk, evidence (command or
code quote) and recommended remediation. Separate "verified by command" from "by inspection".

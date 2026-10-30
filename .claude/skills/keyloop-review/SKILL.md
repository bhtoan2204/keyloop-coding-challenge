---
name: keyloop-review
description: Review the current git diff of the Keyloop inventory backend for correctness, maintainability and architecture, with focus on Event Sourcing and CQRS (atomic append + read-model update, OCC, replay determinism, snapshot round-trip, projection version check, lock order). Reports critical/major/minor findings with file:line citations and never edits code. Use before the developer commits a phase.
argument-hint: "[base ref, default HEAD]"
disallowed-tools: Edit, Write, NotebookEdit
---
# keyloop-review

Base: `$ARGUMENTS` (if empty, use `HEAD`). Read-only: do not edit, stage, commit or run mutating git commands.

## 1. Collect the change
- `git status --short`, `git diff --stat <base>`, `git diff <base>`; list untracked files and read them.
- For every **deleted** line or file, find what replaced it. Unexplained deletions are findings.
- Read surrounding code for each hunk, not only the hunk.

## 2. Review checklist
- **Correctness:** logic, error wrapping (`%w`), nil handling, context propagation, resource cleanup.
- **Event Sourcing** (`.claude/rules/event-sourcing.md`): one transaction per command; CAS + unique
  version kept; retries re-load and re-run; apply methods validate before mutating and are
  deterministic; typed payloads; frozen event/aggregate names; no derived values stored; snapshot
  JSON covers every state field.
- **CQRS:** the read model is written only through `Projection.Apply` (in the command's save
  transaction via `CommandProjection`, or by `rebuild-projections`), never by handlers or ad-hoc
  SQL; version check in that same transaction; projection lock taken before event-store row locks;
  queries read `read_model` on the read-only pool; all table names schema-qualified.
- **Architecture** (`.claude/rules/architecture.md`): reuse instead of parallel abstractions,
  package direction, no new deps/schema/Docker outside the planned phase.
- **Maintainability / source organization** (`.claude/rules/source-organization.md`): one
  responsibility per file; handlers, DTOs, mappers, parsing, middleware and error mapping kept
  apart; no catch-all files; files near 250–300 lines and functions near 40–60 lines justified;
  multiline struct literals one field per line; guard clauses instead of nesting; comments explain
  why; comments only where the code cannot speak (no narration or name repetition).
  **Package organization:** no flat directory of unrelated features; no package per file;
  children never import the parent, shared helpers never import features, no sideways
  imports; small exported APIs; public entry points kept stable (aliases/facades).
  For structural refactors, confirm no behaviour change (same exported API, events, SQL, HTTP
  contracts, tests and assertions).
- **Repositories and reuse** (`.claude/rules/source-organization.md` "Reusability", "Separation of
  concerns", "Query construction", "Anti-overengineering"): port interfaces declared by their
  application consumer, implemented in infrastructure; no GORM/SQL types in application models;
  persistence models only in `viewstore/models`; explicit mappers; every repository of one write built
  on the same transaction handle, never beginning/committing itself or using the root pool; list
  queries composed from named scopes with bound parameters and fixed ordering (check `EXPLAIN` when a
  predicate changes); shared predicates/helpers reused rather than duplicated; no generic CRUD
  repository, reflection mapping, `any`-typed APIs or adapter layers without a real responsibility.
- **Tests:** do new behaviours have failing-without-change tests? Were any assertions weakened?
- **Docs:** deviations from `docs/` recorded; `docs/ai-log.md` factual.

For a large or ES-heavy diff, you may delegate an independent pass to the `go-reviewer` and/or
`event-sourcing-reviewer` subagents (give them the base ref and file list, not your conclusions),
then merge and de-duplicate their findings. Skip this for small diffs.

## 3. Report
Group findings as **Critical** (data loss, broken invariant, corrupt history, security),
**Major** (likely bug, missing test for a rule, architecture violation), **Minor** (style,
naming, docs). Each: `path:line` — problem — why it matters — suggested fix. Say "no findings"
per category when empty. List what you verified by running commands vs. by reading only.

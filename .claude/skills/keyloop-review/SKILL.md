---
name: keyloop-review
description: Review the current git diff of the Keyloop inventory backend for correctness, maintainability and architecture, with focus on Event Sourcing and CQRS (atomic appends, OCC, replay determinism, snapshot round-trip, projection idempotency). Reports critical/major/minor findings with file:line citations and never edits code. Use before the developer commits a phase.
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
- **CQRS:** read model updated only by the projector; version check in the same Postgres tx; no
  read-model writes from command handlers; consistency stated in API responses.
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

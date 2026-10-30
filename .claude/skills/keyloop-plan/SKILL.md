---
name: keyloop-plan
description: Plan a change to the Keyloop inventory backend before any code is written — inspects git status and the real code, finds the affected existing components, proposes the minimal change, defines tests and acceptance criteria, and explains trade-offs. Use at the start of every phase or non-trivial task in this repository.
argument-hint: "<phase or task, e.g. 'Phase 8: VIN reservation'>"
disallowed-tools: Edit, Write, NotebookEdit
---
# keyloop-plan

Produce an implementation plan for: **$ARGUMENTS**. Do not modify files. End by asking for approval.

## 1. Ground truth first
- `git status --short`, `git log --oneline -5`, `git diff --stat`: report uncommitted work; never discard it.
- Read `CLAUDE.md`, the relevant `docs/0x-*.md` section, and the latest `docs/ai-log.md` entry.
- Read the actual code you would touch and its tests. Run `go test -count=1 ./...` to get the baseline.
- Where docs and code disagree, the code wins. List each mismatch.

## 2. Affected components
Table: component · path · state (existing / partial / empty / missing) · action
(reuse as-is / extend / narrow refactor / new file in existing package). Justify every refactor
with a concrete reason (failing test, bug, compatibility). Name the Keyloop criterion (B1–B7)
or self-imposed requirement each change serves.

## 3. Minimal change
- Ordered steps, each small enough to test on its own.
- Files to create and modify. **No deletions** unless explicitly justified per file.
- **Source organization** (`.claude/rules/source-organization.md`): say which file each new type,
  handler, mapper or query goes into; one business operation per file where operations evolve
  independently; no catch-all files. Flag existing files near 250–300 lines or functions near
  40–60 lines that the change would grow, and say whether to split them first.
- New dependencies, schema changes or Docker changes only if this phase owns them (`docs/07`).

## 4. Tests and acceptance criteria
For each step: the tests to add (unit / SQLite / `-tags integration` PostgreSQL), including
edge cases — invariant rejections, transaction rollback (events, CAS and read model together),
OCC conflicts, replay, snapshots, projection version check, rebuild equality — and the observable acceptance criterion. Say which tests can fail
without the change and how to mutation-check the key rule.

## 5. Trade-offs and risks
Alternatives considered and why rejected. Unverified assumptions labelled as hypotheses with the
test that would settle them. Cut-list impact if the phase overruns (`docs/07 §4`).

## 6. Output
A plan in this order: summary, components table, steps, tests, risks, open questions.
Stop and wait for the developer's approval.

#!/usr/bin/env python3
"""PreToolUse safety hook for this repository (stdlib only).

Default mode (whole session, from .claude/settings.json):
  deny  edits to real .env files and anything under .git/
  deny  clearly destructive git commands (reset --hard, clean -f, checkout/restore of
        the whole tree, stash drop/clear, force push, history rewriting)
  ask   broad deletions (recursive rm, rm/git rm of many files or globs, find -delete),
        git stash, git push, git commit, branch force-delete
  allow everything else (gofmt, go test/vet, read-only git, single-file rm, refactors)

--read-only mode (review subagents): allow only an allowlist of read-only commands and
deny every file edit.

Protocol: reads the hook JSON on stdin; prints a hookSpecificOutput decision for
deny/ask; prints nothing (exit 0) to fall through to the normal permission flow.
The parsing is best-effort; it is a guard rail against mistakes, not a sandbox.
"""
import json
import os
import re
import shlex
import sys

EDIT_TOOLS = {"Edit", "Write", "NotebookEdit"}
ENV_TEMPLATE_SUFFIXES = (".example", ".sample", ".template", ".dist")
SAFE_DELETE_PREFIXES = ("/tmp/",)
WRAPPERS = {"sudo", "command", "env", "time", "nohup", "nice", "exec"}
SEGMENT_SPLIT = re.compile(r"&&|\|\||;|\||\n")


def decide(decision, reason):
    print(json.dumps({"hookSpecificOutput": {
        "hookEventName": "PreToolUse",
        "permissionDecision": decision,
        "permissionDecisionReason": "[keyloop guard] " + reason,
    }}))
    sys.exit(0)


def is_env_file(path):
    name = os.path.basename(path.rstrip("/"))
    if name != ".env" and not name.startswith(".env."):
        return False
    return not name.endswith(ENV_TEMPLATE_SUFFIXES)


def in_git_dir(path):
    return ".git" in os.path.normpath(path).split(os.sep)


def words(segment):
    try:
        parts = shlex.split(segment, posix=True)
    except ValueError:
        parts = segment.split()
    # drop VAR=value prefixes and wrapper commands
    while parts and (re.match(r"^[A-Za-z_][A-Za-z0-9_]*=", parts[0]) or parts[0] in WRAPPERS):
        parts = parts[1:]
    return parts


def segments(command):
    for raw in SEGMENT_SPLIT.split(command):
        raw = raw.strip()
        if not raw:
            continue
        w = words(raw)
        # evaluate the inner script of bash -c / sh -c as well
        if len(w) >= 3 and os.path.basename(w[0]) in ("bash", "sh", "zsh") and w[1] == "-c":
            yield from segments(w[2])
            continue
        yield raw, w


def git_subcommand(w):
    """Return (subcommand, args) skipping git's global options."""
    i = 1
    while i < len(w):
        a = w[i]
        if a in ("-C", "-c", "--git-dir", "--work-tree", "--namespace"):
            i += 2
        elif a.startswith("-"):
            i += 1
        else:
            return a, w[i + 1:]
    return "", []


def short_flags(args):
    flags = set()
    for a in args:
        if a.startswith("-") and not a.startswith("--"):
            flags.update(a[1:])
    return flags


def check_git(w):
    sub, args = git_subcommand(w)
    flags = short_flags(args)
    if sub == "reset" and "--hard" in args:
        decide("deny", "git reset --hard discards uncommitted work. Inspect `git status`/`git diff` and ask the developer.")
    if sub == "clean" and ("f" in flags or "--force" in args):
        decide("deny", "git clean -f deletes untracked files. Use `git clean -n` to preview and ask the developer.")
    if sub == "checkout" and ("." in args or ":/" in args or "-f" in args or "--force" in args):
        decide("deny", "git checkout of the whole tree / --force discards local changes.")
    if sub == "restore" and ("." in args or ":/" in args) and ("--staged" not in args or "--worktree" in args or "-W" in args):
        decide("deny", "git restore of the whole working tree discards local changes.")
    if sub == "stash":
        if args and args[0] in ("drop", "clear"):
            decide("deny", "git stash drop/clear destroys stashed work.")
        if not args or args[0] not in ("list", "show"):
            decide("ask", "git stash hides uncommitted work; confirm with the developer.")
    if sub == "push":
        if any(a in ("-f", "--force", "--mirror", "--delete", "-d") or a.startswith("--force") or a.startswith("+") for a in args):
            decide("deny", "Force/destructive push is not allowed.")
        decide("ask", "Pushing requires explicit developer approval.")
    if sub == "commit":
        decide("ask", "Committing requires explicit developer approval.")
    if sub in ("filter-branch", "filter-repo") or (sub == "reflog" and args[:1] == ["expire"]) or (sub == "gc" and any(a.startswith("--prune") for a in args)):
        decide("deny", "History rewriting / pruning is not allowed.")
    if sub == "branch" and ("-D" in args or ("--delete" in args and "--force" in args)):
        decide("ask", "Force-deleting a branch can lose commits; confirm with the developer.")
    if sub == "rm":
        targets = [a for a in args if not a.startswith("-")]
        if any(in_git_dir(t) for t in targets):
            decide("deny", "Removing .git content is not allowed.")
        if "--cached" in args:
            return
        if "r" in flags or len(targets) >= 3 or any(has_glob(t) or os.path.isdir(t) for t in targets):
            decide("ask", "Broad `git rm` of existing files; the developer must review which files go and why.")


def has_glob(s):
    return any(c in s for c in "*?[")


def outside_tmp(path):
    return not os.path.abspath(path).startswith(SAFE_DELETE_PREFIXES)


def check_rm(w):
    args = w[1:]
    targets = [a for a in args if not a.startswith("-")]
    flags = short_flags(args)
    recursive = bool(flags & {"r", "R"}) or "--recursive" in args
    for t in targets:
        if in_git_dir(t) or os.path.basename(t.rstrip("/")) == ".git":
            decide("deny", "Deleting .git content is not allowed.")
        if t in ("/", "~", "$HOME", ".", "..", "*", "./", "../"):
            decide("deny", "rm of `%s` is too broad." % t)
    risky = [t for t in targets if outside_tmp(t)]
    if risky and (recursive or len(risky) >= 3 or any(has_glob(t) for t in risky)):
        decide("ask", "Broad removal of existing files (%s). The developer must review it." % " ".join(risky[:5]))


def check_shell_writes(command):
    for m in re.finditer(r"(?:>>?|\btee\s+(?:-a\s+)?)\s*([^\s;&|<>]+)", command):
        target = m.group(1)
        if target.startswith("&") or target == "/dev/null":
            continue
        if is_env_file(target):
            decide("deny", "Writing to a real .env file is not allowed; use .env.example.")
        if in_git_dir(target):
            decide("deny", "Writing into .git/ is not allowed.")


def check_default(tool, tool_input):
    if tool in EDIT_TOOLS:
        path = tool_input.get("file_path") or tool_input.get("notebook_path") or ""
        if in_git_dir(path):
            decide("deny", "Editing files under .git/ is not allowed.")
        if is_env_file(path):
            decide("deny", "Editing a real .env file is not allowed; edit .env.example and let the developer copy it.")
        return
    if tool != "Bash":
        return
    command = tool_input.get("command", "")
    check_shell_writes(command)
    for raw, w in segments(command):
        if not w:
            continue
        prog = os.path.basename(w[0])
        if prog == "git":
            check_git(w)
        elif prog in ("rm", "rmdir", "unlink"):
            check_rm(w)
        elif prog == "find" and ("-delete" in w or any(x in w for x in ("-exec", "-execdir")) and "rm" in w):
            if any(outside_tmp(a) for a in w[1:2] if not a.startswith("-")):
                decide("ask", "find with -delete/-exec rm removes files in bulk; the developer must review it.")


# --- read-only mode -----------------------------------------------------------------

READ_ONLY_PROGRAMS = {
    "cat", "head", "tail", "wc", "ls", "grep", "rg", "sort", "uniq", "cut", "tr", "diff",
    "stat", "file", "tree", "echo", "pwd", "true", "jq", "basename", "dirname", "realpath",
}
READ_ONLY_GIT = {"status", "diff", "log", "show", "blame", "ls-files", "rev-parse", "grep",
                 "shortlog", "describe", "cat-file", "merge-base", "name-rev", "ls-tree"}
READ_ONLY_GO = {"vet", "test", "list", "version", "env", "doc", "build"}


def check_read_only(tool, tool_input):
    if tool in EDIT_TOOLS:
        decide("deny", "Review agents are read-only: report findings instead of editing.")
    if tool != "Bash":
        return
    command = tool_input.get("command", "")
    if re.search(r"(?<![0-9&])>(?!&)|>>", re.sub(r"[12]?>\s*/dev/null|2>&1", "", command)):
        decide("deny", "Review agents may not redirect output into files.")
    for raw, w in segments(command):
        if not w:
            continue
        prog = os.path.basename(w[0])
        if prog == "git":
            sub, args = git_subcommand(w)
            if sub not in READ_ONLY_GIT or any(a.startswith("--output") for a in args):
                decide("deny", "Only read-only git commands are allowed here (got `git %s`)." % sub)
        elif prog == "go":
            if len(w) < 2 or w[1] not in READ_ONLY_GO or "-o" in w:
                decide("deny", "Only go vet/test/list/build (no -o) are allowed here.")
        elif prog == "gofmt":
            if "-w" in w:
                decide("deny", "gofmt -w rewrites files; use gofmt -l or -d.")
        elif prog == "sed":
            if any(a.startswith("-i") or a == "--in-place" for a in w):
                decide("deny", "sed -i edits files.")
        elif prog == "find":
            if any(a in ("-delete", "-exec", "-execdir", "-ok", "-fprint", "-fprintf", "-fls") for a in w):
                decide("deny", "find may only list files here.")
        elif prog not in READ_ONLY_PROGRAMS:
            decide("deny", "`%s` is not on the read-only allowlist for review agents." % prog)


def main():
    try:
        payload = json.load(sys.stdin)
    except (json.JSONDecodeError, ValueError):
        return  # not a hook payload; fall through
    tool = payload.get("tool_name", "")
    tool_input = payload.get("tool_input") or {}
    if "--read-only" in sys.argv[1:]:
        check_read_only(tool, tool_input)
    check_default(tool, tool_input)


if __name__ == "__main__":
    main()

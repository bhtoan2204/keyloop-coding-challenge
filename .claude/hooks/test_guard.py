#!/usr/bin/env python3
"""Tests for guard.py. Feeds simulated PreToolUse payloads; never executes the commands.

Run: python3 -I .claude/hooks/test_guard.py -v
"""
import json
import os
import subprocess
import sys
import unittest

GUARD = os.path.join(os.path.dirname(os.path.abspath(__file__)), "guard.py")
ROOT = "/home/user/keyloop-coding-challenge"


def run(tool, tool_input, *flags):
    payload = {"hook_event_name": "PreToolUse", "tool_name": tool, "tool_input": tool_input, "cwd": ROOT}
    out = subprocess.run([sys.executable, "-I", GUARD, *flags], input=json.dumps(payload),
                         capture_output=True, text=True, check=True)
    if not out.stdout.strip():
        return "allow"
    return json.loads(out.stdout)["hookSpecificOutput"]["permissionDecision"]


def bash(cmd, *flags):
    return run("Bash", {"command": cmd}, *flags)


class DefaultPolicy(unittest.TestCase):
    def check(self, want, cases):
        for case in cases:
            with self.subTest(case=case):
                got = bash(case) if isinstance(case, str) else run(*case)
                self.assertEqual(got, want)

    def test_allowed_development_commands(self):
        self.check("allow", [
            "gofmt -w internal/domain",
            "go test -race -count=1 ./...",
            "go vet ./...",
            "git status --short && git diff --stat",
            "git log --oneline -5",
            "git show HEAD:internal/domain/errors.go",
            "git checkout -b feature/phase-2",
            "git checkout -- internal/domain/errors.go",
            "git restore --staged .",
            "git stash list",
            "git clean -n",
            "git rm --cached -r vendor",
            "git rm internal/old_file.go",
            "rm internal/domain/tmp_notes.go",
            "rm -rf /tmp/build-cache",
            "cat .env",
            "go test ./... 2>&1 | tail -5",
            "echo hi > /dev/null",
            ("Edit", {"file_path": ROOT + "/internal/domain/errors.go"}),
            ("Write", {"file_path": ROOT + "/.env.example"}),
            ("Write", {"file_path": ROOT + "/docs/.environment.md"}),
            ("Write", {"file_path": ROOT + "/.github/workflows/ci.yml"}),
        ])

    def test_denied(self):
        self.check("deny", [
            "git reset --hard",
            "git reset --hard HEAD~1",
            "cd x && git -C . reset --hard origin/main",
            "git clean -fd",
            "git clean -xdf",
            "git checkout -- .",
            "git checkout .",
            "git restore .",
            "git stash drop",
            "git stash clear",
            "git push --force origin main",
            "git push origin +main",
            "git filter-branch --tree-filter x",
            "bash -c 'git reset --hard'",
            "sudo git clean -f",
            "rm -rf .git",
            "rm -rf /",
            "echo SECRET=1 > .env",
            "echo x >> .env.local",
            "printf x | tee .env.production",
            "echo x > .git/config",
            ("Edit", {"file_path": ROOT + "/.env"}),
            ("Write", {"file_path": ROOT + "/.env.local"}),
            ("Edit", {"file_path": ROOT + "/.git/config"}),
            ("NotebookEdit", {"notebook_path": ROOT + "/.git/x.ipynb"}),
        ])

    def test_requires_review(self):
        self.check("ask", [
            "rm -rf internal/infrastructure",
            "rm -r package",
            "rm internal/domain/*.go",
            "rm a.go b.go c.go",
            "git rm -r internal/application",
            "git rm a.go b.go c.go",
            "find internal -name '*.go' -delete",
            "git stash",
            "git push origin feature/x",
            "git commit -m 'msg'",
            "git branch -D old",
        ])


class ReadOnlyPolicy(unittest.TestCase):
    def test_allowed(self):
        for cmd in ["git diff --stat", "git log -p -3 -- internal/domain", "git show HEAD",
                    "go vet ./...", "go test -count=1 ./internal/domain/...", "gofmt -l .",
                    "grep -rn ApplyChange internal | head -20", "sed -n 1,40p go.mod",
                    "find internal -name '*_test.go'", "go test ./... 2>&1 | tail -3"]:
            with self.subTest(cmd=cmd):
                self.assertEqual(bash(cmd, "--read-only"), "allow")

    def test_denied(self):
        for cmd in ["gofmt -w .", "sed -i s/a/b/ x.go", "git commit -m x", "git checkout main",
                    "git add -A", "go build -o bin/app ./cmd", "rm x.go", "python3 x.py",
                    "git diff > review.patch", "find . -name x -delete", "xargs rm",
                    "git diff --output=x.patch", "curl https://example.com"]:
            with self.subTest(cmd=cmd):
                self.assertEqual(bash(cmd, "--read-only"), "deny")
        for tool in ("Edit", "Write", "NotebookEdit"):
            with self.subTest(tool=tool):
                self.assertEqual(run(tool, {"file_path": ROOT + "/x.go"}, "--read-only"), "deny")


class Robustness(unittest.TestCase):
    def test_non_json_input_falls_through(self):
        out = subprocess.run([sys.executable, "-I", GUARD], input="not json", capture_output=True, text=True)
        self.assertEqual((out.returncode, out.stdout), (0, ""))

    def test_other_tools_ignored(self):
        self.assertEqual(run("Read", {"file_path": ROOT + "/.env"}), "allow")


if __name__ == "__main__":
    unittest.main()

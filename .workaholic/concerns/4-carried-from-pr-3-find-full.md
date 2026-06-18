---
origin_pr: 4
origin_pr_url: https://github.com/qmu/gdrive-ftp/pull/4
origin_branch: work-20260618-172215
origin_commit: 94c3613
created_at: 2026-06-18T19:57:02+09:00
severity: low
status: active
resolved_by_pr:
resolved_by_commit:
---

# (carried from PR #3) `find` full-path reconstruction is best-effort for shared-with-me items

## Description

`findPath` walks each match's `Parents[0]` to the corpus root; for items shared *with* the user whose ancestry doesn't reach the drive root, the rendered path is a best-effort partial (`internal/shell/commands.go`). The emitted `id` is always exact, so follow-up `id:` actions are unaffected — only the displayed path may be incomplete. Unchanged this branch.

## How to Fix

Detect when the parent-walk stops before the corpus root and mark such paths explicitly (e.g. a `…/` prefix), or fetch the owning context to label them.

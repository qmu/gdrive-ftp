---
origin_pr: 3
origin_pr_url: https://github.com/qmu/gdrive-ftp/pull/3
origin_branch: work-20260618-095217
origin_commit: a478a5a
created_at: 2026-06-18T17:09:50+09:00
severity: low
status: active
resolved_by_pr:
resolved_by_commit:
---

# `find` full-path reconstruction is best-effort for shared-with-me items

## Description

`findPath` walks each match's `Parents[0]` up to the corpus root with a cache; for items shared *with* the user (returned by the default `corpora=user` search) whose ancestry doesn't reach the drive root, the rendered path is a best-effort partial (see [bd17e89](https://github.com/qmu/gdrive-ftp/commit/bd17e89) in `internal/shell/commands.go`). The emitted `id` is always exact, so follow-up `id:` actions are unaffected — only the displayed path may be incomplete.

## How to Fix

Detect when the parent-walk terminates before the corpus root and mark such paths explicitly (e.g. a `shared/` or `…/` prefix), or fetch the owning context to label them.

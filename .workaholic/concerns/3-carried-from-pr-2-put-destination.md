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

# (carried from PR #2) put-destination logic is verified live, not unit-tested

## Description

The destination decision in `cmdPut`/`cmdGet` is network-bound (`resolveDir` → live Drive calls), so it has no unit coverage. This branch widened the same gap: `gdrive.Search`, `cmdFind`, and the per-command JSON output paths are likewise untestable because no `gdrive.Client` interface/mock exists (see [bd17e89](https://github.com/qmu/gdrive-ftp/commit/bd17e89) in `internal/shell/commands.go`). Only pure helpers (`parseIDArg`, `toFileEntry`, `emit`, `nameContains`, `findPath` on root files) are covered.

## How to Fix

Introduce a `gdrive.Client` interface seam so `cmdPut`/`cmdGet`/`cmdFind` and the client query methods can be exercised against a mock, then add command-level output and resolution tests.

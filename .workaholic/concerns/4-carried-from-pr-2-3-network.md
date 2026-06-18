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

# (carried from PR #2/#3) Network-bound command logic — including the new audit path — is not unit-tested

## Description

There is still no `gdrive.Client` interface/seam, so command logic that calls the live Drive API has no unit coverage. This branch widened that surface: the mutator→audit-record path in `cmdPut`/`cmdMkdir`/`cmdRm` and the raw-mode `Browse` keypress loop are also untested (see [c16bd97](https://github.com/qmu/gdrive-ftp/commit/c16bd97) and [9629a0d](https://github.com/qmu/gdrive-ftp/commit/9629a0d) in `internal/shell/commands.go` and `internal/audit/browser.go`). Pure helpers are covered (the `internal/audit` package has 13 tests; `parseIDArg`/`toFileEntry`/`emit`/`nameContains`/`findPath`/`moveCursor`/`viewportTop` are all tested). This consolidates the duplicate carry-overs `2-put-destination-logic` and `3-carried-from-pr-2-put-destination`, which track the same debt.

## How to Fix

Introduce a `gdrive.Client` interface seam so the command/mutator paths can be exercised against a mock, then add command-level output, resolution, and audit-recording tests. (Two stale duplicate concern files for this item can be collapsed to one during corpus housekeeping.)

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

# (carried from PR #3) JSON `size` omits genuine 0-byte files

## Description

`fileEntry.Size` uses `omitempty`, so a real 0-byte file emits no `size` key (`internal/shell/output.go`); `isFolder`/`mimeType` still disambiguate kind, so impact is minimal. Unchanged this branch. The audit `Entry` shares the same omitempty choice for `size`/`priorSize`.

## How to Fix

If exact size reporting for empty files matters, switch `Size` to `*int64` or a custom marshaler that always emits `size` for non-folders.

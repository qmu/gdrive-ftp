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

# JSON `size` omits genuine 0-byte files

## Description

`fileEntry.Size` uses `omitempty`, so a real 0-byte binary file emits no `size` key, indistinguishable on that field alone from a folder/gdoc (see [7436f29](https://github.com/qmu/gdrive-ftp/commit/7436f29) in `internal/shell/output.go`). `isFolder` and `mimeType` still disambiguate kind, so impact is minimal.

## How to Fix

If exact size reporting for empty files matters, switch `Size` to `*int64` or a custom marshaler that always emits `size` for non-folders.

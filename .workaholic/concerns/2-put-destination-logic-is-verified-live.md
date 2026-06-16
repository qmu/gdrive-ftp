---
origin_pr: 2
origin_pr_url: https://github.com/qmu/gdrive-ftp/pull/2
origin_branch: work-20260616-211843
origin_commit: 812bffc
created_at: 2026-06-17T00:06:42+09:00
severity: low
status: active
resolved_by_pr:
resolved_by_commit:
---

# put-destination logic is verified live, not unit-tested

## Description

The destination decision is network-bound (`resolveDir` →

## How to Fix

Introduce a client-interface seam so `cmdPut`/`cmdGet`

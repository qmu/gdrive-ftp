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

# Wedged paths from the old bug still need out-of-band cleanup

## Description

A path already broken by the old behavior (a folder and a

## How to Fix

Add an ID-based selector (e.g. `rm --id <fileId>` / `get --id`)

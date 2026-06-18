---
title: gdrive-ftp CLI & plugin (deploy-on-merge)
environment: production
confirmation_method: other
url: https://github.com/qmu/gdrive-ftp
---
## Procedure

gdrive-ftp ships as a Go CLI plus a Claude Code / Codex plugin served from the
default branch via `.claude-plugin/marketplace.json` and `.agents/plugins/marketplace.json`.
There is no separate runtime or server to deploy to: **merging the PR to `main` is
the deployment.** Users update through the marketplace (`/plugin marketplace add
qmu/gdrive-ftp`, then enable/refresh the plugin) and build the binary themselves
with `go build -o gdrive-ftp .`. A GitHub Release tags the shipped version.

## Confirmation

Because the artifact is source (no live endpoint to probe), the deploy is confirmed
on the exact commit that will merge:

- **Pre-merge (branch):** the toolchain is green on the merge artifact —
  `go build ./...`, `go vet ./...`, `go test ./...` all pass, and `gofmt -l` reports
  no files. This is the executable proof the change is shippable.
- **Post-merge:** the merge commit is present on `main`, and the published GitHub
  Release tag matches the `version` in `.claude-plugin/marketplace.json`.

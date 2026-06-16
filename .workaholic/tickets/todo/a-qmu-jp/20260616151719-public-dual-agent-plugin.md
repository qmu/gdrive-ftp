---
created_at: 2026-06-16T15:17:19+09:00
author: a@qmu.jp
type: enhancement
layer: [Config, UX]
effort:
commit_hash:
category:
depends_on:
---

# Publish gdrive-ftp as a public Claude Code + Codex plugin

## Overview

Package this repo so it installs as a plugin on **Claude Code** and **OpenAI
Codex**, distributing the existing `gdrive-ftp` usage skill, and make it safe to
publish at `github.com/qmu/gdrive-ftp`. Modeled on the public `../workaholic`
dual-agent marketplace.

The skill is pure prose (no `${CLAUDE_PLUGIN_ROOT}` tokens, no bundled scripts —
it just shells out to the `gdrive-ftp` binary on PATH), so it is already
self-contained: **no build/`dist/` generator is needed** (unlike workaholic).
The same `skills/` dir serves both agents. All four manifests share one version
(**1.0.0**) and the **MIT** license (user-chosen).

Scope: distribution manifests + LICENSE + README install matrix + genericizing a
real GCP project number in a test. No CLI behavior changes. Security scan already
done: no secrets tracked or ever committed; the only finding is the project
number below.

## Key Files

New (create):
- `.claude-plugin/marketplace.json` - Claude Code marketplace (repo root).
- `.agents/plugins/marketplace.json` - Codex marketplace (repo root).
- `plugins/gdrive-ftp/.claude-plugin/plugin.json` - Claude plugin manifest.
- `plugins/gdrive-ftp/.codex-plugin/plugin.json` - Codex plugin manifest
  (carries `repository`, `license`, `keywords`, `skills: "./skills/"`).
- `plugins/gdrive-ftp/skills/gdrive-ftp/SKILL.md` - the skill, **moved here**
  (`git mv`) from `.claude/skills/gdrive-ftp/SKILL.md` (content unchanged).
- `LICENSE` - MIT, holder "tamurayoshiya" (qmu), year 2026.

Edit:
- `internal/shell/shell_test.go` - replace the 4 occurrences of the real project
  number `651063897762` with placeholder `123456789012` (fixture + assertions).
- `README.md` - add an install matrix (Claude + Codex + skills CLI), a prominent
  "this can modify your Drive" caveat, and a note that the binary is installed
  separately; reconcile the existing `~/.claude/skills` symlink note (now the
  skill lives under `plugins/gdrive-ftp/skills/`).
- `.gitignore` - verify built binary / secrets stay ignored (no change expected).

## Related History

Direct foundation (this packages and distributes it):

- [20260616142105-claude-skill-gdrive-ftp-usage.md](.workaholic/tickets/archive/work-20260616-073652/20260616142105-claude-skill-gdrive-ftp-usage.md) - created the `gdrive-ftp` skill now being distributed; its README symlink note is superseded by the install matrix here.

## Implementation Steps

1. `git mv .claude/skills/gdrive-ftp .claude/skills/__tmp` is **not** needed —
   instead move the skill into the plugin layout:
   `mkdir -p plugins/gdrive-ftp/skills && git mv .claude/skills/gdrive-ftp plugins/gdrive-ftp/skills/gdrive-ftp`.
   Remove the now-empty `.claude/skills/` (and `.claude/` if empty). Optionally
   add a dev symlink `.claude/skills/gdrive-ftp -> ../../plugins/gdrive-ftp/skills/gdrive-ftp`
   so the skill still auto-loads when working in this repo (note it in README).
2. Create `.claude-plugin/marketplace.json`:
   ```json
   {
     "name": "gdrive-ftp",
     "version": "1.0.0",
     "description": "FTP-style CLI for Google Drive, plus a skill teaching coding agents to drive it",
     "owner": { "name": "tamurayoshiya", "email": "a@qmu.jp" },
     "plugins": [
       {
         "name": "gdrive-ftp",
         "description": "Use when a task needs to read or modify the user's Google Drive from the command line — list/navigate My Drive and Shared Drives, download, upload, mkdir, trash — via the gdrive-ftp CLI.",
         "version": "1.0.0",
         "author": { "name": "tamurayoshiya", "email": "a@qmu.jp" },
         "source": "./plugins/gdrive-ftp",
         "category": "development",
         "skills": ["./skills/gdrive-ftp"]
       }
     ]
   }
   ```
3. Create `plugins/gdrive-ftp/.claude-plugin/plugin.json`:
   ```json
   {
     "name": "gdrive-ftp",
     "description": "Use when a task needs to read or modify the user's Google Drive from the command line via the gdrive-ftp CLI.",
     "version": "1.0.0",
     "dependencies": [],
     "author": { "name": "tamurayoshiya", "email": "a@qmu.jp" }
   }
   ```
4. Create `plugins/gdrive-ftp/.codex-plugin/plugin.json`:
   ```json
   {
     "name": "gdrive-ftp",
     "version": "1.0.0",
     "description": "Use when a task needs to read or modify the user's Google Drive from the command line via the gdrive-ftp CLI.",
     "author": { "name": "tamurayoshiya", "email": "a@qmu.jp" },
     "repository": "https://github.com/qmu/gdrive-ftp",
     "license": "MIT",
     "keywords": ["google-drive", "cli", "ftp", "upload", "download"],
     "skills": "./skills/"
   }
   ```
5. Create `.agents/plugins/marketplace.json`:
   ```json
   {
     "name": "gdrive-ftp",
     "interface": { "displayName": "gdrive-ftp" },
     "plugins": [
       {
         "name": "gdrive-ftp",
         "source": { "source": "local", "path": "./plugins/gdrive-ftp" },
         "policy": { "installation": "AVAILABLE", "authentication": "ON_USE" },
         "category": "Productivity"
       }
     ]
   }
   ```
   Verify the exact Codex marketplace schema against
   `../workaholic/.agents/plugins/marketplace.json` before committing (field
   names/casing must match what Codex expects).
6. Create `LICENSE` — standard MIT text, `Copyright (c) 2026 tamurayoshiya`.
   Its identifier must match `license: "MIT"` in the Codex manifest.
7. Genericize the test: in `internal/shell/shell_test.go` replace every
   `651063897762` with `123456789012` (the fixture `Message` and both
   `strings.Contains` assertions), so the test stays self-consistent and still
   asserts the exact project number + activation URL are surfaced. Run
   `go test ./...`.
8. Update `README.md`: add an **Install matrix**:
   ```
   | Agent | How |
   | ----- | --- |
   | Claude Code | `/plugin marketplace add qmu/gdrive-ftp`, then enable the gdrive-ftp plugin |
   | OpenAI Codex | `codex plugin marketplace add qmu/gdrive-ftp --ref main` then `codex plugin add gdrive-ftp@gdrive-ftp` |
   | Cursor / OpenCode / others | `npx skills add qmu/gdrive-ftp` |
   ```
   Add a prominent caveat (workaholic uses a `> [!WARNING]` callout): "this
   requests full Drive access and can upload, overwrite, and trash files." Note
   the plugin ships the *skill*; the `gdrive-ftp` binary must be built/installed
   separately and on PATH. Reconcile the old `~/.claude/skills` symlink section
   with the new plugin location.
9. Validate: `go build ./... && go vet ./... && go test ./...`; JSON-lint the 4
   manifests; confirm `name` == directory for the plugin; confirm no secrets are
   newly tracked (`git status`, `.gitignore` still covers credentials/token/binary).

## Considerations

- **Version lockstep**: all four manifests use `1.0.0`. If bumped later, bump
  every manifest together (workaholic enforces this).
- **License consistency**: the `LICENSE` file and the `license` field in
  `.codex-plugin/plugin.json` must agree (MIT).
- **Skill self-containment (vendor-neutrality lens)**: the moved SKILL.md must
  stay pure-prose — no `${CLAUDE_PLUGIN_ROOT}`/Claude-only tokens — so it resolves
  identically on Codex and the skills CLI (`plugins/gdrive-ftp/skills/gdrive-ftp/SKILL.md`).
- **Skill move breaks local auto-discovery**: moving out of `.claude/skills/`
  means a session in this repo no longer auto-loads it unless a symlink is kept
  (step 1) — call this out so the dev experience doesn't silently regress.
- **Codex schema risk**: the `.agents/plugins/marketplace.json` shape is inferred
  from workaholic; verify field names against the reference before publishing
  (a wrong key silently breaks `codex plugin marketplace add`).
- **Public hygiene**: `.workaholic/tickets/` (with author emails) will be public —
  consistent with workaholic's own convention, so left as-is; flag only if the
  user wants them excluded. No secrets are tracked (verified).
- **go.mod module path** is the bare `gdrive-ftp` (not a public path like
  `github.com/qmu/gdrive-ftp`); not required for the plugin, but note it if the
  binary is meant to be `go install`-able from the public URL (out of scope here).
- **README is the spec (docs-in-sync lens)**: keep README and SKILL.md consistent
  after edits.

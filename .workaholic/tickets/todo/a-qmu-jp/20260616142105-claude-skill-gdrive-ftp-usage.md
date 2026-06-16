---
created_at: 2026-06-16T14:21:05+09:00
author: a@qmu.jp
type: enhancement
layer: [UX]
effort:
commit_hash:
category:
depends_on:
---

# Claude Code skill: how to use gdrive-ftp for Google Drive

## Overview

Create a Claude Code **Skill** (a `SKILL.md`) that teaches another Claude Code
session how to drive the `gdrive-ftp` CLI to operate on Google Drive — auth
setup, listing/navigating My Drive + Shared Drives, downloading (`get`),
uploading (`put`), making folders (`mkdir`), and trashing (`rm`). The user will
load this skill in a *different* session and invoke gdrive-ftp from there, so the
skill must be accurate, self-contained, and biased toward **one-shot,
non-interactive** usage (an agent won't sit in the REPL).

Scope: Google Drive operations via gdrive-ftp only. NOT the workaholic
`/ticket`→`/drive` workflow.

## Location

Author the canonical skill in-repo (version-controlled) at:

```
.claude/skills/gdrive-ftp/SKILL.md
```

Rationale (from policy discovery): a project skill under `.claude/skills/<name>/`
is auto-discovered by any Claude Code session whose cwd is this repo, needs no
copy step, and lives next to the code it documents. The directory name must equal
the frontmatter `name` and the file must be exactly `SKILL.md`.

Because the *other* session may run outside this repo, the skill body (and this
ticket's README touch) must document how to make it globally loadable:

```
mkdir -p ~/.claude/skills
ln -s "$PWD/.claude/skills/gdrive-ftp" ~/.claude/skills/gdrive-ftp
# (or copy it) — then any session can load the gdrive-ftp skill.
```

## Key Files

- `.claude/skills/gdrive-ftp/SKILL.md` - NEW. The skill (frontmatter + body).
- `README.md` - The authoritative behavior spec; the skill must stay consistent
  with it and may point to it. Add a short "Claude Code skill" note pointing at
  the new skill and the symlink/copy install step.
- `main.go` - Source of invocation modes: interactive (no args) vs one-shot
  (`gdrive-ftp <cmd> args`), the `auth` and `completion zsh` subcommands, and the
  `-creds`/`-token` flags + default paths (`~/.config/gdrive-ftp/...`). One-shot
  failures print `gdrive-ftp: <msg>` to stderr and exit non-zero.
- `internal/shell/commands.go` - The verbs and their exact behavior the skill
  documents (`get` exports Google-native docs; `put` overwrites by exact name;
  trailing-slash/dir destinations).
- `internal/shell/shell.go` - Virtual-root path model and the friendly
  disabled-API error text.
- `internal/gdrive/client.go` - Trash-not-delete, exact/case-sensitive matching,
  `ambiguous name` refusal, Google-native export-format map.
- `.gitignore` - Confirms `credentials.json`/`token.json` are private; the skill
  must never instruct committing or echoing secrets.

## Related History

No prior ticket creates a documentation/skill artifact. The skill must reflect
behavior delivered by these archived tickets (cross-reference as the behavior
source):

- [20260616074105-virtual-root-list-all-drives.md](.workaholic/tickets/archive/work-20260616-073652/20260616074105-virtual-root-list-all-drives.md) - virtual root + drive-as-first-path-component model.
- [20260616074104-shared-drives-client-support.md](.workaholic/tickets/archive/work-20260616-073652/20260616074104-shared-drives-client-support.md) - Shared Drives navigation.
- [20260616073652-remote-ssh-oauth-flow.md](.workaholic/tickets/archive/work-20260616-073652/20260616073652-remote-ssh-oauth-flow.md) - the auth flow.

## Implementation Steps

1. Create `.claude/skills/gdrive-ftp/SKILL.md` with YAML frontmatter:
   - `name: gdrive-ftp`
   - `description:` third-person, what + when, e.g.: *"Use when a task needs to
     read or modify the user's Google Drive from the command line — listing or
     navigating My Drive and Shared Drives, downloading files, uploading files,
     creating folders, or trashing items — via the `gdrive-ftp` CLI. Covers the
     one-time auth setup and non-interactive one-shot command usage."*
2. Body — **Prerequisites**: `gdrive-ftp` must be on PATH; the Google Drive API
   must be enabled for the OAuth client's project; run `gdrive-ftp auth` **once
   interactively** to cache the token (`~/.config/gdrive-ftp/token.json`).
   Non-interactive commands need that cached token or they would block on
   consent. Never commit/echo `credentials.json` or `token.json`.
3. Body — **Invocation model**: prefer one-shot `gdrive-ftp <cmd> args` (runs one
   command, exits). One-shot has **no persistent cwd**, so always use absolute
   paths beginning with the drive name. The first path component selects the
   drive: `My Drive` (the personal drive's literal name) or a Shared Drive name.
   Quote any path containing spaces: `"/My Drive/Work"`.
4. Body — **Command reference** (with runnable examples), at minimum:
   - `gdrive-ftp ls /` — list the virtual root (all drives you're a member of).
   - `gdrive-ftp ls "/My Drive/Work"` — list a folder.
   - `gdrive-ftp get "/My Drive/Work/report.pdf" ./report.pdf` — download
     (atomic). Google-native docs are exported with an appended extension
     (Docs→.docx, Sheets→.xlsx, Slides→.pptx, Drawing→.png, AppsScript→.json).
   - `gdrive-ftp put ./report.pdf "/My Drive/Work"` — upload; replaces a single
     exact-name match else creates new.
   - `gdrive-ftp mkdir "/My Drive/Work/specs"` ; `gdrive-ftp rm "/My Drive/Work/old.pdf"`.
   - Note `lcd`/`lls`/`lpwd` are local-side and only meaningful in the REPL.
5. Body — **Gotchas** (load-bearing): virtual root holds no files (`get`/`put`/
   `mkdir`/`rm` at `/` fail with "cd into a drive first"); names are matched
   exactly and case-sensitively, duplicates refused with `ambiguous name ...`;
   `rm` **trashes** (reversible), not a hard delete; single files only (no
   recursive upload/download); a folder merely *shared* with you appears inside
   its owning Shared Drive, not at the root; errors go to stderr with non-zero
   exit (check exit code). If you see "the Google Drive API is disabled …",
   enable it in the Cloud Console and retry after ~1 min.
6. Body — point to `README.md` as the authoritative spec and keep the skill in
   sync if commands/flags change. Keep the body focused; if it grows, split
   reference detail into an adjacent file (progressive disclosure).
7. Add the install/symlink note to `README.md` and verify the skill is valid
   (frontmatter parses; `name` matches the directory).

## Considerations

- **Trigger quality**: the `description` is what makes another session load the
  skill — it must name the capability (Google Drive file ops) and the tool
  (`gdrive-ftp`) and the *when*. A vague description won't trigger.
- **Accuracy/sync (operation lens)**: this is documentation that must match real
  behavior; derive every command/flag/limitation from `README.md`/source, not
  memory. Note in the skill that it tracks the CLI and must be updated on change.
- **Reachability (implementation lens)**: keep it version-controlled in-repo AND
  document the `~/.claude/skills/` symlink so out-of-repo sessions can load it.
- **Secrets**: never instruct committing or printing `credentials.json`/
  `token.json` (`.gitignore`); auth is via `gdrive-ftp auth` + cached token.
- **Non-interactive bias**: the consuming session is an agent; emphasize one-shot
  + absolute paths and the stderr/exit-code contract over the interactive REPL.
- **`.claude/` in this repo**: adding `.claude/skills/` is new for this repo;
  ensure it isn't git-ignored (it must be committed to be version-controlled).

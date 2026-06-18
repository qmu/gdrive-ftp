---
name: gdrive-ftp
description: Use when a task needs to read or modify the user's Google Drive from the command line — listing or navigating My Drive and Shared Drives, downloading files, uploading files, creating folders, or trashing items — via the `gdrive-ftp` CLI. Covers the one-time auth setup and non-interactive one-shot command usage.
---

# Using gdrive-ftp for Google Drive

`gdrive-ftp` is an FTP-style CLI for Google Drive. Use it to list, navigate,
download, upload, create folders, and trash files in **My Drive** and any
**Shared Drives** the user can access. Prefer **one-shot** commands
(`gdrive-ftp <cmd> args`) — each runs one command and exits, which is what you
want as an agent. The interactive shell (`gdrive-ftp` with no args) exists too
but you generally won't use it.

`README.md` in the gdrive-ftp repo is the authoritative spec; this skill must
stay consistent with it.

## Prerequisites (check these first)

1. `gdrive-ftp` is on `PATH` (`command -v gdrive-ftp`).
2. **Authorized once**: a cached token must exist at
   `~/.config/gdrive-ftp/token.json`. If it's missing, the user must run
   `gdrive-ftp auth` **interactively** (it walks an OAuth consent flow). Do not
   try to auth non-interactively — it blocks on a prompt. If a command fails with
   an auth/consent error, tell the user to run `gdrive-ftp auth`.
3. The Google Drive API must be enabled for the OAuth client's Cloud project. If
   you see *"the Google Drive API is disabled for this OAuth client's Google
   Cloud project"*, the user must enable it in the Cloud Console and retry after
   ~1 minute.

Never read, print, commit, or move `credentials.json` or `token.json` — they are
secrets.

## Path model (important)

- The top level is a **virtual root** listing the drives. The **first path
  component selects the drive**: `My Drive` (the personal drive's literal name)
  or a Shared Drive's name.
- One-shot mode has **no persistent working directory** — every command starts at
  the virtual root, so **always use absolute paths beginning with the drive
  name**, e.g. `"/My Drive/Work/report.pdf"`, `"/Engineering Team/specs"`.
- **Quote** any path containing spaces (the whole path is one argument):
  `"/My Drive/..."`.
- The virtual root holds no files: `get`/`put`/`mkdir`/`rm` at `/` fail with
  "cd into a drive first" — always include a drive as the first component.
- **Address by ID** with an `id:<DriveID>` token anywhere a remote path is
  expected (`get id:1A2b3C`, `put f.txt id:0BxFolder`, `rm id:1A2b3C`,
  `ls id:0BxFolder`, `cd id:0BxFolder`, `mkdir id:0BxFolder/New`). This skips name
  navigation entirely, so it is **drive-prefix-independent** (no leading
  `/My Drive/...` needed) and unambiguous — prefer it when you already hold a file
  or folder ID (e.g. from a previous `created … (<id>)` line or a Drive URL), or
  when a name is duplicated/ambiguous. An `id:` token used as a folder (`cd`/`ls`/
  `put` target / `mkdir` parent) must point at a folder; `mkdir id:<parent>` alone
  is rejected — append `/<name>`.

## Commands (one-shot examples)

```sh
# List all drives (My Drive + Shared Drives you are a member of)
gdrive-ftp ls /

# List a folder
gdrive-ftp ls "/My Drive/Work"
gdrive-ftp ls "/Engineering Team"

# Download a binary file (atomic: temp file renamed on success)
gdrive-ftp get "/My Drive/Work/report.pdf" ./report.pdf
# …into an existing local dir (kept under its remote name)
gdrive-ftp get "/My Drive/Work/report.pdf" ./downloads/

# Download a Google-native doc → auto-exported, extension appended:
#   Docs→.docx  Sheets→.xlsx  Slides→.pptx  Drawing→.png  AppsScript→.json
gdrive-ftp get "/My Drive/notes"        # saves notes.docx

# Upload: replaces a single exact-name match, else creates a new file
gdrive-ftp put ./report.pdf "/My Drive/Work"
gdrive-ftp put ./photo.jpg "/My Drive/Photos/photo.jpg"   # rename remote target

# Make a folder
gdrive-ftp mkdir "/My Drive/Work/specs"

# Trash (reversible — NOT a permanent delete)
gdrive-ftp rm "/My Drive/Work/old.pdf"

# Address by Drive ID (no drive prefix needed; unambiguous)
gdrive-ftp get id:1A2b3CdEfGh ./report.pdf       # download a file by ID
gdrive-ftp put ./report.pdf id:0BxParentFolder   # upload INTO a folder by ID
gdrive-ftp rm id:1A2b3CdEfGh                      # trash by ID
gdrive-ftp ls id:0BxParentFolder                 # list a folder by ID
gdrive-ftp mkdir id:0BxParentFolder/specs        # create under a parent by ID
```

Success output goes to **stdout** (e.g. `downloaded …`, `uploaded …`,
`created specs/ (<id>)`, `trashed old.pdf`). `ls` rows are
`  <size>  <modified>  <name>`, with a trailing `/` on folders/drives.

`lcd` / `lls` / `lpwd` are local-filesystem helpers and only meaningful inside
the interactive shell — ignore them in one-shot use.

Flags: `-creds <path>` and `-token <path>` override the credential/token
locations (defaults under `~/.config/gdrive-ftp/`). `-json` switches output to
machine-readable JSON (see below).

## JSON output (`-json`) — prefer this as an agent

Pass the global `-json` flag to get parseable output instead of scraping text:

```sh
gdrive-ftp -json ls "/My Drive/Work"
# → stdout: [{"name":"report.pdf","id":"1A2b","mimeType":"application/pdf","isFolder":false,"size":840000,"modifiedTime":"2026-06-10T11:02:00Z"}]

gdrive-ftp -json put ./report.pdf id:0BxParent
# → stdout: {"action":"uploaded","name":"report.pdf","id":"1A2b","size":840000}

gdrive-ftp -json get /nope
# → stderr: {"error":"no such file or directory"} , exit 1
```

Contract: **results on stdout** (an array for `ls`; a single object for
`get`/`put`/`mkdir`/`rm`; `{"path":…}` for `pwd`), **errors on stderr** as
`{"error":…}` with a **non-zero exit**. Stable keys: `name`, `id`, `mimeType`,
`isFolder`, `size` (omitted for folders/Google-native docs), `modifiedTime`
(RFC 3339); action objects carry `action`
(`downloaded`/`exported`/`uploaded`/`created`/`trashed`) plus `dest`/`size`/`id`.
Capture an `id` from a result and reuse it as `id:<id>` in a follow-up command.

## Error / exit contract (for scripting)

On failure, gdrive-ftp prints `gdrive-ftp: <message>` to **stderr** and exits
**non-zero**. Always check the exit code. Common messages:

- `no such file or directory` — the path doesn't exist (check the drive name and
  exact casing).
- `ambiguous name (multiple matches); rename to disambiguate` — two items share
  the name; the tool refuses to guess. Names are matched **exactly and
  case-sensitively**.
- `... is a directory (recursive upload is not supported)` — `put` is
  single-file only (no recursive upload/download).
- `cannot upload to the virtual root; cd into a drive first` — you used `/`
  without a drive component.

## Gotchas

- One-shot has no cwd → use absolute, drive-prefixed paths.
- Names are exact + case-sensitive; duplicates are refused, not guessed.
- `rm` trashes (recoverable from the Drive web UI), it does not hard-delete.
- Single files only — no recursive directory transfer.
- A folder merely *shared with you* appears inside its owning Shared Drive, not
  as a top-level entry in `ls /`.
- This skill tracks the CLI — if commands/flags change, update it from
  `README.md`.

---
created_at: 2026-06-18T17:22:15+09:00
author: a@qmu.jp
type: enhancement
layer: [Domain, Infrastructure]
effort: 2h
commit_hash: f1de6a1
category: Added
depends_on:
---

# Audit log of Drive mutations (append-only JSONL under the config dir, with rotation)

## Overview

gdrive-ftp is increasingly driven by AI coding agents, which can `put` (overwrite),
`rm` (trash), or `mkdir` in the user's Drive — sometimes not what the user wanted.
Today nothing records these changes, so there is no way to look back at *what the CLI
actually did, and when*. Add an **append-only audit log** that records every mutating
operation as one JSON object per line, written to the user's config dir, so both the
user and the agent can reconstruct (and undo, by hand) what happened.

This ticket is the **logging foundation**; a separate ticket
([20260618172216-audit-log-tui-browser.md](.workaholic/tickets/todo/a-qmu-jp/20260618172216-audit-log-tui-browser.md))
adds the interactive `log` browser that reads this file.

**Decisions (locked):**
- **Scope = mutations only:** `put` (upload/overwrite), `rm` (trash), `mkdir` (create).
  `get`/download is read-only (it does not change Drive) and is **out of scope**.
- **Format:** JSON Lines (one compact JSON object per line), reusing the `output.go`
  encoder conventions (`SetEscapeHTML(false)`, newline-terminated).
- **Location:** `~/.config/gdrive-ftp/audit.jsonl` via a new `defaultLogPath()` mirroring
  `defaultTokenPath()`; file `0600`, dir `0700` (same discipline as `token.json`).
- **Append-only:** entries are never edited or deleted in place (history-structure policy).
- **Best-effort:** a log-write failure **must never break the command** — exactly like the
  token cache write, which "must not break the session". Surface it at most as a one-line
  warning, never as a command error.
- **Rotation:** size-based. When `audit.jsonl` exceeds ~5 MB, rotate to `audit.jsonl.1`
  (shifting `.1`→`.2`→`.3`) and keep **3** rotated segments; the oldest is dropped (the
  explicit allowable-loss margin). Simple, bounded, stdlib-only.

## Key Files

- `internal/audit/audit.go` *(new package)* — the domain audit package: an owned `Entry` type, an `Operation` value-object (`upload`/`trash`/`mkdir`), a `Logger` with `Record(ctx, Entry) error` (append + rotate), and `defaultPath`/permission handling. Per Domain Layer Separation, this is a small package with a domain-vocabulary interface; **no `drive.File` SDK type may appear in its schema**.
- `internal/shell/shell.go` — the `Shell` struct (lines 32–46) and `New` (line ~42). Add a logger field (an interface the shell consumes, nil-safe) set in `New`, so **both** one-shot (`Execute`) and interactive (`dispatch`) paths log uniformly through the single Shell object.
- `internal/shell/commands.go` — the three mutators are the emit/log sites: `cmdPut` (~273 `Upload` → `{uploaded, name, f.Id, f.Size}`), `cmdMkdir` (~299 `Mkdir` → `{created, f.Name, f.Id}`), `cmdRm` (~315 `Trash` → `{trashed, f.Name, f.Id}`). Record an audit `Entry` right after the successful client call, beside the existing `s.emit(...)`. `cmdMkdir` is the one mutator whose client call takes no `driveID`, so derive it from `currentDriveID(parent)` at the command layer.
- `internal/shell/output.go` — `actionResult{Action,Name,ID,Dest,MimeType,Size}` is the existing shape the audit `Entry` mirrors and extends (add `timestamp`, `parentID`, `driveID`, `cwd`, and replacement info). Reuse the `emit` encoder conventions for the JSONL writer.
- `internal/gdrive/client.go` — defines the identity fields available at each mutation (`Upload` returns the resulting `*drive.File` with `Id`/`Size`; `Trash` takes the `fileID`; `Mkdir` returns the new folder). For a `put` that **replaces** an existing same-named file, `Upload`'s internal `FindChildren` already locates the prior target — surfacing its prior `Id`/`Size` is what lets the entry record before/after (see Considerations).
- `main.go` — `configDir()`/`defaultTokenPath()` (lines ~145–164) is the model for `defaultLogPath()`; `shell.New(ctx, client, os.Stdout, *jsonOut)` (line ~73) is where the logger is constructed and injected.
- `internal/shell/shell_test.go` & a new `internal/audit/audit_test.go` — the fake-client harness already asserts mutator behavior; extend it to assert an entry is recorded per mutation, and unit-test the `Entry` serialization and the rotation logic directly.
- `README.md`, `plugins/gdrive-ftp/skills/gdrive-ftp/SKILL.md` — document the audit log (location, JSONL schema, rotation, that it records mutations) in the **same commit**.

## Related History

No prior logging/audit work exists; this builds on the config-dir and JSON-DTO
surfaces established recently.

Past tickets that touched similar areas:

- [20260618115619-json-output-format.md](.workaholic/tickets/archive/work-20260618-095217/20260618115619-json-output-format.md) — Created the owned-DTO + `emit` seam in `internal/shell/output.go` and enumerated the mutating-command success sites; the audit `Entry` mirrors `actionResult` and reuses its encoder conventions.
- [20260618095212-accept-drive-id-for-commands.md](.workaholic/tickets/archive/work-20260618-095217/20260618095212-accept-drive-id-for-commands.md) — Added `id:` addressing + `GetByID`; the IDs the audit log records are exactly what a user/agent feeds back as `id:` to recover (e.g. restore a trashed file).
- [20260616213158-put-into-existing-folder-shadows-it.md](.workaholic/tickets/archive/work-20260616-211843/20260616213158-put-into-existing-folder-shadows-it.md) — The put-shadowing bug is the canonical "unwanted mutation" this audit trail is meant to make recoverable; clarifies what a `put` does to the Drive.

## Implementation Steps

1. **`internal/audit` package.** Define `Operation` (a small string value-object with constants `OpUpload`/`OpTrash`/`OpMkdir`; reject the zero value via a constructor) and `Entry`:
   `{Time time.Time (RFC3339 on marshal), Op Operation, Name, ID, ParentID, DriveID, Cwd string, Size int64 (omitempty), ReplacedID string (omitempty), ReplacedSize int64 (omitempty)}`. Provide constructors so a malformed entry can't be built.

2. **`Logger`.** `New(path string) *Logger` and `Record(ctx, Entry) error`: open the file `O_APPEND|O_CREATE|O_WRONLY, 0600` (parent dir `MkdirAll 0700`), encode the entry compact (`SetEscapeHTML(false)`) + newline, append. Serialize concurrent writes with a mutex. Check size before/after write and rotate when over the cap.

3. **Rotation.** A `rotate(path)` helper: when `audit.jsonl` ≥ 5 MB, shift `audit.jsonl.2`→`.3`, `.1`→`.2`, `audit.jsonl`→`.1`, drop anything beyond `.3`. Pure filesystem renames, unit-testable against a temp dir. Cap and count are package constants.

4. **Inject into the Shell.** Add a small consumer-side interface to `internal/shell` (e.g. `type auditLogger interface { Record(context.Context, audit.Entry) error }`) and a nil-safe field on `Shell`; `New` takes it. When the field is nil (e.g. tests, or logging disabled), recording is a no-op.

5. **Record at the three mutators.** After each successful client call in `cmdPut`/`cmdMkdir`/`cmdRm`, build the `Entry` (op, name, resulting/trashed id, `currentID(parent)` as ParentID, `currentDriveID(parent)` as DriveID, `s.pwd()` as Cwd, size) and call the logger; ignore/soft-warn on its error so the command still succeeds. For `put`, populate `ReplacedID`/`ReplacedSize` when the upload replaced an existing file.

6. **Wire in main.** Add `defaultLogPath()` (`filepath.Join(configDir(), "audit.jsonl")`), construct `audit.New(path)`, pass it into `shell.New`. Consider an opt-out (`-no-log` flag or `GDRIVE_FTP_NO_LOG` env) so privacy-sensitive users can disable — small, but see Considerations.

7. **Tests.** `audit_test.go`: entry marshals to the expected JSON keys/RFC3339 time; rotation shifts/drops segments correctly at the cap; permissions are `0600`. In `shell_test.go`: each mutator records exactly one entry with the right op/ids via a capturing fake logger; a logger error does not fail the command.

8. **Docs (same commit).** README: a "Audit log" section (location, JSONL schema with field meanings, rotation policy, how to disable, that it never stores credentials or file contents). SKILL.md: tell the agent the log exists, where it is, that it is greppable JSONL, and that recorded `id`s can be fed back as `id:` to recover.

## Patches

> **Note**: speculative — verify signatures/line numbers against current code before applying.

### `main.go`

```diff
@@ config paths
 func defaultTokenPath() string {
 	return filepath.Join(configDir(), "token.json")
 }
+
+// defaultLogPath is the append-only audit log of Drive mutations, beside the token.
+func defaultLogPath() string {
+	return filepath.Join(configDir(), "audit.jsonl")
+}
```

## Considerations

- **Append-only with before/after, not just actor+time** (`internal/audit/audit.go`). Per the History Structures policy, an entry that says only "a file was trashed at T" is insufficient — record *what changed*: the target id+name, parent, operation, the resulting id (upload/mkdir) or trashed id (rm), and for a replacing `put` the prior file's id/size (`ReplacedID`/`ReplacedSize`). That is what makes the log a recovery surface, not just a diary.
- **Observable by design, structured** (`internal/audit/audit.go`). Emit structured JSON at the moment of the mutation with stable keys — never free-form text. This is the Observability/Self-Healing policy: the log's output is also the *input* to recovery.
- **No credentials, least data** (`internal/audit/audit.go`, `internal/auth`). The log must never contain the OAuth token/credentials (those stay in `token.json`) and **no file contents** — only the identity fields the audit needs. File names/ids are user data: collect them because they are load-bearing, nothing extra (Defense-in-Depth + Data Sovereignty). File `0600`, dir `0700`, mirroring `saveToken`.
- **Best-effort, never breaks the command** (`internal/shell/commands.go`). A failed log write is soft — the mutation already happened and the command must still report success. Mirror the token-cache pattern (an unwritable cache "must not break the session"). Errors as values, `context.Context` threaded first (Go standards).
- **Domain separation, no SDK leakage** (`internal/audit` vs `internal/gdrive`). The audit schema is an owned type; `drive.File` must not appear in it — translate at the command layer. The logger is one seam reused by both one-shot and interactive paths, not duplicated per entry point.
- **Bounded disk / capacity plan** (`internal/audit/audit.go`). Rotation is the capacity plan: 5 MB × (1 active + 3 rotated) ≈ 20 MB ceiling, oldest dropped. This is the explicit allowable-loss margin; keep it minimal per "start from the minimum" (Capacity & Recovery Planning).
- **User sovereignty / opt-out** (`main.go`, `README`). The log is the user's data: a plain file they can read, grep, or delete, and an opt-out (`-no-log`/env) respects users who don't want a local trail. Document retention plainly. Default **on**, since an always-present trail is the whole point of recovering from unwanted agent changes.
- **Dual reachability** (`internal/audit/audit.go`). The JSONL file is the AI/grep path (the TUI ticket adds the human path over the same data). Keep the schema parseable and stable so an agent can `cat`/`grep` it without the browser — the Accessibility policy treats the agent-reachable path as primary, not an afterthought.

## Final Report

Development completed as planned. The `internal/audit` package owns the schema and writer; the Shell holds a nil-safe `*audit.Logger` so one-shot and interactive paths log identically through one seam; the three mutators record after success. Build, `go vet`, the suite, and `gofmt` all pass; `-no-log` confirmed in `-h`.

### Discovered Insights

- **Insight**: A `put` that overwrites does an `Files.Update` on the *same* file id, so the "replaced" file's id equals the result's id — the only meaningful before/after signal is the **size**. The schema models this as `replaced: bool` + `priorSize`, not a redundant `replacedId`, which would have duplicated `id` and misled readers into thinking a different object was involved.
  **Context**: `internal/gdrive/client.go` `Upload`/`UploadResult` and `internal/audit` `Entry` — the overwrite case is a content revision, not a file swap.
- **Insight**: The best-effort warning on a failed log write must go to **`os.Stderr`**, not `s.out` — writing it to `s.out` would corrupt the single-JSON-value stdout contract under `-json`. This is the one place the shell deliberately bypasses its `out` writer.
  **Context**: `internal/shell/shell.go` `audit()` helper — stdout stays machine-clean; warnings are stderr-only.
- **Insight**: A nil `*audit.Logger` is a valid no-op logger (method on nil receiver guarded by `if l == nil`), so `-no-log`, completion, and tests all pass `nil` without any interface or wrapper — simpler than a consumer-side `auditLogger` interface, which the ticket suggested but proved unnecessary since the mutator path can't be unit-tested anyway (no client fake).
  **Context**: `internal/audit/audit.go` `Record` + `internal/shell` — the nil-receiver pattern removed a layer of indirection.

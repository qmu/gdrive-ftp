---
created_at: 2026-06-16T11:45:48+09:00
author: a@qmu.jp
type: enhancement
layer: [UX, Domain]
effort:
commit_hash:
category:
depends_on:
---

# Tab completion in the interactive shell (sftp-style)

## Overview

In the `gdrive-ftp` interactive shell, pressing **Tab** should complete what
you're typing against real data — like `sftp` does — instead of nothing
happening. Three completion contexts:

1. **Command verbs** — at the start of the line, Tab completes `ls`, `cd`,
   `get`, `put`, `mkdir`, `rm`, `lcd`, `lls`, `lpwd`, `help`, `?`, `quit`/
   `exit`/`bye`.
2. **Remote paths** — for remote arguments (`ls`, `cd`, `get`, `rm`, `mkdir`,
   and the remote arg of `put`), Tab lists the entries of the relevant Drive
   directory queried **live from Google Drive** (folders shown with a trailing
   `/`), and at the virtual root it completes drive names (My Drive + Shared
   Drives).
3. **Local paths** — for local arguments (`lcd`, `lls`, and the local arg of
   `put`/`get`), Tab completes from the local filesystem.

Note on "in my zsh": this is **in-shell** completion (the gdrive-ftp REPL has
its own prompt), exactly like `sftp` — it is not zsh's completion of the
`gdrive-ftp` external command. The REPL currently reads lines with a plain
`bufio.Scanner`, which has no line editing or Tab handling; it must be replaced
(in interactive TTY mode) with a line editor that supports a Tab callback.

Chosen approach: use **`golang.org/x/term`**'s `term.Terminal`, which is already
a project dependency (used by `internal/auth`) and exposes an
`AutoCompleteCallback` — so no new third-party dependency is introduced. The
non-interactive path (pipes / one-shot) keeps the existing `bufio.Scanner`.

## Key Files

- `internal/shell/shell.go` - The core change. `Run()` (lines ~56-82) is the
  `bufio.Scanner` loop to replace, in interactive mode, with a `term.Terminal`
  reader whose `AutoCompleteCallback` performs completion; keep the scanner loop
  for the non-TTY/`interactive == false` path. Reuse `tokenize`, `splitPath`,
  `startStack`/`resolveDir`, `currentID`/`currentDriveID`, `driveList`,
  `sortedCommandNames`, and the `commands` map. `Shell` currently holds only an
  `io.Writer`; the editor needs the tty for reading too.
- `internal/shell/commands.go` - Source of candidate sets: the `commands` map
  keys (verbs), the remote-children pattern `s.c.List(ctx, currentDriveID,
  currentID)` + `gdrive.IsFolder` (+ trailing `/`), `s.driveList()` for the
  virtual root, and the local pattern `os.ReadDir` (as in `cmdLls`). Which
  commands take remote vs local args drives which completer to use.
- `internal/gdrive/client.go` - Candidate fetch APIs already exist: `List`,
  `ListDrives`, `FindDir`; `Ref{ID,Name,DriveID}` and `IsFolder`. No Drive
  query strings should be added to the shell — keep them behind this wrapper.
- `main.go` - Wires `shell.New(ctx, client, os.Stdout)` then `sh.Run(true)`.
  `term.Terminal` needs an `io.ReadWriter` over the tty and raw mode via
  `term.MakeRaw(int(os.Stdin.Fd()))` (restored on exit); `New`/`Run` likely
  need access to stdin, or the editor is constructed inside `Run` from
  `os.Stdin`/`os.Stdout`.
- `internal/shell/shell_test.go` - Add table-driven tests for the pure
  completion logic (see Considerations) following the existing dependency-free
  style.
- `go.mod` - No change expected; `golang.org/x/term` is already a direct dep.
- `README.md` - Document Tab completion in the Commands/Usage section.

## Related History

Builds directly on already-archived work in this branch: the virtual root and
the drive-context path model.

- [20260616074105-virtual-root-list-all-drives.md](.workaholic/tickets/archive/work-20260616-073652/20260616074105-virtual-root-list-all-drives.md) - Defines the cwd stack, `currentID`/`currentDriveID`, `driveList`, and virtual-root semantics the completer reuses (same package).
- [20260616074104-shared-drives-client-support.md](.workaholic/tickets/archive/work-20260616-073652/20260616074104-shared-drives-client-support.md) - Provides the `driveID`-aware `List`/`FindDir`/`ListDrives` APIs the completer queries for candidates.
- [20260616073652-remote-ssh-oauth-flow.md](.workaholic/tickets/archive/work-20260616-073652/20260616073652-remote-ssh-oauth-flow.md) - Precedent for raw-mode terminal handling with `golang.org/x/term`.

## Implementation Steps

1. **Factor the pure completion core** (testable, no tty, no network injected):
   a function that, given the current line + cursor position and a candidate
   provider, returns the completion result. Suggested shape:
   `complete(line string, pos int, providers) (newLine string, newPos int, candidates []string)`.
   It uses `tokenize` (or a cursor-aware variant) to find the active token, then:
   - first token → filter `sortedCommandNames()` + `quit`/`exit`/`bye` by prefix;
   - argument token → `splitPath` into (dir, basePrefix), and ask the relevant
     provider for the names in `dir`, filtered by `basePrefix`.
   Keep the Drive/local lookups behind a small interface so tests pass a fake.
2. **Remote candidate provider**: resolve the dir part via `resolveDir`, then
   `s.driveList()` if the resulting stack is the virtual root, else
   `s.c.List(s.ctx, currentDriveID(stack), currentID(stack))`; map to names,
   appending `/` to folders. Honor `s.ctx` so Ctrl-C cancels an in-flight Tab
   lookup. On any error, return no candidates (never abort the line).
3. **Local candidate provider**: `os.ReadDir` of the dir part (mirroring
   `cmdLls`), appending `/` to directories.
4. **Per-command arg routing**: a table marking each verb's argument positions
   as remote vs local (e.g. `get` = remote then local; `put` = local then
   remote; `lcd`/`lls` = local; `ls`/`cd`/`rm`/`mkdir` = remote). Position is
   the token index under the cursor.
5. **Wire the editor**: in `Run(interactive)` when stdin is a TTY
   (`term.IsTerminal`), put it in raw mode (`term.MakeRaw`, deferred
   `Restore`), build a `term.Terminal` with the `gdrive:<pwd>> ` prompt, set
   `AutoCompleteCallback` to invoke the core on Tab (`key == '\t'`), and read
   lines via `terminal.ReadLine()` in place of the scanner. Keep the existing
   `bufio.Scanner` loop for non-TTY/`interactive == false`.
6. **Multi-candidate display**: `term.Terminal`'s callback returns a single
   replacement, so on Tab compute the longest common prefix and complete to it;
   when several candidates remain (e.g. unchanged line / repeat Tab), print the
   candidate list to the terminal (like sftp) and re-show the prompt+line.
7. **Quoting**: candidates containing spaces must round-trip through `tokenize`
   — emit them quoted (or backslash-escaped) consistent with the tokenizer.
8. **Docs + tests**: README note; table-driven tests for the pure core (verb
   prefixing, dir/base split, prefix filter, trailing `/`, quoting, common-
   prefix logic) with a fake provider. Run `go build/vet/test ./...`.

## Considerations

- **Raw-mode output (CRLF)**: under `term.MakeRaw`, a bare `\n` in command
  output won't return the carriage. Either route command output through the
  `term.Terminal` (which translates), or ensure interactive output uses `\r\n`.
  Verify `ls`/`help` output still lines up while the editor is active
  (`internal/shell/commands.go` formatting helpers; `internal/shell/shell.go`
  `Run`).
- **Graceful degradation (operation lens)**: no completion when stdin isn't a
  TTY (one-shot `Execute`, pipes) — fall back to the scanner. A failed/slow
  Drive lookup yields zero candidates and never blocks or crashes the prompt;
  the lookup must honor the signal-cancelable `s.ctx` (`main.go` sets it).
- **Latency**: each remote-path Tab is one `List` round-trip. Acceptable, but
  consider a tiny per-directory cache keyed by (driveID, folderID) for repeat
  Tabs in the same dir within a session; keep it simple — correctness over
  caching, and document if a cache is added.
- **Anti-corruption boundary (implementation lens)**: fetch candidates only via
  existing `internal/gdrive` methods returning `Ref`/`*drive.File`; do not build
  Drive queries in the shell (`internal/gdrive/client.go`).
- **Testability (implementation lens)**: the only previously-tested surface is
  pure helpers; keep the completion core tty/network-independent so it is unit
  tested without a terminal — `term.Terminal` itself stays a thin adapter.
- **Modeless (design lens)**: Tab augments free-form input; every command stays
  fully typeable and Tab never traps the user (`internal/shell/shell.go`).
- **Scope boundary**: this is in-REPL completion only. A separate zsh/bash
  completion script for the external `gdrive-ftp` command is explicitly out of
  scope (and largely impossible for live remote paths). If line history /
  arrow-key recall is wanted too, treat it as a follow-up — `term.Terminal`
  gives basic history but a richer setup may warrant its own ticket.

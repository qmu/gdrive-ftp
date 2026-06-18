---
created_at: 2026-06-18T17:22:16+09:00
author: a@qmu.jp
type: enhancement
layer: [UX, Domain]
effort:
commit_hash:
category:
depends_on: [20260618172215-audit-log-mutations.md]
---

# `log` subcommand: interactive tig-like browser for the audit log (j/k, no new deps)

## Overview

Once mutations are recorded to `~/.config/gdrive-ftp/audit.jsonl`
([20260618172215-audit-log-mutations.md](.workaholic/tickets/todo/a-qmu-jp/20260618172215-audit-log-mutations.md)),
add a `gdrive-ftp log` subcommand that lets a human browse that history
interactively — a small, **tig-like** terminal viewer: `j` moves the cursor down,
`k` up, with a couple of other obvious keys, and a clean exit. It is **read-only**
(it never touches Drive or the log) and adds **no new dependencies** — it is built on
the standard library plus the already-vendored `golang.org/x/term` (the same raw-mode
machinery the interactive shell already uses).

**Decisions (locked):**
- **Keys:** `j`/`↓` down, `k`/`↑` up, `g`/`G` top/bottom, `q`/`Esc`/`Ctrl-C` quit; optional
  `Enter` to expand the selected entry's full detail. Read-only; no mutation keys.
- **No new deps:** stdlib + `golang.org/x/term` only. No bubbletea/tview/tcell. Use
  `term.MakeRaw` + a manual 1-byte read loop (the shell's `term.NewTerminal.ReadLine` is
  line-oriented and unsuitable for single-keystroke navigation).
- **Dual reachability:** when stdout is a TTY, open the TUI; when piped/non-TTY **or** when
  `-json` is given, print the entries to stdout (text rows, or a JSON array under `-json`)
  so scripts and AI agents reach the same data without the TUI. (The raw JSONL file remains
  directly greppable regardless.)
- **Auth-free:** browsing the local log needs no Drive auth, so the subcommand branches in
  `main.go` **before** `auth.Client`, exactly like `completion zsh`.

## Key Files

- `main.go` — wire the `log` subcommand as a guard clause among `auth`/`completion`/`__complete` (lines ~36–67), branching **before** `auth.Client` (line ~57) since it needs no Drive auth. Resolve the log path via `defaultLogPath()` (added by the foundation ticket) and respect the existing `-json` flag and TTY detection.
- `internal/audit/` *(from the foundation ticket)* — add a reader: `Read(path) ([]Entry, error)` (and/or a streaming/tail reader) that parses the JSONL log and the rotated segments in chronological order, tolerating a trailing partial line. The `Entry` type and its fields come from the foundation ticket; this ticket only consumes them.
- `internal/audit/browser.go` *(new, or `internal/shell` if it must reuse `crlfWriter`)* — the raw-mode TUI: load entries, render a scrollable list (newest first), track a cursor + viewport, handle the keypress loop, and restore the terminal on exit. Reuse the `term.MakeRaw`/`defer term.Restore`/`crlfWriter` pattern from `internal/shell/shell.go` `runTerminal()` (lines ~102–162). Single-keystroke input = `os.Stdin.Read(buf[:1])` after `MakeRaw`.
- `internal/shell/output.go` — for the non-TTY/`-json` dump, reuse the `emit`/encoder conventions so the `log -json` output matches the rest of the CLI's JSON contract (an array of entry objects).
- `internal/audit/audit_test.go` — unit-test the reader (parse JSONL incl. rotated segments, skip a partial trailing line, order) and the pure TUI state logic (cursor/viewport math: clamping at top/bottom, page scrolling, `g`/`G`). The raw-mode I/O loop itself is hard to unit-test; keep it thin and push logic into pure, tested functions.
- `README.md`, `plugins/gdrive-ftp/skills/gdrive-ftp/SKILL.md` — document `gdrive-ftp log` (keys, read-only, non-TTY/`-json` behavior) in the **same commit**.

## Related History

Builds directly on the audit-log foundation and reuses the raw-mode TUI precedent.

Past tickets that touched similar areas:

- [20260618172215-audit-log-mutations.md](.workaholic/tickets/todo/a-qmu-jp/20260618172215-audit-log-mutations.md) — **Prerequisite.** Defines the JSONL log, its location, and the `Entry`/`Operation` types this browser reads. `depends_on` points here.
- [20260616114548-shell-tab-completion.md](.workaholic/tickets/archive/work-20260616-073652/20260616114548-shell-tab-completion.md) — Established the raw-mode `golang.org/x/term` interactive pattern (`term.MakeRaw`, restore on exit, `crlfWriter`) and the "no new third-party dependency" rule the browser reuses.
- [20260618115619-json-output-format.md](.workaholic/tickets/archive/work-20260618-095217/20260618115619-json-output-format.md) — The `-json`/`emit` contract the non-TTY dump mode mirrors so the `log -json` output is consistent with every other command.

## Implementation Steps

1. **Reader (`internal/audit`).** `Read(path) ([]Entry, error)`: read the active log plus rotated segments (`.3`,`.2`,`.1`,active in chronological order), `json.Unmarshal` each line into `Entry`, skip blank/partial trailing lines without erroring. Optionally cap to the most recent N to bound memory on huge logs.

2. **`log` subcommand (`main.go`).** Add `args[0] == "log"` guard before `auth.Client`. Resolve `defaultLogPath()`, read entries. If none: print a self-explanatory empty-state line ("no operations have been logged yet") and exit 0. If stdout is **not** a TTY, or `-json` is set: dump entries (text rows, or a JSON array under `-json`) and exit. Otherwise launch the TUI.

3. **TUI (`internal/audit/browser.go`).** `MakeRaw(stdin)` + `defer Restore`; render the list newest-first through a `crlfWriter`; maintain `cursor` and `top` (viewport) indices. Loop: read 1 byte, dispatch `j`/`k`/`g`/`G`/`Enter`/`q`/`Esc`/`Ctrl-C`, re-render. Factor cursor/viewport updates into pure functions (`moveDown`, `moveUp`, `clampViewport`) so they are unit-tested. Each row shows: time, operation (in the CLI's vocabulary — `uploaded`/`trashed`/`created`), name, and a short id; `Enter` expands the full entry (parent, drive, replaced id/size, cwd).

4. **Self-explanatory states & exits** (Self-Explanatory UI + Modeless policies). Show a one-line key hint footer (`j/k move · enter detail · q quit`). Handle the empty state explicitly. Offer multiple exits (`q`, `Esc`, `Ctrl-C`) and never trap the user; the browser is observation-only and must not mutate anything.

5. **Tests.** Reader: parses multi-segment logs in order, tolerates a partial line. TUI state: `moveDown`/`moveUp` clamp at bounds, viewport scrolls correctly past the screen height, `g`/`G` jump to ends. Non-TTY dump: `-json` emits a valid entry array.

6. **Docs (same commit).** README: a `gdrive-ftp log` subsection (keys table, read-only note, non-TTY/`-json` behavior). SKILL.md: note that the agent should prefer `gdrive-ftp log -json` (or grepping `audit.jsonl`) over the TUI, since the TUI is the human path.

## Considerations

- **No new dependencies** (`go.mod`, `internal/audit/browser.go`). The binding constraint: build on stdlib + `golang.org/x/term` (already vendored). Adding a TUI library would, per Conservative Vendor Dependence, require a full dependency-decision log — and is exactly the "trivial feature" case the policy says to implement ourselves. Keep the viewer minimal so the overhead stays low, as the user asked.
- **Read-only and modeless** (`internal/audit/browser.go`). The browser never writes the log or touches Drive. It is a separate, stateless subcommand reachable any time, with multiple clear exits — it must not be a mode the user can get trapped in (Modeless Design).
- **Dual reachability — TUI is the human path, not the only path** (`main.go`, `internal/audit`). The Accessibility policy treats the AI-reachable path as primary: the non-TTY/`-json` dump and the raw greppable JSONL file are how the agent reviews history; the TUI is sugar over the same data. Do **not** make the TUI the only way to read the log.
- **Self-explanatory, all states designed** (`internal/audit/browser.go`). Loading is instant (local file), but the **empty** state must explain itself ("no operations logged yet"), and a corrupt/partial line must not crash the viewer — skip it. Labels use the CLI's own action vocabulary (`uploaded`/`trashed`/`created`), not internal enum names.
- **Raw-mode safety** (`internal/audit/browser.go`). Always `defer term.Restore` so a panic or early exit can't leave the terminal in raw mode; degrade gracefully (fall back to the plain dump) if `MakeRaw` fails, mirroring `runTerminal()`'s fallback to `runScanner()`.
- **Testability boundary** (`internal/audit/audit_test.go`). The raw-mode keypress loop is inherently hard to unit-test; keep it a thin shell and push all cursor/viewport/render-selection logic into pure functions that are fully covered — consistent with the project's "pure helpers are tested, I/O edges are thin" pattern.

---
created_at: 2026-06-18T11:56:19+09:00
author: a@qmu.jp
type: enhancement
layer: [UX, Domain, Infrastructure]
effort:
commit_hash:
category:
depends_on:
---

# Machine-readable JSON output via a global `-json` flag

## Overview

Every gdrive-ftp command writes human-formatted text today (`ls` rows like
`        gdoc  2026-06-12 14:55  notes`, lines like `uploaded … -> … (820.4KB)`),
which scripts and AI agents must scrape. Add a global **`-json` boolean flag**
that switches all command output to compact, machine-readable JSON.

Per the confirmed scope, **every command emits JSON** under `-json`:

```
$ gdrive-ftp -json ls "/My Drive/Work"
[{"name":"report.pdf","id":"1A2b","mimeType":"application/pdf","isFolder":false,"size":840000,"modifiedTime":"2026-06-10T11:02:00Z"}]

$ gdrive-ftp -json put ./r.pdf id:0Bx
{"action":"uploaded","name":"r.pdf","id":"1A2b","size":840000}

$ gdrive-ftp -json get /nope
{"error":"no such file or directory"}      # → stderr, exit 1
```

**Output contract:** results go to **stdout** as a single JSON value (an **array**
for `ls`, a single **object** for `get`/`put`/`mkdir`/`rm`/`pwd`); errors go to
**stderr** as `{"error":"…"}` with **exit 1**, preserving the existing
stdout-results / stderr-errors / non-zero-exit contract that the gdrive-ftp skill
already documents. Output is compact (one line, newline-terminated) so it is
ndjson- and pipe-friendly.

## Key Files

- `main.go` — flag parsing (`-creds`/`-token` at lines 28–33) is where `-json` is added; `shell.New(ctx, client, os.Stdout)` (line ~72) is where the format is threaded in; `fatal` (lines ~134–137) is the one-shot error→stderr→exit-1 path that must emit `{"error":…}` in JSON mode. `completeForShell` (lines ~91–108) constructs a Shell for completion and must stay **plain** — completion is never JSON.
- `internal/shell/shell.go` — the `Shell` struct (lines 32–38, holds `out io.Writer`) gains a format field; `New` (line ~42) gains a parameter/option. `Execute` (one-shot, ~163–175) and `dispatch` (interactive, ~508–523) are the two error sites; `friendlyErr` (~180–210) is the error-rewrite contract a JSON error must wrap, not replace.
- `internal/shell/commands.go` — **primary edit site.** Every `fmt.Fprintf(s.out, …)` success site branches on the format: `ls` rows (53, 76), `listDrives` (89), `get` (172/174), `put` (~277), `mkdir` (~303), `rm` (~318), `pwd` (122). `lls`/`lpwd`/`lcd`/`help` are interactive-only local helpers and stay text (out of one-shot/agent scope).
- `internal/shell/output.go` *(new)* — owned DTO types + a renderer seam (see steps). Keeps serialization out of the command bodies.
- `internal/gdrive/client.go` — source of the data to serialize: `Ref` (41–45) and the `*drive.File` fields populated by `fileFields` (line 34: `id,name,mimeType,size,modifiedTime,md5Checksum,parents`, plus `driveId` via `GetByID`). `IsFolder`/`IsGoogleDoc` classify entries. This is the vendor boundary the JSON DTO is translated from.
- `internal/shell/shell_test.go` — table-driven, network-free tests; the `Shell` already takes an `io.Writer`, so JSON output is testable by injecting a `bytes.Buffer` (no existing golden text to update).
- `README.md` and `plugins/gdrive-ftp/skills/gdrive-ftp/SKILL.md` — document the `-json` flag and the per-command JSON shapes (mandatory, same commit — this is a CLI-surface change).

## Related History

No prior ticket adds JSON output; the relevant archived work established the
surfaces this extends — the agent/scripting stdout-stderr contract, the `ls`
rendering sites, and the modeless-flag precedent.

Past tickets that touched similar areas:

- [20260616142105-claude-skill-gdrive-ftp-usage.md](.workaholic/tickets/archive/work-20260616-073652/20260616142105-claude-skill-gdrive-ftp-usage.md) — Defined the agent consumption model (one-shot, results on stdout, errors on stderr + non-zero exit) that the JSON contract upgrades; its SKILL.md must be updated when JSON ships.
- [20260616074105-virtual-root-list-all-drives.md](.workaholic/tickets/archive/work-20260616-073652/20260616074105-virtual-root-list-all-drives.md) — Built the current `cmdLs`/`listDrives` text rendering the JSON branch sits beside (same file: `internal/shell/commands.go`).
- [20260618095212-accept-drive-id-for-commands.md](.workaholic/tickets/archive/work-20260618-095217/20260618095212-accept-drive-id-for-commands.md) — Most recent CLI-surface change; precedent for modeless/agent-composable design and the standing rule that README + SKILL.md are updated in the same commit as any surface change.

## Implementation Steps

1. **Owned output DTOs (`internal/shell/output.go`).** Per Conservative Vendor
   Dependence, never `json.Marshal` a `*drive.File`. Define owned types with
   stable, domain-vocabulary JSON tags, e.g.:
   - `fileEntry{ Name, ID, MimeType string; IsFolder bool; Size int64 (omitempty); ModifiedTime string (omitempty) }` — for `ls` rows and drive entries.
   - `actionResult{ Action, Name, ID, Dest, MimeType string; Size int64 }` (omitempty as appropriate) — for `get`/`put`/`mkdir`/`rm`.
   - `pwdResult{ Path string }`, `errorResult{ Error string }`.
   Add a `toFileEntry(*drive.File) fileEntry` translator (and a `Ref`→entry for
   drive listings). Express the cases the human strings currently smuggle as typed
   fields: folder vs file via `IsFolder`, Google-native via `MimeType`, size
   omitted for folders/gdocs (not the `"-"`/`"gdoc"` sentinels from `sizeStr`).

2. **Format selector on the Shell.** Add a field to `Shell` (`json bool`, or a
   small `format` enum to leave room for the `-format` future) and thread it
   through `New` (a parameter or functional option). Add a render seam — e.g.
   `func (s *Shell) emit(v any, text func()) error` — that, in JSON mode, encodes
   `v` to `s.out` (compact, `Encoder` with `SetEscapeHTML(false)`, newline-
   terminated) and otherwise runs `text()`. Command bodies build the owned value,
   then call `emit`, keeping rendering out of the logic (Domain Layer Separation).

3. **Wire the flag (`main.go`).** Add `jsonOut := flag.Bool("json", false, "emit machine-readable JSON output")`; pass its value into `shell.New`. Leave the `completeForShell` Shell in text mode (completion never JSON).

4. **Convert each command (`internal/shell/commands.go`).** Replace each success
   `fmt.Fprintf(s.out, …)` with: build the owned DTO, then `emit`. `ls` collects a
   `[]fileEntry` (folders keep no trailing `/` in JSON — that is a text affordance)
   and emits the array; `listDrives` emits drive entries; `get` emits
   `{action:"exported|downloaded", name, dest, size, mimeType?}`; `put` emits
   `{action:"uploaded", name, id, size}`; `mkdir` emits `{action:"created", name, id}`;
   `rm` emits `{action:"trashed", name, id}`; `pwd` emits `{path}`.

5. **JSON error path.** In JSON mode, emit `errorResult{Error: friendlyErr(err).Error()}`
   to **stderr** and keep **exit 1**. Implement at the one-shot boundary (`main`’s
   call to `fatal`, which already owns stderr+exit; pass it the format so it
   serializes instead of printing the plain line) and at interactive `dispatch`
   for consistency. Do not change `friendlyErr`’s rewrite logic — wrap its result.

6. **Tests (`internal/shell/shell_test.go`).** Inject a `bytes.Buffer` as the Shell
   `out`, set JSON mode, and assert exact serialized output for: an `ls` listing
   (array, fields, size omitted for a folder/gdoc), a `put`/`mkdir`/`rm` action
   object, `pwd`, and an `errorResult`. Unit-test `toFileEntry` directly
   (folder→isFolder true + no size; binary→size present; gdoc→mimeType set, size
   omitted; `modifiedTime` passthrough).

7. **Docs (mandatory, same commit).** Update `README.md` (document the `-json`
   flag in the Flags table and add a short "JSON output" subsection with the
   per-command shapes and the stdout/stderr/exit contract) and
   `plugins/gdrive-ftp/skills/gdrive-ftp/SKILL.md` (the agent-facing doc — describe
   `-json`, the result shapes, and that errors are `{"error":…}` on stderr with
   exit 1). The CLI surface is not "done" until both reflect the flag.

## Patches

> **Note**: speculative — verify line numbers and signatures against current code before applying.

### `main.go`

```diff
@@ flag parsing
 	creds := flag.String("creds", defaultCredsPath(), "path to OAuth client credentials.json")
 	token := flag.String("token", defaultTokenPath(), "path to the cached auth token")
+	jsonOut := flag.Bool("json", false, "emit machine-readable JSON output")
 	flag.Usage = usage
 	flag.Parse()
```

### `internal/shell/shell.go`

```diff
@@ type Shell struct
 	cwd  []gdrive.Ref // path from the virtual root; empty means the virtual root
 	out  io.Writer
+	json bool          // emit machine-readable JSON instead of human text
 	term *term.Terminal // set only while the interactive line editor is active
 }
```

## Considerations

- **No vendor leakage into the public contract** (`internal/shell/output.go`, `internal/gdrive/client.go`). The Google Drive `drive.File` struct must never be serialized directly; translate to an owned DTO so the JSON shape is decoupled from the SDK's field names and can stay stable as the SDK changes. (`workaholic:implementation` Conservative Vendor Dependence; the dependency rule keeps `drive/v3` confined to the `internal/gdrive` boundary.)
- **Rendering is a separate concern** (`internal/shell/commands.go`). Factor output through the `emit` seam so command logic returns/builds domain values and the renderer serializes; do not deepen the current inline-`fmt.Fprintf` interleaving. (`workaholic:implementation` Domain Layer Separation: thin entry point interprets args, calls domain, formats the result.)
- **Stable, self-explanatory, AI-reachable contract** (`README.md`, `SKILL.md`). JSON keys use domain vocabulary (`name`, `mimeType`, `isFolder`, `size`, `modifiedTime`, `id`), with typed cases instead of display sentinels; results→stdout, errors→stderr, exit codes preserved. This is the machine-readable surface agents consume under one contract. (`workaholic:implementation` Accessibility for Humans and AI; `workaholic:design` Self-Explanatory UI.)
- **Modeless flag, not a mode** (`main.go`). `-json` is a composable per-invocation attribute applied uniformly across verbs; it introduces no stateful toggle, so an agent can compose any one-shot sequence. (`workaholic:design` Modeless Design.)
- **Completion stays plain** (`main.go` `completeForShell`, `internal/shell/shell.go` `Complete`). The `-json` flag must not affect Tab/zsh completion candidate output — only command results.
- **Typed cases over display strings** (`internal/shell/commands.go` `sizeStr`/`modTime`). Today `sizeStr` returns `"-"` (folder) and `"gdoc"` (Google-native) in the size column and `modTime` blank-pads on parse failure; the JSON model must use real fields (`isFolder`, omitted `size`, raw RFC3339 `modifiedTime`) rather than reusing these human artifacts. (`workaholic:implementation` Preferring Rich Typing.)
- **Interactive vs one-shot error sites** (`internal/shell/shell.go` `Execute`/`dispatch`, `main.go` `fatal`). One-shot errors already route to stderr+exit-1 via `fatal`; interactive errors print inline. Ensure JSON-mode errors serialize at both, with `friendlyErr` still doing the rewrite.
- **Scope boundary** (`internal/shell/commands.go`). `lls`/`lpwd`/`lcd`/`help` are interactive local-filesystem/help helpers outside the agent one-shot contract; they remain text. Only the remote-operation commands (`ls`, `get`, `put`, `mkdir`, `rm`, `pwd`) and the error path are in scope.

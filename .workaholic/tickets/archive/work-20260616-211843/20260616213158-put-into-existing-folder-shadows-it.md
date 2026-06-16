---
created_at: 2026-06-16T21:31:58+09:00
author: a@qmu.jp
type: bugfix
layer: [UX, Domain]
effort: 0.5h
commit_hash: 400c067
category: Changed
depends_on:
---

# `put <file> "/drive/Folder"` creates a same-named file shadowing the folder instead of uploading into it

## Overview

`put` to a destination that is an **existing folder** (no trailing slash) does
not upload the file *into* that folder. Instead it creates a new file in the
folder's **parent**, named after the folder, so the folder and a same-named
file now coexist. Every subsequent name-based command on that path then fails
with `ambiguous name (multiple matches); rename to disambiguate`, and because
`rm` is also name-based the stray file cannot be removed with the CLI either —
the path is effectively wedged.

This contradicts the README, whose own example implies upload-into-folder
without a trailing slash:

```sh
# Upload: replaces a single exact-name match, else creates a new file
gdrive-ftp put ./report.pdf "/My Drive/Work"
```

### Observed (real reproduction, 2026-06-16)

```sh
gdrive-ftp put seiho-target-matrix.pdf  "/My Drive/00_作業"   # -> uploaded ... -> 00_作業 (798.1KB)
gdrive-ftp put sonpo-coverage-matrix.pdf "/My Drive/00_作業"  # -> uploaded ... -> 00_作業 (617.7KB)
gdrive-ftp ls  "/My Drive/00_作業"
# gdrive-ftp: 00_作業: ambiguous name (multiple matches); rename to disambiguate
gdrive-ftp ls  "/My Drive"
#            -  ...  00_作業/        <- the real folder
#      617.7KB  ...  00_作業         <- stray file named after the folder (2nd upload replaced the 1st)
```

Both uploads created a file literally named `00_作業` in `/My Drive`; the second
replaced the first via the existing "replace exact-name match" path, so the
first file's contents were also silently lost. The user expected two PDFs
**inside** `/My Drive/00_作業/`.

### Root cause

`cmdPut` (`internal/shell/commands.go:248-263`) only treats the destination as a
directory when the argument ends in `/`. Otherwise it unconditionally splits the
arg into `dir`/`base` via `splitPath`, resolves `dir` as the parent, and uses
`base` as the **upload filename** — never checking whether `base` already names
an existing folder:

```go
dir, base := splitPath(args[1])
if strings.HasSuffix(args[1], "/") {
    dir, base = args[1], ""        // only here is base==folder honored
}
if dir != "" || strings.HasPrefix(args[1], "/") {
    if parent, err = s.resolveDir(dir); err != nil { return err }
}
if base != "" {
    name = base                    // <- "00_作業" becomes the new FILE name
}
```

So `put f "/My Drive/00_作業"` resolves parent=`/My Drive`, name=`00_作業`, and
uploads `f` as a file called `00_作業` next to the folder. Only the trailing-slash
form `"/My Drive/00_作業/"` uploads into the folder. This is the opposite of
standard FTP `put` and of `get`, which uploads/downloads *into* a target dir when
the target is a directory (`get` into an existing local dir keeps the remote name
— `internal/shell/commands.go` / README).

## Key Files

- `internal/shell/commands.go` - `cmdPut` (lines 231-274). The
  `dir`/`base`/trailing-slash logic at 247-263 is what must change: when `base`
  resolves to an existing folder, treat the full arg as the destination directory
  and keep the local filename.
- `internal/shell/shell.go` - `resolveDir` (line 600), `resolveFile` (633),
  `splitPath` (674). `resolveDir(args[1])` succeeds iff the whole path is a
  directory — the cheapest existence-of-folder probe. `FindDir`/`FindOne` on the
  client are the lookup primitives.
- `internal/gdrive/client.go` - `IsFolder` (line 61), `Upload` (214). `Upload`
  already has "replace single exact-name match" semantics (222), which is exactly
  why the second stray upload clobbered the first — note this in tests.
- `README.md` - The `put` examples and the "replaces a single exact-name match,
  else creates a new file" line; clarify the into-folder-vs-rename rule once the
  behavior is fixed.

## Implementation Steps

1. In `cmdPut`, before deciding `name`, probe whether `args[1]` (sans any
   trailing slash) is itself an existing folder. The simplest robust check:
   attempt `s.resolveDir(args[1])` — if it succeeds, the whole arg is a directory,
   so set `parent` to that stack and keep `name = filepath.Base(local)`.
2. Only when `args[1]` does **not** resolve to a directory, fall back to the
   current split: `dir`/`base`, resolve `dir` as parent, use `base` as the
   rename target (`put ./a.jpg "/My Drive/Photos/b.jpg"` must still rename).
3. Keep the explicit trailing-slash form working (it already means "into this
   dir"); after step 1 it becomes a subset of the general case.
4. Decide and document the collision policy: if a folder *and* a file share the
   destination's final name (the wedged state this bug produces), `resolveDir`
   will still find the folder by mime filter — confirm `FindDir` filters on
   `mimeType = folder` so it is not itself ambiguous, and prefer uploading into
   the folder.
5. Update `README.md` so the `put ./report.pdf "/My Drive/Work"` example is
   explicitly "into the Work folder", and document that a final component which
   does not name an existing folder is treated as the target filename.
6. Tests in `internal/shell` (table-driven, following existing
   `*_test.go` style): (a) `put` into an existing folder path with no trailing
   slash lands inside the folder under the local basename; (b) `put` to
   `dir/newname` with a non-existent final component still renames; (c) trailing
   slash still means into-dir; (d) regression: two sequential `put`s into the
   same folder produce two distinct files inside it, not one shadowing file in the
   parent. Run `go build ./...`, `go vet ./...`, `go test ./...`.

## Considerations

- **Data loss, not just confusion**: because `Upload` replaces an exact-name
  match, repeated `put`s to the folder path silently overwrite each other in the
  parent. The fix removes the foot-gun, but the wedged-path recovery (a folder and
  file sharing a name) is unreachable from the current name-only CLI — consider a
  follow-up allowing `rm`/`get`/`ls` to target by file ID, or a `--id` selector,
  so an already-wedged path can be cleaned without the Drive web UI.
- Keep the `dir/newname` rename path intact — distinguishing "final component is
  an existing folder" (into-dir) from "final component is a new name" (rename) is
  the whole point; do not collapse them.
- `resolveDir(args[1])` does one extra round-trip on the happy path; acceptable
  for a one-shot CLI, and it is the authoritative existence check. Avoid
  reimplementing folder detection by string heuristics.
- Mirror `get`'s into-directory semantics so `put`/`get` stay symmetric and FTP
  expectations hold.
- Names are matched exactly + case-sensitively elsewhere; keep that invariant in
  the probe.

## Notes

This was hit live moving two coverage-matrix report PDFs
(`seiho-target-matrix.pdf`, `sonpo-coverage-matrix.pdf`) from the data-platform
repo into `/My Drive/00_作業`. Cleanup of the current stray `/My Drive/00_作業`
file + re-upload of both PDFs into the folder is being handled out of band (needs
the Drive web UI or an ID-based operation, since the CLI can't disambiguate the
name today — see the first Consideration).

## Final Report

Development completed as planned. `cmdPut` now probes `resolveDir(dest)` first:
if the whole destination resolves to an existing folder it uploads INTO it under
the local basename; only otherwise does it fall back to the `dir`/`base` split
(rename). The trailing-slash form is naturally subsumed (it resolves as a dir).
README's `put` row clarified.

Verified live against Drive: two sequential `put`s into an existing folder (no
trailing slash) both landed inside it with no stray shadow file in the parent;
the rename form (`put f dir/new.pdf`) and trailing-slash form still work. Temp
folder trashed and local temp removed after.

### Discovered Insights

- **Insight**: the fix makes `put` symmetric with `get` (both go *into* a target
  directory) by using `resolveDir(dest)` as the authoritative "is this a folder?"
  probe — one extra round-trip on the happy path, deliberately chosen over a
  string heuristic so the wedged folder+file collision still resolves to the
  folder (FindDir filters on `mimeType = folder`).
- **Not unit-tested (and why)**: the destination decision is network-bound
  (`resolveDir` → `FindDir`), and the repo has no Drive-client mock — only pure
  helpers (`splitPath`, completion, etc.) are unit-tested. Regression was verified
  by live round-trip instead. A follow-up could introduce a client interface seam
  to make `cmdPut`/`cmdGet` destination logic table-testable offline.

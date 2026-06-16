---
created_at: 2026-06-16T07:41:05+09:00
author: a@qmu.jp
type: enhancement
layer: [UX, Domain]
effort: 1h
commit_hash: ef18922
category: Added
depends_on: [20260616074104-shared-drives-client-support.md]
---

# Virtual root: list all drives (My Drive + Shared Drives) as the top level

## Overview

Today the shell starts at My Drive's root (`currentID` returns `RootID` for the
empty cwd stack), and `pwd` `/` is My Drive. This ticket replaces that with a
synthetic **virtual root**: the initial directory lists, as top-level
directories, **My Drive** plus every Shared Drive the user can access. The drive
name becomes the first path component, so paths read like `/My Drive/Reports` and
`/Engineering Team/specs/api.md`.

Builds on `20260616074104-shared-drives-client-support.md` (the client's
`ListDrives` and the `driveID`-aware `List`/`Find*` calls).

Behavior:

- The empty cwd stack is the **virtual root** (not My Drive). `pwd` prints `/`.
- `ls` at the virtual root lists drive entries: `My Drive/` first, then each
  Shared Drive (folder-styled). No file/transfer operation is valid at the
  virtual root itself — those error with a clear "cd into a drive first" message.
- `cd <Drive Name>` (e.g. `cd "My Drive"`) enters a drive; the first path
  segment of any absolute path selects the drive. `My Drive` resolves to root
  folder `root` with an empty `driveID`; a Shared Drive resolves to its
  `driveId` as both the root folder ID and the drive context.
- Inside a drive, `ls`/`cd`/`get`/`put`/`mkdir`/`rm`/`..`/`.` work as today but
  carry the current drive's `driveID` into client calls.

## Key Files

- `internal/shell/shell.go` - The core change. Add a `DriveID` field to
  `gdrive.Ref` (or carry drive context in the stack's first element); make the
  empty stack mean the virtual root; add a `currentDriveID(stack)` helper
  (`stack[0].DriveID`, `""` at virtual root / My Drive). Update `currentID`
  (lines 122-129), `pwd` (lines 109-120), `resolveDir` (lines 131-152),
  `resolveFile` (lines 154-179), `startStack` (lines 183-190) so the first
  segment selects a drive and later segments resolve within it.
- `internal/shell/commands.go` - `cmdLs` (lines 34-71) special-cases the virtual
  root to list drives (`ListDrives` + synthesized `My Drive`); `cmdCd`
  (lines 73-84) allows `cd /` back to the virtual root; `cmdPut`/`cmdMkdir`/
  `cmdGet`/`cmdRm` must pass the current `driveID` and reject operation at the
  virtual root.
- `internal/gdrive/client.go` - Consumes the foundation ticket: `ListDrives`,
  and `List`/`FindDir`/`FindOne` now take `driveID`. `Ref` may gain `DriveID`
  here (it is the natural home for the path-element type).
- `main.go` - Wires `shell.New`; the initial cwd stays empty (now = virtual
  root) so no signature change is expected, but verify the startup banner/text.
- `internal/shell/shell_test.go` - Add table-driven tests for `pwd`,
  `currentID`, `currentDriveID`, and `startStack` across the virtual root, My
  Drive, and a Shared Drive (these are pure, no network).
- `README.md` - Remove the "Access is scoped to My Drive (shared drives are not
  traversed)" note; document the virtual root and the drive-as-first-path-
  component model.

## Related History

No prior archived tickets; the only sibling work is the foundation ticket this
one depends on (see Considerations).

## Implementation Steps

1. Add `DriveID string` to `gdrive.Ref` (empty for ordinary folders and My
   Drive). The virtual root's drive entries carry it: `My Drive` →
   `Ref{ID: "root", Name: "My Drive", DriveID: ""}`; a Shared Drive →
   `Ref{ID: <driveId>, Name: <name>, DriveID: <driveId>}`.
2. Treat the empty cwd stack as the virtual root. `currentDriveID(stack)` returns
   `stack[0].DriveID` (or `""` when empty). `currentID` keeps returning the tip
   folder ID, but guard against being called at the virtual root (no real
   folder) — callers must check for the virtual root first.
3. `resolveDir`: when starting from the virtual root (absolute path, or empty
   stack), resolve the **first** segment against the drive list — match `My
   Drive` or a Shared Drive name (case-sensitive; reuse `ErrNotFound`/ambiguity
   semantics), pushing the drive `Ref`. Resolve subsequent segments via
   `FindDir(ctx, currentDriveID(stack), currentID(stack), seg)` within that
   drive.
4. `resolveFile`: same first-segment drive selection; a bare file name at the
   virtual root is an error ("no such drive" / "cd into a drive first").
5. `cmdLs`: if the resulting stack is the virtual root, call `ListDrives`,
   prepend the synthesized `My Drive`, and print each as a directory (`name/`).
   Otherwise list `currentID(stack)` children with `currentDriveID(stack)`.
6. `cmdCd`: `cd` / `cd /` returns to the virtual root (empty stack); `cd "My
   Drive"` / `cd "<Shared Drive>"` enters a drive.
7. `cmdGet`/`cmdPut`/`cmdMkdir`/`cmdRm`: pass `currentDriveID` into client calls
   and reject when the target resolves to the virtual root.
8. Update `pwd` so the virtual root prints `/` and drive paths render
   `/<Drive Name>/...` (already true once the drive Ref is `cwd[0]`).
9. Update `README.md` (remove the My-Drive-only limitation, document the virtual
   root) and the startup banner if it implies My Drive.
10. Add the pure-logic tests; run `go build ./...`, `go vet ./...`,
    `go test ./...`.

## Considerations

- **Depends on** `20260616074104-shared-drives-client-support.md` — it must be
  implemented first (provides `ListDrives` and the `driveID` parameter). This
  ticket changes the `List`/`Find*` call sites from `""` to `currentDriveID`.
- Virtual-root edge cases to cover (`internal/shell/commands.go`): `ls` with no
  drives shared still shows `My Drive`; `get`/`put`/`mkdir`/`rm` at `/` must fail
  with a helpful message, not a confusing API error; `cd ..` from a drive root
  returns to the virtual root.
- Name collisions: a Shared Drive literally named `My Drive`, or two Shared
  Drives with the same name, must surface as ambiguity (reuse `ErrAmbiguous`)
  rather than silently picking one (`internal/gdrive/client.go` lines 124-160).
- Keep the modeless/composable path contract: absolute paths like
  `/Engineering/specs` and relative `../Photos`, `.`/`..` must work uniformly
  across My Drive and Shared Drives, with no "select a drive" mode
  (`internal/shell/commands.go`).
- `pwd` of a Shared Drive should be unambiguous and round-trippable by `cd`
  (quote names with spaces — the tokenizer already supports quotes,
  `internal/shell/shell.go` lines 204-238).
- Preserve the anti-corruption boundary and `Ref` as the path-element type; do
  not leak `*drive.Drive`/`*drive.File` selection logic into the shell beyond the
  existing `*drive.File` listing surface (`internal/gdrive/client.go`).
- Update `README.md`'s Project layout/Notes so the documented behavior matches.

## Final Report

Development completed as planned.

### Discovered Insights

- **Insight**: `cmdLs`'s old fast path for listing a folder argument
  (`stack = []gdrive.Ref{{ID, Name}}`) silently dropped drive context. Inside a
  Shared Drive that would have queried the wrong corpus, so the folder-listing
  branch now re-resolves the arg via `resolveDir` to thread `DriveID`. The lesson:
  once a `Ref` carries `DriveID`, never reconstruct a stack element by hand —
  always go through `resolveDir` so the drive id propagates.
  **Context**: The drive context lives on `cwd[0]` and is read by
  `currentDriveID`; any code that builds a partial stack breaks that invariant.
- **Insight**: `ls` cannot tell a drive name from a file name by syntax alone, so
  `singleDriveArg` routes a single top-level component (absolute, or bare at the
  virtual root) through `resolveDir`. This keeps the modeless contract — `ls Team`
  and `ls /Team` work without a "select a drive" mode.

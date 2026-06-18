---
created_at: 2026-06-18T09:52:12+09:00
author: a@qmu.jp
type: enhancement
layer: [UX, Domain, Infrastructure]
effort:
commit_hash:
category:
depends_on:
---

# Accept a Google Drive file/folder ID (`id:` prefix) as a target for up/download and other commands

## Overview

Today every gdrive-ftp command resolves its remote target purely by name-based path
navigation from the virtual root: the shell walks slash-separated segments, looking each
name up as a child of the current folder. There is no way to address a file or folder by
its Google Drive ID, even though every low-level Drive operation is already ID-keyed
internally.

Add an explicit **`id:` prefix** that lets the user supply a raw Drive ID anywhere a remote
target is expected. The prefix is opt-in and unambiguous, so it never collides with a real
filename and introduces no "mode" — `get id:1A2b3C…` is just another argument form of the
same `get` verb.

Applies to **all target-taking commands**: `get`, `put`, `rm`, `ls`, `cd`, and `mkdir`.

```
get  id:1A2b3CdEfGh local.txt      # download a file by ID
put  report.pdf id:0BxParentFolder # upload into a folder by ID
rm   id:1A2b3CdEfGh                # trash by ID
ls   id:0BxParentFolder            # list a folder by ID
cd   id:0BxParentFolder            # cd into a folder by ID
mkdir id:0BxParent/NewFolder       # create under a parent identified by ID
```

**Motivation / origin.** This is the explicit follow-up named in the archived bugfix
ticket `20260616213158-put-into-existing-folder-shadows-it.md`: a same-named file and folder
can coexist ("wedged" state) and the name-only CLI cannot disambiguate or clean it up. An
ID is inherently unambiguous, so ID-based `rm`/`get` is the recovery path for exactly that
collision.

## Key Files

- `internal/shell/shell.go` — `resolveFile` (line 633) and `resolveDir`/`startStack` (line 600) are the two choke points that map a user path to a `*drive.File` / directory stack. The `id:` branch belongs here so every command inherits it. `splitPath`, `currentID`, `currentDriveID` are the helpers around them.
- `internal/shell/commands.go` — command handlers `cmdGet` (126), `cmdPut` (231/248), `cmdRm` (307), `cmdMkdir` (281), `cmdLs`, `cmdCd`. They already route through `resolveFile`/`resolveDir`; usage/help strings here need updating.
- `internal/gdrive/client.go` — thin Drive v3 wrapper. `Ref{ID,Name,DriveID}` (40), `Download` (244) shows the `Files.Get(...).SupportsAllDrives(true)` pattern. **Missing piece:** a `GetByID(fileID)` metadata-fetch method to turn an ID into a `*drive.File` (with name, mimeType, parents, driveId).
- `internal/shell/shell_test.go` — table-driven, network-free test style. The `id:` detection logic should be a pure helper to fit this pattern.
- `main.go` — entry point; confirms positional args flow `sh.Execute(args)` → command map untouched. No per-command flag parsing exists, which is why an `id:` string prefix (not a `--id` flag) is the right surface.
- `plugins/gdrive-ftp/skills/gdrive-ftp/SKILL.md` and `README` — user-facing docs to update with the `id:` form.

## Related History

The path-resolution model and per-command argument routing this builds on were established
by the virtual-root and tab-completion tickets; the ID feature is the disambiguation escape
hatch that the put-shadowing bugfix explicitly asked for.

Past tickets that touched similar areas:

- [20260616213158-put-into-existing-folder-shadows-it.md](.workaholic/tickets/archive/work-20260616-211843/20260616213158-put-into-existing-folder-shadows-it.md) — Originating context: recommends ID-based `rm`/`get`/`ls` so a wedged folder+file name collision can be cleaned without the Drive web UI (touches the exact `cmdPut`/`resolveDir`/`resolveFile` path).
- [20260616074105-virtual-root-list-all-drives.md](.workaholic/tickets/archive/work-20260616-073652/20260616074105-virtual-root-list-all-drives.md) — Defines `resolveDir`/`resolveFile`/`currentID`/`currentDriveID` and the `Ref{ID,Name,DriveID}` cwd stack that ID resolution must integrate with (DriveID must still be threaded).
- [20260616114548-shell-tab-completion.md](.workaholic/tickets/archive/work-20260616-073652/20260616114548-shell-tab-completion.md) — Documents per-command remote/local arg routing (get=remote+local, put=local+remote) that the ID form extends.
- [20260616074104-shared-drives-client-support.md](.workaholic/tickets/archive/work-20260616-073652/20260616074104-shared-drives-client-support.md) — Provides the driveID-aware client primitives (`List`/`FindDir`/`FindOne`) a direct-ID `Files.Get` lookup sits beside.

## Implementation Steps

1. **Pure detection helper.** Add `parseIDArg(seg string) (id string, ok bool)` (or `idPrefix` constant + helper) in `internal/shell` that recognizes the `id:` prefix on a path **segment** and returns the bare ID. Unit-test it in `shell_test.go` (prefix present/absent, empty after prefix, prefix mid-path is not special, case sensitivity of the literal `id:`).

2. **Client metadata-by-ID method.** Add `GetByID(ctx, fileID) (*drive.File, error)` to `internal/gdrive/client.go`, mirroring `Download`'s `Files.Get(id).SupportsAllDrives(true)` call and requesting the same field set the name-lookup path uses (`id, name, mimeType, size, parents, driveId`). Map a 404 to the existing `ErrNotFound` so callers get a consistent "no such target" error.

3. **`resolveFile` branch (covers `get`, `rm`).** At the top of `resolveFile`, if the whole path parses as `id:X`, call `GetByID(X)` and return the `*drive.File` directly — bypassing `splitPath`/name navigation. Existing kind checks downstream still apply (`cmdGet` rejects a folder; `cmdRm` trashes whatever it resolves).

4. **`startStack`/`resolveDir` branch (covers `cd`, `ls`, `put` dest, `mkdir` parent).** When the **first** segment of a path is `id:X`, seed the directory stack from that ID instead of cwd/root: `GetByID(X)`, verify it is a folder, and push `Ref{ID: f.Id, Name: f.Name, DriveID: f.DriveId}`. Then continue walking any remaining name segments normally. This makes `cd id:X`, `ls id:X`, `put f id:X`, and `mkdir id:X/Name` all work through one code path.

5. **Thread DriveID correctly.** `Files.Get` returns `driveId` only for Shared Drive items (empty for My Drive). Set `Ref.DriveID` from it so subsequent `currentDriveID(stack)` calls scope correctly; My Drive items keep the empty DriveID the rest of the code already expects.

6. **Usage / docs (mandatory, same change).** This change alters the CLI/command surface, so the docs MUST be updated in the same commit — not deferred. Update the `get`/`put`/`rm`/`ls`/`cd`/`mkdir` usage strings in `commands.go`, the `README` command table, **and** `plugins/gdrive-ftp/skills/gdrive-ftp/SKILL.md` to document the `id:` form with a short example per command. Treat the skill doc and README as part of the public API: any command-surface change ships with its doc update.

7. **Optional — completion.** Tab completion need not (and should not) try to complete opaque IDs; leave the completion logic to fall through cleanly when the current token starts with `id:` so it does not emit spurious candidates.

## Patches

> **Note**: Both patches are speculative — exact field set, error mapping, and signatures should be verified against the current code before applying.

### `internal/gdrive/client.go`

```diff
--- a/internal/gdrive/client.go
+++ b/internal/gdrive/client.go
@@ -244,6 +244,17 @@ func (c *Client) Download(ctx context.Context, fileID string, w io.Writer) (int6
 	resp, err := c.srv.Files.Get(fileID).SupportsAllDrives(true).Context(ctx).Download()
 	// ...
 }
+
+// GetByID fetches a single file or folder's metadata by its Drive ID, so an
+// id:-prefixed argument can be resolved without name navigation. driveId is
+// returned for Shared Drive items and empty for My Drive.
+func (c *Client) GetByID(ctx context.Context, fileID string) (*drive.File, error) {
+	f, err := c.srv.Files.Get(fileID).
+		Fields("id, name, mimeType, size, parents, driveId").
+		SupportsAllDrives(true).Context(ctx).Do()
+	if err != nil {
+		return nil, err // TODO: map 404 -> ErrNotFound to match the name path
+	}
+	return f, nil
+}
```

### `internal/shell/shell.go`

```diff
--- a/internal/shell/shell.go
+++ b/internal/shell/shell.go
@@ -633,6 +633,11 @@ func (s *Shell) resolveFile(path string) (*drive.File, error) {
+	if id, ok := parseIDArg(path); ok {
+		return s.c.GetByID(s.ctx, id)
+	}
 	dir, base := splitPath(path)
 	switch base {
 	case "":
 		return nil, fmt.Errorf("%s: invalid path", path)
```

## Considerations

- **Modeless / composable design** (`internal/shell/commands.go`). The `id:` prefix is an argument form, not a mode or wizard, so scripts and AI agents can compose `get id:<id>` without coordinating any toggle state — consistent with `workaholic:design` Modeless Design. Do **not** add a `--by-id` flag/mode.
- **Domain-layer separation** (`internal/shell/shell.go` vs `main.go`). The ID-vs-path discrimination and resolution stay in `internal/shell`; `main.go` gains nothing and keeps passing raw positional args to `sh.Execute`. Vendor (`drive.File`) types must not leak into the detection helper's signature. (`workaholic:implementation` Domain Layer Separation.)
- **Vendor boundary stays thin** (`internal/gdrive/client.go`). A Drive ID is a vendor-opaque token; the only new boundary surface is `GetByID`, which reuses the existing `Files.Get`/`SupportsAllDrives` pattern. The shell must not import the Drive SDK directly. (`workaholic:implementation` Conservative Vendor Dependence.)
- **Docs are not optional** (`README`, `plugins/gdrive-ftp/skills/gdrive-ftp/SKILL.md`). Per standing project guidance, any change to the CLI/API surface ships with the skill doc and README updated in the same commit. The implementation is not "done" until the `id:` form is documented in both.
- **Self-explanatory UI & no-guess safety** (`internal/shell/commands.go`, `README`). Update usage/help/README/skill so the `id:` form is discoverable. Errors must stay actionable via the existing `friendlyErr`: a malformed/non-existent ID → a clear "no such target", an ID of the wrong kind (folder to `get`, file as a `put`/`cd`/`mkdir` parent) → an explicit kind error. The "refuse to guess on ambiguity" guarantee (`ErrAmbiguous`) is *strengthened* by IDs, since an ID hits exactly one object or fails loudly. (`workaholic:design` Self-Explanatory UI.)
- **DriveID threading for Shared Drives** (`internal/shell/shell.go` `startStack`, `currentDriveID`). An ID-seeded stack frame must carry the file's `driveId` so later client calls scope to the right corpus; verify `cd id:<shared-drive-folder>` then `ls`/`put` operate within that Shared Drive. (Builds on the virtual-root / shared-drives tickets.)
- **`mkdir`/`splitPath` interaction** (`internal/shell/shell.go`). `id:PARENT` contains a colon and no slash; ensure `splitPath` and `startStack` treat a leading `id:` segment as a stack seed rather than a literal name, so `mkdir id:PARENT/NewName` creates `NewName` under the parent (and `mkdir id:PARENT` alone is a usage error, since there is no new-folder name).
- **Testability** (`internal/shell/shell_test.go`). Keep `parseIDArg` pure and unit-tested. The actual `GetByID` resolution path has no coverage today (no `Client` interface/mock); note this debt — introducing a client seam to test resolution is out of scope here but worth a future ticket. (`workaholic:implementation` Active Use of Unit Tests.)

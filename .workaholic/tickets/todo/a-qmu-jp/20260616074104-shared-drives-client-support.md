---
created_at: 2026-06-16T07:41:04+09:00
author: a@qmu.jp
type: enhancement
layer: [Infrastructure, Domain]
effort:
commit_hash:
category:
depends_on:
---

# Shared Drives support in the gdrive client

## Overview

The `gdrive.Client` wrapper can only see **My Drive**: every listing/lookup is
keyed by a folder ID and corpora defaults to the user's drive, so Shared Drives
(team drives) are invisible and file operations inside them are unreliable. This
ticket makes the client Shared-Drive-aware — the *foundation* for the virtual
root delivered by the sibling ticket
`20260616074105-virtual-root-list-all-drives.md`.

Two capabilities:

1. **Enumerate Shared Drives** — add `ListDrives(ctx)` that calls
   `Drives.List` and returns each accessible Shared Drive as a domain `Ref`
   (`id`, `name`). My Drive is *not* returned here; the shell synthesizes it.
2. **Drive-context-aware operations** — thread a `driveID` (empty = My Drive /
   default corpora) through the listing/lookup calls so they target the right
   corpus. When `driveID` is non-empty, set `Corpora("drive")`,
   `DriveId(driveID)` and `IncludeItemsFromAllDrives(true)` (plus the already
   present `SupportsAllDrives(true)`); when empty, keep today's behavior.

This change is backward compatible: with `driveID == ""` everywhere, My Drive
behaves exactly as now, so the build stays green before the shell ticket lands.

## Key Files

- `internal/gdrive/client.go` - All Drive API calls live here. Add `ListDrives`;
  add a `driveID` parameter to `List` (lines 72-92) and `FindChildren`
  (lines 94-122), and propagate it through `FindDir`/`FindOne`/`FindAll` so
  navigation/transfer inside a Shared Drive sets the corpora/driveId flags.
  `RootID` constant (line 23) and the sentinel errors stay. `Mkdir`, `Upload`,
  `Download`, `Trash` already chain `SupportsAllDrives(true)`; `Export` needs no
  change (no SupportsAllDrives option in the v3 client).
- `internal/shell/shell.go` / `internal/shell/commands.go` - Current callers of
  `List`/`FindDir`/`FindOne`. Their call sites must compile against the new
  signature; in this ticket they pass `driveID == ""` (the sibling ticket starts
  passing real drive IDs).
- `internal/auth/auth.go` - Confirms `drive.DriveScope` (full) is already
  granted, so `Drives.List` and Shared-Drive file ops need no new OAuth scope.
- `internal/gdrive` (new `client_test.go`, optional) - No tests exist for this
  package; the pure query-building helpers are the testable surface.

## Related History

No prior archived tickets touch Drive enumeration; this is a new project. A
sibling ticket in the same queue (`20260616074105-virtual-root-list-all-drives.md`)
consumes this foundation — see Considerations.

## Implementation Steps

1. Add `ListDrives(ctx context.Context) ([]Ref, error)` to `client.go`:
   `c.srv.Drives.List().PageSize(100).Fields("nextPageToken, drives(id,name)").Pages(ctx, ...)`,
   appending `Ref{ID: d.Id, Name: d.Name}` for each. Paginate transparently like
   `List` does. Document it: returns Shared Drives only (My Drive is synthesized
   by the caller).
2. Add a `driveID string` parameter to `List` and `FindChildren`. Extract a small
   helper that, given a `*drive.FilesListCall` and `driveID`, applies
   `.Corpora("drive").DriveId(driveID).IncludeItemsFromAllDrives(true)` only when
   `driveID != ""` (My Drive keeps the default corpora). Keep `Spaces("drive")`
   and `SupportsAllDrives(true)`.
3. Thread `driveID` through `FindDir`, `FindOne`, and any `FindAll`/query helper
   that calls `FindChildren`, so a lookup inside a Shared Drive carries its drive
   context.
4. Update the call sites in `internal/shell/{shell,commands}.go` to pass
   `driveID == ""` so the package compiles (the sibling ticket replaces `""`
   with the current drive's ID).
5. Keep `Mkdir`/`Upload`/`Download`/`Trash` as-is (they already set
   `SupportsAllDrives(true)`); creating a file under a Shared-Drive folder ID
   works once the folder ID is resolved with the right drive context.
6. Run `go build ./...`, `go vet ./...`, `go test ./...`.

## Patches

> **Note**: Speculative — verify line numbers and the exact SDK builder method
> names (`Corpora`, `DriveId`, `IncludeItemsFromAllDrives`) before applying.

### `internal/gdrive/client.go`

```diff
@@
-// List returns the non-trashed children of folderID, folders first then by
-// name. Results are paginated transparently.
-func (c *Client) List(ctx context.Context, folderID string) ([]*drive.File, error) {
-	q := fmt.Sprintf("'%s' in parents and trashed = false", escapeQ(folderID))
-	var out []*drive.File
-	err := c.srv.Files.List().
-		Q(q).
-		Spaces("drive").
-		Fields("nextPageToken, files("+fileFields+")").
-		OrderBy("folder,name_natural").
-		PageSize(1000).
-		SupportsAllDrives(true).
-		Pages(ctx, func(fl *drive.FileList) error {
-			out = append(out, fl.Files...)
-			return nil
-		})
+// List returns the non-trashed children of folderID, folders first then by
+// name. driveID selects a Shared Drive corpus ("" means My Drive). Results are
+// paginated transparently.
+func (c *Client) List(ctx context.Context, driveID, folderID string) ([]*drive.File, error) {
+	q := fmt.Sprintf("'%s' in parents and trashed = false", escapeQ(folderID))
+	var out []*drive.File
+	call := c.srv.Files.List().
+		Q(q).
+		Spaces("drive").
+		Fields("nextPageToken, files("+fileFields+")").
+		OrderBy("folder,name_natural").
+		PageSize(1000).
+		SupportsAllDrives(true)
+	call = withDrive(call, driveID)
+	err := call.Pages(ctx, func(fl *drive.FileList) error {
+		out = append(out, fl.Files...)
+		return nil
+	})
 	if err != nil {
 		return nil, err
 	}
 	return out, nil
 }
+
+// withDrive scopes a files-list call to a Shared Drive when driveID is set; an
+// empty driveID leaves the default (My Drive) corpus in place.
+func withDrive(call *drive.FilesListCall, driveID string) *drive.FilesListCall {
+	if driveID == "" {
+		return call
+	}
+	return call.Corpora("drive").DriveId(driveID).IncludeItemsFromAllDrives(true)
+}
+
+// ListDrives returns the Shared Drives the user can access as (id, name) Refs.
+// My Drive is not included; callers synthesize it for the virtual root.
+func (c *Client) ListDrives(ctx context.Context) ([]Ref, error) {
+	var out []Ref
+	err := c.srv.Drives.List().
+		PageSize(100).
+		Fields("nextPageToken, drives(id,name)").
+		Pages(ctx, func(dl *drive.DriveList) error {
+			for _, d := range dl.Drives {
+				out = append(out, Ref{ID: d.Id, Name: d.Name})
+			}
+			return nil
+		})
+	if err != nil {
+		return nil, err
+	}
+	return out, nil
+}
```

## Considerations

- **Sibling split**: `20260616074105-virtual-root-list-all-drives.md` depends on
  this ticket — it consumes `ListDrives` and passes real drive IDs into the new
  `driveID` parameter. Implement this one first; it must leave My Drive behavior
  unchanged so the queue stays green between commits.
- Preserve the anti-corruption boundary: `ListDrives` returns domain `Ref`, not
  `*drive.Drive`; `drive/v3` types stay inside `internal/gdrive`
  (`internal/gdrive/client.go`).
- Keep the safety invariants intact when adding `driveID`: exact case-sensitive
  re-filtering and ambiguity refusal in `FindChildren`/`FindOne` must still apply
  within a Shared Drive (`internal/gdrive/client.go` lines 94-160).
- Signature change to `List`/`FindChildren` ripples to every shell call site;
  update them all in this ticket (passing `""`) so the change is atomic per
  package (`internal/shell/commands.go`, `internal/shell/shell.go`).
- `Drives.List` only returns drives the user is a member of; drives shared as a
  single folder still surface as ordinary entries — document the scope in the
  sibling ticket's README update, not here.
- Stay stdlib + the already-adopted `google.golang.org/api/drive/v3`; no new
  dependency (`go.mod`).

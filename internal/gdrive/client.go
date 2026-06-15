// Package gdrive is a thin, FTP-flavored wrapper over the Google Drive v3 API.
// It exposes the handful of operations the shell needs: listing a folder,
// finding a child by name, making folders, uploading, downloading/exporting,
// and trashing.
package gdrive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	drive "google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

// FolderMime is the Drive MIME type that marks a file as a folder.
const FolderMime = "application/vnd.google-apps.folder"

// RootID is the alias Drive accepts for the user's root folder in queries.
const RootID = "root"

// ErrNotFound is returned when a named child does not exist in a folder.
var ErrNotFound = errors.New("no such file or directory")

// ErrAmbiguous is returned when several non-trashed children share a name and
// an operation cannot safely pick one.
var ErrAmbiguous = errors.New("ambiguous name (multiple matches); rename to disambiguate")

// fileFields is the set of File fields fetched for listings and lookups.
const fileFields = "id,name,mimeType,size,modifiedTime,md5Checksum,parents"

// Ref is a lightweight (id, name) pointer to a Drive file or folder, used to
// build the working-directory path without re-querying the API.
type Ref struct {
	ID   string
	Name string
}

// Client wraps an authenticated Drive service.
type Client struct {
	srv *drive.Service
}

// New builds a Client from an authorized HTTP client.
func New(ctx context.Context, hc *http.Client) (*Client, error) {
	srv, err := drive.NewService(ctx, option.WithHTTPClient(hc))
	if err != nil {
		return nil, fmt.Errorf("creating drive service: %w", err)
	}
	return &Client{srv: srv}, nil
}

// IsFolder reports whether f is a Drive folder.
func IsFolder(f *drive.File) bool { return f != nil && f.MimeType == FolderMime }

// IsGoogleDoc reports whether f is a native Google editor file (Docs, Sheets,
// …) that must be exported rather than downloaded byte-for-byte.
func IsGoogleDoc(f *drive.File) bool {
	return f != nil && f.MimeType != FolderMime &&
		strings.HasPrefix(f.MimeType, "application/vnd.google-apps.")
}

// escapeQ escapes a literal for use inside a Drive query string.
func escapeQ(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, "'", `\'`)
}

// List returns the non-trashed children of folderID, folders first then by
// name. Results are paginated transparently.
func (c *Client) List(ctx context.Context, folderID string) ([]*drive.File, error) {
	q := fmt.Sprintf("'%s' in parents and trashed = false", escapeQ(folderID))
	var out []*drive.File
	err := c.srv.Files.List().
		Q(q).
		Spaces("drive").
		Fields("nextPageToken, files("+fileFields+")").
		OrderBy("folder,name_natural").
		PageSize(1000).
		SupportsAllDrives(true).
		Pages(ctx, func(fl *drive.FileList) error {
			out = append(out, fl.Files...)
			return nil
		})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// FindChildren returns every non-trashed child of folderID whose name matches
// exactly. Drive's "name =" query operator is case-insensitive and applies
// Unicode normalization, so the results are re-filtered client-side for an
// exact, case-sensitive match. Returns ErrNotFound if none match.
func (c *Client) FindChildren(ctx context.Context, folderID, name string) ([]*drive.File, error) {
	q := fmt.Sprintf("name = '%s' and '%s' in parents and trashed = false",
		escapeQ(name), escapeQ(folderID))
	r, err := c.srv.Files.List().
		Q(q).
		Spaces("drive").
		Fields("files(" + fileFields + ")").
		PageSize(100).
		SupportsAllDrives(true).
		Context(ctx).
		Do()
	if err != nil {
		return nil, err
	}
	var out []*drive.File
	for _, f := range r.Files {
		if f.Name == name {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	return out, nil
}

// FindDir returns the single folder named name inside folderID. It returns a
// "not a directory" error if name resolves only to non-folders, ErrNotFound if
// nothing matches, and ErrAmbiguous if several folders share the name.
func (c *Client) FindDir(ctx context.Context, folderID, name string) (*drive.File, error) {
	matches, err := c.FindChildren(ctx, folderID, name)
	if err != nil {
		return nil, err
	}
	var dirs []*drive.File
	for _, f := range matches {
		if IsFolder(f) {
			dirs = append(dirs, f)
		}
	}
	switch len(dirs) {
	case 0:
		return nil, errors.New("not a directory")
	case 1:
		return dirs[0], nil
	default:
		return nil, ErrAmbiguous
	}
}

// FindOne returns the single child named name inside folderID. It returns
// ErrNotFound if nothing matches and ErrAmbiguous if the name is shared by more
// than one file or folder, so destructive callers never act on a guessed
// target.
func (c *Client) FindOne(ctx context.Context, folderID, name string) (*drive.File, error) {
	matches, err := c.FindChildren(ctx, folderID, name)
	if err != nil {
		return nil, err
	}
	if len(matches) > 1 {
		return nil, ErrAmbiguous
	}
	return matches[0], nil
}

// Mkdir creates a folder named name inside parentID.
func (c *Client) Mkdir(ctx context.Context, parentID, name string) (*drive.File, error) {
	f := &drive.File{Name: name, MimeType: FolderMime, Parents: []string{parentID}}
	return c.srv.Files.Create(f).
		Fields("id,name,mimeType").
		SupportsAllDrives(true).
		Context(ctx).
		Do()
}

// Upload streams r into a file named name under parentID. If exactly one
// non-folder child has that exact name its content is replaced (a new
// revision); otherwise a new file is created. Requiring a single exact match
// avoids overwriting a differently-cased or ambiguously-named neighbor.
func (c *Client) Upload(ctx context.Context, parentID, name string, r io.Reader) (*drive.File, error) {
	matches, err := c.FindChildren(ctx, parentID, name)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	var target *drive.File
	files := 0
	for _, f := range matches {
		if !IsFolder(f) {
			files++
			target = f
		}
	}
	if files == 1 {
		return c.srv.Files.Update(target.Id, &drive.File{}).
			Media(r).
			Fields("id,name,size,mimeType").
			SupportsAllDrives(true).
			Context(ctx).
			Do()
	}
	f := &drive.File{Name: name, Parents: []string{parentID}}
	return c.srv.Files.Create(f).
		Media(r).
		Fields("id,name,size,mimeType").
		SupportsAllDrives(true).
		Context(ctx).
		Do()
}

// Download streams a binary file's content to w. It must not be called for
// native Google editor files; use Export for those (see IsGoogleDoc).
func (c *Client) Download(ctx context.Context, fileID string, w io.Writer) (int64, error) {
	resp, err := c.srv.Files.Get(fileID).
		SupportsAllDrives(true).
		Context(ctx).
		Download()
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return io.Copy(w, resp.Body)
}

// Export streams a native Google editor file to w, converting it to mimeType.
func (c *Client) Export(ctx context.Context, fileID, mimeType string, w io.Writer) (int64, error) {
	resp, err := c.srv.Files.Export(fileID, mimeType).
		Context(ctx).
		Download()
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return io.Copy(w, resp.Body)
}

// Trash moves a file to the trash (a reversible delete).
func (c *Client) Trash(ctx context.Context, fileID string) error {
	_, err := c.srv.Files.Update(fileID, &drive.File{Trashed: true}).
		SupportsAllDrives(true).
		Context(ctx).
		Do()
	return err
}

// exportFormat maps each supported native Google type to a sensible download
// format and file extension.
var exportFormat = map[string]struct{ Mime, Ext string }{
	"application/vnd.google-apps.document":     {"application/vnd.openxmlformats-officedocument.wordprocessingml.document", ".docx"},
	"application/vnd.google-apps.spreadsheet":  {"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", ".xlsx"},
	"application/vnd.google-apps.presentation": {"application/vnd.openxmlformats-officedocument.presentationml.presentation", ".pptx"},
	"application/vnd.google-apps.drawing":      {"image/png", ".png"},
	"application/vnd.google-apps.script":       {"application/vnd.google-apps.script+json", ".json"},
}

// ExportFormat returns the download MIME type and file extension to use for a
// native Google editor MIME type, and whether one is known.
func ExportFormat(googleMime string) (mime, ext string, ok bool) {
	f, ok := exportFormat[googleMime]
	return f.Mime, f.Ext, ok
}

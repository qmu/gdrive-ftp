package shell

import (
	"encoding/json"
	"io"

	"gdrive-ftp/internal/gdrive"

	drive "google.golang.org/api/drive/v3"
)

// This file holds the machine-readable output layer: owned DTOs that the
// commands serialize under the global -json flag, plus the emit() render seam.
// The Google Drive SDK's *drive.File is never marshaled directly — it is
// translated into these owned types so the public JSON contract stays decoupled
// from the vendor struct shape and field names.

// fileEntry is one file or folder in machine-readable form. Folders and
// Google-native docs omit size (they have no byte length); a drive entry at the
// virtual root sets only Name/ID/IsFolder.
type fileEntry struct {
	Name         string `json:"name"`
	ID           string `json:"id"`
	MimeType     string `json:"mimeType,omitempty"`
	IsFolder     bool   `json:"isFolder"`
	Size         int64  `json:"size,omitempty"`
	ModifiedTime string `json:"modifiedTime,omitempty"`
}

// actionResult is the JSON result of a mutating/transfer command (get, put,
// mkdir, rm). Fields irrelevant to a given action are omitted.
type actionResult struct {
	Action   string `json:"action"`
	Name     string `json:"name"`
	ID       string `json:"id,omitempty"`
	Dest     string `json:"dest,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Size     int64  `json:"size,omitempty"`
}

// pwdResult is the JSON result of pwd.
type pwdResult struct {
	Path string `json:"path"`
}

// errorResult is the JSON error envelope emitted (to stderr in one-shot mode)
// when a command fails.
type errorResult struct {
	Error string `json:"error"`
}

// toFileEntry translates a Drive file into the owned DTO. Size is reported only
// for binary files; folders and Google-native docs have no byte length.
func toFileEntry(f *drive.File) fileEntry {
	e := fileEntry{
		Name:         f.Name,
		ID:           f.Id,
		MimeType:     f.MimeType,
		IsFolder:     gdrive.IsFolder(f),
		ModifiedTime: f.ModifiedTime,
	}
	if !e.IsFolder && !gdrive.IsGoogleDoc(f) {
		e.Size = f.Size
	}
	return e
}

// emit renders a command's result. In JSON mode it encodes v (compact, one line,
// newline-terminated, HTML escaping off) to the shell's output writer; otherwise
// it runs the text closure that prints the human-readable form.
func (s *Shell) emit(v any, text func()) error {
	if !s.jsonOut {
		text()
		return nil
	}
	enc := json.NewEncoder(s.out)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// encodeErrorJSON writes an {"error": …} object to w. Used for one-shot JSON
// error output on stderr (exit code is owned by the caller in main).
func encodeErrorJSON(w io.Writer, err error) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(errorResult{Error: err.Error()})
}

// EncodeErrorJSON is the exported entry point for the one-shot error path in
// main: it serializes err as a JSON {"error": …} object to w.
func EncodeErrorJSON(w io.Writer, err error) { encodeErrorJSON(w, err) }

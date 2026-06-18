package shell

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gdrive-ftp/internal/gdrive"

	drive "google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
)

func TestFilterByPrefix(t *testing.T) {
	names := []string{"Reports/", "Recipes/", "budget.xlsx", "notes"}
	got := filterByPrefix(names, "Re")
	want := []string{"Reports/", "Recipes/"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("filterByPrefix = %#v, want %#v", got, want)
	}
	if got := filterByPrefix(names, ""); len(got) != 4 {
		t.Errorf("empty prefix should match all, got %d", len(got))
	}
	if got := filterByPrefix(names, "zzz"); got != nil {
		t.Errorf("no match should be nil, got %#v", got)
	}
}

func TestLongestCommonPrefix(t *testing.T) {
	tests := []struct {
		in   []string
		want string
	}{
		{[]string{"Reports/", "Recipes/"}, "Re"},
		{[]string{"abc", "abd", "abz"}, "ab"},
		{[]string{"only/"}, "only/"},
		{[]string{"a", "b"}, ""},
		{nil, ""},
	}
	for _, tt := range tests {
		if got := longestCommonPrefix(tt.in); got != tt.want {
			t.Errorf("longestCommonPrefix(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestQuoteArg(t *testing.T) {
	if got := quoteArg("plain"); got != "plain" {
		t.Errorf("quoteArg(plain) = %q, want plain", got)
	}
	if got := quoteArg("my file"); got != `"my file"` {
		t.Errorf(`quoteArg("my file") = %q, want "my file"`, got)
	}
}

func TestLastTokenStart(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"ls", 0},
		{"ls ", 3},      // trailing space → new token at end
		{"ls Wo", 3},    // active token "Wo" starts at 3
		{"cd a/b/c", 3}, // whole path is one token
		{`get "my fi`, 4},
	}
	for _, tt := range tests {
		if got := lastTokenStart(tt.in); got != tt.want {
			t.Errorf("lastTokenStart(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestArgKind(t *testing.T) {
	tests := []struct {
		verb string
		idx  int
		want string
	}{
		{"ls", 1, "remote"},
		{"cd", 1, "remote"},
		{"get", 1, "remote"},
		{"get", 2, "local"},
		{"put", 1, "local"},
		{"put", 2, "remote"},
		{"lcd", 1, "local"},
		{"pwd", 1, ""},
		{"ls", 2, ""},
	}
	for _, tt := range tests {
		if got := argKind(tt.verb, tt.idx); got != tt.want {
			t.Errorf("argKind(%q,%d) = %q, want %q", tt.verb, tt.idx, got, tt.want)
		}
	}
}

func TestCompletionVerbs(t *testing.T) {
	verbs := completionVerbs()
	for _, want := range []string{"ls", "cd", "get", "put", "quit", "exit", "bye"} {
		found := false
		for _, v := range verbs {
			if v == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("completionVerbs missing %q", want)
		}
	}
}

func TestFriendlyErr(t *testing.T) {
	if friendlyErr(nil) != nil {
		t.Error("friendlyErr(nil) should be nil")
	}

	plain := errors.New("boom")
	if got := friendlyErr(plain); got != plain {
		t.Errorf("plain error should pass through unchanged, got %v", got)
	}

	disabled := &googleapi.Error{
		Code: 403,
		Message: "Google Drive API has not been used in project 123456789012 before or it is " +
			"disabled. Enable it by visiting https://console.developers.google.com/apis/api/" +
			"drive.googleapis.com/overview?project=123456789012 then retry.",
		Errors: []googleapi.ErrorItem{{Reason: "accessNotConfigured"}},
	}
	got := friendlyErr(disabled)
	if got == disabled || !strings.Contains(got.Error(), "Google Drive API is disabled") {
		t.Errorf("disabled-API error not rewritten: %v", got)
	}
	// The exact project-specific activation URL and project number are surfaced.
	if !strings.Contains(got.Error(), "overview?project=123456789012") {
		t.Errorf("rewritten error should include the exact activation URL: %v", got)
	}
	if !strings.Contains(got.Error(), "--project=123456789012") {
		t.Errorf("rewritten error should include the exact project number: %v", got)
	}
}

// drive stacks used across the virtual-root path tests.
var (
	rootStack    []gdrive.Ref // virtual root
	myDriveStack = []gdrive.Ref{{ID: gdrive.RootID, Name: myDriveName, DriveID: ""}}
	sharedStack  = []gdrive.Ref{
		{ID: "d1", Name: "Team", DriveID: "d1"},
		{ID: "f1", Name: "sub", DriveID: "d1"},
	}
)

func TestPwd(t *testing.T) {
	tests := []struct {
		name string
		cwd  []gdrive.Ref
		want string
	}{
		{"virtual root", rootStack, "/"},
		{"my drive", myDriveStack, "/My Drive"},
		{"shared drive subfolder", sharedStack, "/Team/sub"},
	}
	for _, tt := range tests {
		s := &Shell{cwd: tt.cwd}
		if got := s.pwd(); got != tt.want {
			t.Errorf("%s: pwd() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestCurrentID(t *testing.T) {
	tests := []struct {
		name  string
		stack []gdrive.Ref
		want  string
	}{
		{"virtual root has no folder", rootStack, ""},
		{"my drive root", myDriveStack, gdrive.RootID},
		{"shared drive tip", sharedStack, "f1"},
	}
	for _, tt := range tests {
		if got := currentID(tt.stack); got != tt.want {
			t.Errorf("%s: currentID() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestCurrentDriveID(t *testing.T) {
	tests := []struct {
		name  string
		stack []gdrive.Ref
		want  string
	}{
		{"virtual root", rootStack, ""},
		{"my drive uses default corpus", myDriveStack, ""},
		{"shared drive carries id from first element", sharedStack, "d1"},
	}
	for _, tt := range tests {
		if got := currentDriveID(tt.stack); got != tt.want {
			t.Errorf("%s: currentDriveID() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestSingleDriveArg(t *testing.T) {
	tests := []struct {
		name string
		cwd  []gdrive.Ref
		arg  string
		want bool
	}{
		{"bare name at virtual root", rootStack, "Team", true},
		{"absolute single component", myDriveStack, "/Team", true},
		{"bare name inside a drive", myDriveStack, "Reports", false},
		{"multi-component absolute", rootStack, "/Team/sub", false},
		{"root slash is not a drive", rootStack, "/", false},
	}
	for _, tt := range tests {
		s := &Shell{cwd: tt.cwd}
		if got := s.singleDriveArg(tt.arg); got != tt.want {
			t.Errorf("%s: singleDriveArg(%q) = %v, want %v", tt.name, tt.arg, got, tt.want)
		}
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"ls", []string{"ls"}},
		{"  ls   /foo  ", []string{"ls", "/foo"}},
		{`get "my file.pdf" out.pdf`, []string{"get", "my file.pdf", "out.pdf"}},
		{`put 'a b' "c d"`, []string{"put", "a b", "c d"}},
		{`cd ""`, []string{"cd", ""}},
		{"a\tb", []string{"a", "b"}},
	}
	for _, tt := range tests {
		got, err := tokenize(tt.in)
		if err != nil {
			t.Errorf("tokenize(%q) unexpected error: %v", tt.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("tokenize(%q) = %#v, want %#v", tt.in, got, tt.want)
		}
	}
}

func TestTokenizeUnterminated(t *testing.T) {
	if _, err := tokenize(`get "oops`); err == nil {
		t.Errorf("expected error for unterminated quote")
	}
}

func TestSplitPath(t *testing.T) {
	tests := []struct {
		in        string
		dir, base string
	}{
		{"foo", "", "foo"},
		{"/foo", "/", "foo"},
		{"a/b/c", "a/b/", "c"},
		{"/a/b/c", "/a/b/", "c"},
		{"foo/", "", "foo"},
		{"/", "", ""},
	}
	for _, tt := range tests {
		dir, base := splitPath(tt.in)
		if dir != tt.dir || base != tt.base {
			t.Errorf("splitPath(%q) = (%q, %q), want (%q, %q)", tt.in, dir, base, tt.dir, tt.base)
		}
	}
}

func TestParseIDArg(t *testing.T) {
	tests := []struct {
		in     string
		wantID string
		wantOK bool
	}{
		{"id:1A2b3C", "1A2b3C", true},
		{"id:0Bx_-Folder", "0Bx_-Folder", true},
		{"id:", "", false},         // empty ID after the prefix
		{"id:a/b", "", false},      // contains a slash → it's a path, not an ID
		{"Reports", "", false},     // a plain name
		{"ID:1A2b3C", "", false},   // prefix is case-sensitive
		{"xid:1A2b3C", "", false},  // prefix must be at the start
		{"my id:thing", "", false}, // prefix must be at the start
	}
	for _, tt := range tests {
		gotID, gotOK := parseIDArg(tt.in)
		if gotID != tt.wantID || gotOK != tt.wantOK {
			t.Errorf("parseIDArg(%q) = (%q, %v), want (%q, %v)", tt.in, gotID, gotOK, tt.wantID, tt.wantOK)
		}
	}
}

func TestToFileEntry(t *testing.T) {
	tests := []struct {
		name string
		in   *drive.File
		want fileEntry
	}{
		{"binary file keeps size",
			&drive.File{Id: "1", Name: "a.bin", MimeType: "application/octet-stream", Size: 10, ModifiedTime: "2026-06-10T11:02:00Z"},
			fileEntry{Name: "a.bin", ID: "1", MimeType: "application/octet-stream", IsFolder: false, Size: 10, ModifiedTime: "2026-06-10T11:02:00Z"}},
		{"folder omits size",
			&drive.File{Id: "2", Name: "Work", MimeType: gdrive.FolderMime, Size: 0},
			fileEntry{Name: "Work", ID: "2", MimeType: gdrive.FolderMime, IsFolder: true}},
		{"google doc omits size",
			&drive.File{Id: "3", Name: "notes", MimeType: "application/vnd.google-apps.document", Size: 999},
			fileEntry{Name: "notes", ID: "3", MimeType: "application/vnd.google-apps.document", IsFolder: false}},
	}
	for _, tt := range tests {
		if got := toFileEntry(tt.in); got != tt.want {
			t.Errorf("%s: toFileEntry = %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

func TestEmitJSON(t *testing.T) {
	var buf bytes.Buffer
	s := &Shell{out: &buf, jsonOut: true}
	textCalled := false
	if err := s.emit(actionResult{Action: "trashed", Name: "old.pdf", ID: "1A2b"}, func() { textCalled = true }); err != nil {
		t.Fatal(err)
	}
	if textCalled {
		t.Error("text closure must not run in JSON mode")
	}
	want := `{"action":"trashed","name":"old.pdf","id":"1A2b"}` + "\n"
	if buf.String() != want {
		t.Errorf("emit JSON = %q, want %q", buf.String(), want)
	}
}

func TestEmitFileEntryArrayOmitsSize(t *testing.T) {
	var buf bytes.Buffer
	s := &Shell{out: &buf, jsonOut: true}
	entries := []fileEntry{toFileEntry(&drive.File{Id: "2", Name: "Work", MimeType: gdrive.FolderMime})}
	if err := s.emit(entries, func() {}); err != nil {
		t.Fatal(err)
	}
	want := `[{"name":"Work","id":"2","mimeType":"application/vnd.google-apps.folder","isFolder":true}]` + "\n"
	if buf.String() != want {
		t.Errorf("emit entries = %q, want %q", buf.String(), want)
	}
}

func TestEmitText(t *testing.T) {
	var buf bytes.Buffer
	s := &Shell{out: &buf, jsonOut: false}
	if err := s.emit(pwdResult{Path: "/x"}, func() { buf.WriteString("text") }); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "text" {
		t.Errorf("text mode should run the closure, got %q", buf.String())
	}
}

func TestEncodeErrorJSON(t *testing.T) {
	var buf bytes.Buffer
	encodeErrorJSON(&buf, errors.New("no such file or directory"))
	want := `{"error":"no such file or directory"}` + "\n"
	if buf.String() != want {
		t.Errorf("encodeErrorJSON = %q, want %q", buf.String(), want)
	}
}

func TestByteCount(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0B"},
		{512, "512B"},
		{1024, "1.0KB"},
		{1536, "1.5KB"},
		{1048576, "1.0MB"},
	}
	for _, tt := range tests {
		if got := byteCount(tt.n); got != tt.want {
			t.Errorf("byteCount(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestShortType(t *testing.T) {
	if got := shortType("application/vnd.google-apps.document"); got != "document" {
		t.Errorf("shortType = %q, want document", got)
	}
	if got := shortType("text/plain"); got != "text/plain" {
		t.Errorf("shortType = %q, want text/plain", got)
	}
}

func TestResolveLocalDest(t *testing.T) {
	dir := t.TempDir()
	if got := resolveLocalDest("", "remote.txt"); got != "remote.txt" {
		t.Errorf("empty local: got %q, want remote.txt", got)
	}
	if got := resolveLocalDest("named.bin", "remote.txt"); got != "named.bin" {
		t.Errorf("named local: got %q, want named.bin", got)
	}
	if got, want := resolveLocalDest(dir, "remote.txt"), filepath.Join(dir, "remote.txt"); got != want {
		t.Errorf("existing dir: got %q, want %q", got, want)
	}
	if got, want := resolveLocalDest(dir+"/", "remote.txt"), filepath.Join(dir, "remote.txt"); got != want {
		t.Errorf("trailing slash: got %q, want %q", got, want)
	}
}

func TestSaveToFileSuccess(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out.txt")
	got, n, err := saveToFile(dest, func(w io.Writer) (int64, error) {
		m, err := io.WriteString(w, "hello")
		return int64(m), err
	})
	if err != nil {
		t.Fatalf("saveToFile: %v", err)
	}
	if got != dest || n != 5 {
		t.Fatalf("saveToFile = (%q, %d), want (%q, 5)", got, n, dest)
	}
	b, err := os.ReadFile(dest)
	if err != nil || string(b) != "hello" {
		t.Fatalf("file content = %q (err %v), want hello", b, err)
	}
}

func TestSaveToFilePreservesExistingOnError(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "keep.txt")
	if err := os.WriteFile(dest, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := saveToFile(dest, func(w io.Writer) (int64, error) {
		io.WriteString(w, "partial") // partially written then fails
		return 7, errors.New("boom")
	})
	if err == nil {
		t.Fatal("expected error from failing writer")
	}
	// The pre-existing good file must be untouched, and no temp left behind.
	b, _ := os.ReadFile(dest)
	if string(b) != "original" {
		t.Fatalf("existing file clobbered: got %q, want original", b)
	}
	entries, _ := os.ReadDir(filepath.Dir(dest))
	if len(entries) != 1 {
		t.Fatalf("leftover temp files: %d entries, want 1", len(entries))
	}
}

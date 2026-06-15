package shell

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gdrive-ftp/internal/gdrive"
)

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

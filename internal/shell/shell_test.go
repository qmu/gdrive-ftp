package shell

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

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

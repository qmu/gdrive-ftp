package auth

import (
	"encoding/base64"
	"io"
	"os"
	"testing"
)

func TestCodeFromRedirect(t *testing.T) {
	const state = "st4te"
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "full redirect URL with matching state",
			input: "http://127.0.0.1:1/?state=st4te&code=4/abc-DEF&scope=drive",
			want:  "4/abc-DEF",
		},
		{
			name:    "state mismatch is rejected",
			input:   "http://127.0.0.1:1/?state=wrong&code=4/abc",
			wantErr: true,
		},
		{
			name:    "error param surfaces as denial",
			input:   "http://127.0.0.1:1/?error=access_denied&state=st4te",
			wantErr: true,
		},
		{
			name:  "bare code is returned as-is",
			input: "4/abc-DEF_ghi",
			want:  "4/abc-DEF_ghi",
		},
	}
	for _, tt := range tests {
		got, err := codeFromRedirect(tt.input, state)
		if tt.wantErr {
			if err == nil {
				t.Errorf("%s: expected error, got code %q", tt.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tt.name, err)
			continue
		}
		if got != tt.want {
			t.Errorf("%s: codeFromRedirect = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// TestCopyToClipboard verifies the OSC 52 escape sequence framing and base64
// payload written to stderr.
func TestCopyToClipboard(t *testing.T) {
	const url = "https://accounts.google.com/o/oauth2/auth?x=1"

	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	copyToClipboard(url)
	w.Close()
	os.Stderr = old

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(url)) + "\x07"
	if string(out) != want {
		t.Errorf("copyToClipboard wrote %q, want %q", out, want)
	}
}

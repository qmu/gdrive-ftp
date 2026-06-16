package auth

import (
	"encoding/base64"
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

// TestClipboardSeq verifies the OSC 52 framing and base64 payload, plain and
// wrapped for tmux passthrough.
func TestClipboardSeq(t *testing.T) {
	const url = "https://accounts.google.com/o/oauth2/auth?x=1"
	b64 := base64.StdEncoding.EncodeToString([]byte(url))

	t.Run("plain", func(t *testing.T) {
		t.Setenv("TMUX", "")
		want := "\x1b]52;c;" + b64 + "\x07"
		if got := clipboardSeq(url); got != want {
			t.Errorf("clipboardSeq = %q, want %q", got, want)
		}
	})

	t.Run("tmux passthrough", func(t *testing.T) {
		t.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
		// Expected: DCS prefix + OSC 52 with every ESC doubled + ST terminator.
		want := "\x1bPtmux;\x1b\x1b]52;c;" + b64 + "\x07\x1b\\"
		if got := clipboardSeq(url); got != want {
			t.Errorf("clipboardSeq(tmux) = %q, want %q", got, want)
		}
	})
}

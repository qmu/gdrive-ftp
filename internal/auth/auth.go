// Package auth handles the Google OAuth2 flow for the Drive API and caches the
// resulting token on disk so the flow only runs once.
package auth

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	drive "google.golang.org/api/drive/v3"
)

// Client builds an authorized *http.Client for the Drive API. On first use it
// runs the OAuth consent flow and caches the token at tokenPath; subsequent
// runs reuse (and silently refresh) that token.
//
// credsPath points at an OAuth "Desktop app" client_credentials.json file
// downloaded from the Google Cloud Console. Consent is done entirely over the
// terminal (copy the URL out, paste the redirect URL back), so it works the
// same on a local machine or a headless/SSH host.
func Client(ctx context.Context, credsPath, tokenPath string) (*http.Client, error) {
	b, err := os.ReadFile(credsPath)
	if err != nil {
		return nil, fmt.Errorf("reading credentials %q: %w\n"+
			"Download an OAuth \"Desktop app\" client from the Google Cloud Console "+
			"(APIs & Services > Credentials) and save it there, or pass -creds.", credsPath, err)
	}
	// DriveScope grants full read/write access, which upload/download/mkdir/rm need.
	config, err := google.ConfigFromJSON(b, drive.DriveScope)
	if err != nil {
		return nil, fmt.Errorf("parsing credentials: %w", err)
	}

	tok, err := tokenFromFile(tokenPath)
	if err != nil {
		tok, err = tokenFromWeb(ctx, config)
		if err != nil {
			return nil, err
		}
		if err := saveToken(tokenPath, tok); err != nil {
			return nil, fmt.Errorf("caching token: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Saved authorization token to %s\n", tokenPath)
	}

	// Wrap the refreshing source so rotated tokens are written back to disk.
	src := &savingSource{base: config.TokenSource(ctx, tok), path: tokenPath, last: tok}
	return oauth2.NewClient(ctx, src), nil
}

// tokenFromWeb walks the user through the OAuth consent screen and returns the
// resulting token.
func tokenFromWeb(ctx context.Context, config *oauth2.Config) (*oauth2.Token, error) {
	state, err := randomState()
	if err != nil {
		return nil, err
	}
	return consentFlow(ctx, config, state)
}

// consentFlow runs the terminal OAuth consent flow used everywhere (local or
// SSH). It prints the consent URL and offers an interactive prompt: 'c' copies
// the URL to the user's local clipboard via the OSC 52 terminal escape (the
// only channel that reaches a laptop over SSH), 'o' attempts to open a local
// browser, and anything else falls through to manual copy. After authorizing,
// the user pastes the entire http://127.0.0.1:1/...?state=...&code=... redirect
// URL their browser lands on; we extract the code and validate the state.
func consentFlow(ctx context.Context, config *oauth2.Config, state string) (*oauth2.Token, error) {
	config.RedirectURL = "http://127.0.0.1:1/"
	authURL := config.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)

	reader := bufio.NewReader(os.Stdin)
	fmt.Fprintf(os.Stderr,
		"To authorize gdrive-ftp, open this URL in your local browser:\n\n%s\n\n", authURL)
	fmt.Fprint(os.Stderr,
		"Press 'c' then Enter to copy the URL to your local clipboard, "+
			"'o' then Enter to try opening it in a browser here, "+
			"or just copy it manually, then press Enter: ")
	choice, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("reading choice: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "c":
		copyToClipboard(authURL)
		fmt.Fprintln(os.Stderr, "Copied to clipboard.")
	case "o":
		openBrowser(authURL)
	}

	fmt.Fprint(os.Stderr,
		"\nAfter you authorize, the browser redirects to a http://127.0.0.1:1/...\n"+
			"URL that fails to load. Paste that entire URL here (or just the code): ")
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("reading redirect URL: %w", err)
	}
	code, err := codeFromRedirect(strings.TrimSpace(line), state)
	if err != nil {
		return nil, err
	}
	return config.Exchange(ctx, code)
}

// codeFromRedirect extracts the OAuth authorization code from a pasted redirect
// URL, validating the embedded state against the expected CSRF value and
// surfacing an error= denial. If the input does not parse as a URL carrying a
// code (the user pasted the bare code), the trimmed input is returned as-is.
func codeFromRedirect(input, state string) (string, error) {
	if u, err := url.Parse(input); err == nil {
		q := u.Query()
		if e := q.Get("error"); e != "" {
			return "", fmt.Errorf("authorization denied: %s", e)
		}
		if code := q.Get("code"); code != "" {
			if q.Get("state") != state {
				return "", fmt.Errorf("state mismatch (possible CSRF); aborting")
			}
			return code, nil
		}
	}
	// Not a redirect URL carrying a code or error; treat input as the bare code.
	return input, nil
}

// copyToClipboard writes the OSC 52 terminal escape sequence to stderr, which a
// terminal emulator forwards to the local clipboard even across SSH. This is
// the only clipboard channel that reaches the user's laptop on a remote host,
// so we deliberately do not shell out to xclip/pbcopy.
func copyToClipboard(s string) {
	enc := base64.StdEncoding.EncodeToString([]byte(s))
	fmt.Fprintf(os.Stderr, "\x1b]52;c;%s\x07", enc)
}

// savingSource is an oauth2.TokenSource that persists the token whenever the
// underlying source rotates it (e.g. an access-token refresh).
type savingSource struct {
	base oauth2.TokenSource
	path string
	mu   sync.Mutex
	last *oauth2.Token
}

func (s *savingSource) Token() (*oauth2.Token, error) {
	t, err := s.base.Token()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil || t.AccessToken != s.last.AccessToken || !t.Expiry.Equal(s.last.Expiry) {
		_ = saveToken(s.path, t) // best effort; an unwritable cache must not break the session
		s.last = t
	}
	return t, nil
}

func tokenFromFile(path string) (*oauth2.Token, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	tok := &oauth2.Token{}
	if err := json.NewDecoder(f).Decode(tok); err != nil {
		return nil, err
	}
	return tok, nil
}

func saveToken(path string, tok *oauth2.Token) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(tok)
}

func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// openBrowser best-effort opens url in the user's default browser. Failure is
// silently ignored because the URL is also printed for manual use.
func openBrowser(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler"}
	default:
		cmd = "xdg-open"
	}
	args = append(args, url)
	_ = exec.Command(cmd, args...).Start()
}

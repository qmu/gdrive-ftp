// Package auth handles the Google OAuth2 flow for the Drive API and caches the
// resulting token on disk so the flow only runs once.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
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
// downloaded from the Google Cloud Console. When manual is true the consent
// code is pasted on the terminal instead of captured via a local web server,
// which is the right choice on a headless host.
func Client(ctx context.Context, credsPath, tokenPath string, manual bool) (*http.Client, error) {
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
		tok, err = tokenFromWeb(ctx, config, manual)
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
func tokenFromWeb(ctx context.Context, config *oauth2.Config, manual bool) (*oauth2.Token, error) {
	state, err := randomState()
	if err != nil {
		return nil, err
	}
	if manual {
		return manualFlow(ctx, config, state)
	}
	return loopbackFlow(ctx, config, state)
}

// loopbackFlow starts a throwaway web server on a random loopback port, opens
// the consent URL in the browser, and captures the redirect automatically.
// Google authorizes loopback redirects for "Desktop app" clients on any port,
// so no redirect URI needs to be pre-registered.
func loopbackFlow(ctx context.Context, config *oauth2.Config, state string) (*oauth2.Token, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		// Fall back to copy/paste if we cannot bind a port.
		return manualFlow(ctx, config, state)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	config.RedirectURL = fmt.Sprintf("http://127.0.0.1:%d/", port)

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		// Ignore stray requests (favicon, prefetch) that browsers send to the
		// loopback origin; only the real redirect carries OAuth parameters.
		if r.URL.Path != "/" || (q.Get("code") == "" && q.Get("error") == "" && q.Get("state") == "") {
			http.NotFound(w, r)
			return
		}
		if e := q.Get("error"); e != "" {
			http.Error(w, "Authorization failed: "+e, http.StatusBadRequest)
			errCh <- fmt.Errorf("authorization denied: %s", e)
			return
		}
		if q.Get("state") != state {
			http.Error(w, "State mismatch.", http.StatusBadRequest)
			errCh <- fmt.Errorf("state mismatch (possible CSRF); aborting")
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "No code in request.", http.StatusBadRequest)
			return
		}
		fmt.Fprintln(w, "Authorization received. You may close this tab and return to the terminal.")
		codeCh <- code
	})}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	defer srv.Close()

	authURL := config.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
	fmt.Fprintf(os.Stderr, "Opening your browser to authorize access. If it does not open, visit:\n\n%s\n\n", authURL)
	openBrowser(authURL)

	select {
	case code := <-codeCh:
		return config.Exchange(ctx, code)
	case err := <-errCh:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// manualFlow prints the consent URL and reads the resulting code from stdin.
// It still uses a loopback redirect; the user copies the code= value out of the
// (failed-to-load) redirect URL their browser lands on.
func manualFlow(ctx context.Context, config *oauth2.Config, state string) (*oauth2.Token, error) {
	config.RedirectURL = "http://127.0.0.1:1/"
	authURL := config.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
	fmt.Fprintf(os.Stderr,
		"Visit this URL to authorize, then paste the value of the \"code\" query\n"+
			"parameter from the page your browser is redirected to:\n\n%s\n\nCode: ", authURL)
	var code string
	if _, err := fmt.Scanln(&code); err != nil {
		return nil, fmt.Errorf("reading code: %w", err)
	}
	code = strings.TrimSpace(code)
	return config.Exchange(ctx, code)
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

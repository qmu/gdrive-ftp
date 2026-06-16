---
created_at: 2026-06-16T07:36:52+09:00
author: a@qmu.jp
type: enhancement
layer: [Infrastructure, UX]
effort: 0.5h
commit_hash: 9a164fa
category: Added
depends_on:
---

# Remote/SSH-friendly OAuth flow: clipboard copy and redirect-URL paste box

## Overview

`gdrive-ftp` runs on a remote EC2 host reached over SSH. The default loopback
OAuth flow opens a browser on the user's laptop but captures the redirect on a
`127.0.0.1` server running on the *server*, so the laptop browser can never
reach it. The `-manual` fallback only reads the bare `code=` value via
`fmt.Scanln` and performs no CSRF (`state`) validation.

Make the manual flow Claude-Code-style and SSH-friendly:

1. **Copy on `c`** — after the consent URL is printed, an interactive prompt
   lets the user press `c` (then Enter) to copy the URL to their **local**
   clipboard. Over SSH the only channel that reaches the laptop clipboard is the
   **OSC 52 terminal escape sequence** (`ESC ] 52 ; c ; <base64(url)> BEL`),
   which the terminal emulator forwards locally. Do **not** shell out to
   `xclip`/`pbcopy` — those write the remote host's clipboard. Offer `o` (then
   Enter) to best-effort open a browser on the host, and otherwise fall through
   to manual copy.
2. **Paste box** — after authorizing, the browser lands on a
   `http://127.0.0.1:1/...?state=...&code=...` URL that fails to load. Prompt
   the user to paste that **entire** URL; parse it with `net/url`, validate
   `state` matches the generated CSRF value, surface an `error=` param as a
   denial, reject an empty `code`, then exchange the extracted code. Tolerate a
   user pasting just the bare code too.

The auto-loopback path stays the default; this upgrades the `-manual` path
(and loopback's fallback to it). The change is confined to `internal/auth`.

## Key Files

- `internal/auth/auth.go` - Primary file. `manualFlow` (lines 140-155) holds the
  paste/exchange logic to rewrite; `tokenFromWeb` (lines 64-75) dispatches
  manual vs loopback; `loopbackFlow` (lines 81-138) already shows the canonical
  `state`/`error`/empty-`code` validation to mirror; `randomState`/`openBrowser`
  (lines 209-232) show the helper + best-effort idioms to follow.
- `main.go` - Thin entry point; defines the `-manual` flag (line 29) that routes
  to `manualFlow`. May need only a wording tweak — keep it thin.
- `internal/auth/auth_test.go` - New file. Table-driven unit tests for the pure
  helpers (OSC 52 encoding, redirect-URL parsing/validation), following the
  `internal/shell/shell_test.go` stdlib-`testing` style.
- `go.mod` - Confirms the stdlib-only posture (all third-party requires are
  `// indirect`). `net/url` is stdlib, so no new dependency is introduced.
- `README.md` - Flag table + SSH/headless narrative describing `-manual`; update
  to reflect the copy-on-`c` and paste-URL behavior.

## Implementation Steps

1. Add `net/url` (and `bufio`) to the `internal/auth/auth.go` import block.
   `encoding/base64` is already imported (used by `randomState`).
2. Add an unexported `copyToClipboard(url string)` helper that writes the OSC 52
   sequence to `os.Stderr`:
   `fmt.Fprintf(os.Stderr, "\x1b]52;c;%s\x07", base64.StdEncoding.EncodeToString([]byte(url)))`.
   Best-effort and non-fatal, with a leading doc comment (mirror `openBrowser`).
   Note: use `base64.StdEncoding` here (OSC 52 expects standard base64), distinct
   from the `RawURLEncoding` used for `state`.
3. Add an unexported `codeFromRedirect(input, state string) (string, error)`
   helper: `url.Parse(input)`; if it parses and carries a `code` query param,
   surface any `error=` param, require `state` to match (reuse the exact
   `"state mismatch (possible CSRF); aborting"` phrasing from `loopbackFlow`),
   reject empty `code`, and return the code. If the input does not parse as a URL
   carrying a code, treat the whole trimmed input as the bare code.
4. Rewrite `manualFlow` to: keep `config.RedirectURL = "http://127.0.0.1:1/"`,
   build `authURL`, print it to `os.Stderr`, then read input with a
   `bufio.NewReader(os.Stdin)` (not `fmt.Scanln`, so long pasted URLs read
   whole). Present the `c`/`o`/manual choice, then prompt for the pasted redirect
   URL, call `codeFromRedirect`, and `config.Exchange(ctx, code)`.
5. (Optional) Tweak the `-manual` flag help text in `main.go` and update the
   `README.md` flag table + SSH paragraph to describe the new behavior.
6. Add `internal/auth/auth_test.go` with table-driven tests for
   `codeFromRedirect` (full redirect URL, bare code, `state` mismatch, `error=`
   param, empty/malformed input) and the OSC 52 base64 encoding. These need no
   network. Run `go build ./...`, `go vet ./...`, `go test ./...`.

## Patches

> **Note**: These patches are speculative — they express the intended shape from
> the discovery snippets; verify line numbers and adjust before applying.

### `internal/auth/auth.go`

```diff
@@
 import (
 	"context"
+	"bufio"
 	"crypto/rand"
 	"encoding/base64"
 	"encoding/json"
 	"errors"
 	"fmt"
 	"net"
 	"net/http"
+	"net/url"
 	"os"
 	"os/exec"
 	"path/filepath"
 	"runtime"
 	"strings"
 	"sync"
```

```diff
-// manualFlow prints the consent URL and reads the resulting code from stdin.
-// It still uses a loopback redirect; the user copies the code= value out of the
-// (failed-to-load) redirect URL their browser lands on.
-func manualFlow(ctx context.Context, config *oauth2.Config, state string) (*oauth2.Token, error) {
-	config.RedirectURL = "http://127.0.0.1:1/"
-	authURL := config.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
-	fmt.Fprintf(os.Stderr,
-		"Visit this URL to authorize, then paste the value of the \"code\" query\n"+
-			"parameter from the page your browser is redirected to:\n\n%s\n\nCode: ", authURL)
-	var code string
-	if _, err := fmt.Scanln(&code); err != nil {
-		return nil, fmt.Errorf("reading code: %w", err)
-	}
-	code = strings.TrimSpace(code)
-	return config.Exchange(ctx, code)
-}
+// manualFlow runs the remote/SSH-friendly consent flow. It prints the consent
+// URL and offers an interactive prompt: 'c' copies the URL to the user's local
+// clipboard via the OSC 52 terminal escape (the only channel that reaches a
+// laptop over SSH), 'o' attempts to open a local browser, anything else falls
+// through to manual copy. The user then pastes the entire
+// http://127.0.0.1:1/...?state=...&code=... redirect URL their browser lands
+// on; we extract the code and validate the state.
+func manualFlow(ctx context.Context, config *oauth2.Config, state string) (*oauth2.Token, error) {
+	config.RedirectURL = "http://127.0.0.1:1/"
+	authURL := config.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
+	reader := bufio.NewReader(os.Stdin)
+	fmt.Fprintf(os.Stderr,
+		"To authorize gdrive-ftp, open this URL in your local browser:\n\n%s\n\n", authURL)
+	fmt.Fprint(os.Stderr,
+		"Press 'c' then Enter to copy the URL to your local clipboard, "+
+			"'o' then Enter to open it in a browser here, "+
+			"or copy it manually, then press Enter: ")
+	choice, err := reader.ReadString('\n')
+	if err != nil {
+		return nil, fmt.Errorf("reading choice: %w", err)
+	}
+	switch strings.ToLower(strings.TrimSpace(choice)) {
+	case "c":
+		copyToClipboard(authURL)
+		fmt.Fprintln(os.Stderr, "Copied to clipboard.")
+	case "o":
+		openBrowser(authURL)
+	}
+	fmt.Fprint(os.Stderr,
+		"\nAfter you authorize, the browser redirects to a http://127.0.0.1:1/...\n"+
+			"URL that fails to load. Paste that entire URL here (or just the code): ")
+	line, err := reader.ReadString('\n')
+	if err != nil {
+		return nil, fmt.Errorf("reading redirect URL: %w", err)
+	}
+	code, err := codeFromRedirect(strings.TrimSpace(line), state)
+	if err != nil {
+		return nil, err
+	}
+	return config.Exchange(ctx, code)
+}
+
+// codeFromRedirect extracts the OAuth authorization code from a pasted redirect
+// URL, validating the embedded state against the expected CSRF value and
+// surfacing an error= denial. If the input does not parse as a URL carrying a
+// code (the user pasted the bare code), the trimmed input is returned as-is.
+func codeFromRedirect(input, state string) (string, error) {
+	u, err := url.Parse(input)
+	if err != nil || u.Query().Get("code") == "" {
+		return input, nil
+	}
+	q := u.Query()
+	if e := q.Get("error"); e != "" {
+		return "", fmt.Errorf("authorization denied: %s", e)
+	}
+	if q.Get("state") != state {
+		return "", fmt.Errorf("state mismatch (possible CSRF); aborting")
+	}
+	return q.Get("code"), nil
+}
+
+// copyToClipboard writes the OSC 52 terminal escape sequence to stderr, which a
+// terminal emulator forwards to the local clipboard even across SSH. This is
+// the only clipboard channel that reaches the user's laptop on a remote host,
+// so we deliberately do not shell out to xclip/pbcopy.
+func copyToClipboard(s string) {
+	enc := base64.StdEncoding.EncodeToString([]byte(s))
+	fmt.Fprintf(os.Stderr, "\x1b]52;c;%s\x07", enc)
+}
```

## Considerations

- Preserve the existing OAuth security invariants when accepting a pasted URL:
  validate `state`, surface `error=`, and reject an empty `code` — mirror
  `loopbackFlow`'s checks rather than exchanging whatever string is pasted
  (`internal/auth/auth.go` lines 101-115).
- The read mechanism changes from `fmt.Scanln` to `bufio.Reader.ReadString` so
  long pasted URLs (and the `c`/`o` choice line) read whole; verify EOF/Ctrl-D
  still returns a clean error (`internal/auth/auth.go` lines 149-152).
- OSC 52 is honored by most modern terminals but not all; the consent URL must
  remain printed as the always-available fallback so the flow degrades
  gracefully when the escape is swallowed (modeless/accessibility lens).
- Keep the auto-loopback path as the default and `-manual` as the reachable
  alternative — do not force every user into the paste flow (`main.go` line 29,
  `internal/auth/auth.go` lines 71-74).
- Stay stdlib-only: `net/url`, `bufio`, `encoding/base64` are all standard
  library, honoring the project's conservative vendor-dependence posture
  (`go.mod`); do not add a TUI/clipboard package.
- Keep the logic in `internal/auth`; `main.go` stays a thin entry point
  (domain-layer separation).
- Add `internal/auth/auth_test.go` — the auth package currently has no tests;
  the new pure helpers are easily covered without network access following the
  `internal/shell/shell_test.go` style.
- Update `README.md`'s `-manual` description so the documented UX matches.

## Final Report

Development completed as planned.

### Discovered Insights

- **Insight**: `codeFromRedirect` must check `error=` *before* requiring `code=`.
  A denial redirect carries `error=access_denied` with no `code`, so the original
  "bail to bare code when code is empty" guard would have swallowed the denial and
  fed the whole URL to `Exchange`. The final shape parses once, returns the denial
  if `error` is present, then handles `code`+`state`, and only treats the input as
  a bare code when it is not a redirect URL at all.
  **Context**: Mirrors the validation `loopbackFlow` already does server-side; the
  paste path now has parity (CSRF + denial + empty-code handling).
- **Insight**: `copyToClipboard` is unit-testable by swapping `os.Stderr` for an
  `os.Pipe` — no refactor to inject a writer was needed, keeping the helper as a
  tiny best-effort sink that mirrors `openBrowser`.

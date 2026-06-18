// Package shell implements the interactive, FTP-like command loop over a
// gdrive.Client: ls, cd, pwd, get, put, mkdir, rm and local-side helpers.
package shell

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"

	"gdrive-ftp/internal/gdrive"

	"golang.org/x/term"
	drive "google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
)

// myDriveName is the label of the synthetic My Drive entry shown at the virtual
// root alongside the user's Shared Drives.
const myDriveName = "My Drive"

// Shell holds the session state: the Drive client and the current remote
// working directory. The cwd is the chain of path elements from the virtual
// root; an empty cwd means the virtual root itself, whose entries are the
// available drives. The first element of a non-empty cwd is always a drive
// (My Drive or a Shared Drive) and carries its DriveID.
type Shell struct {
	ctx  context.Context
	c    *gdrive.Client
	cwd  []gdrive.Ref // path from the virtual root; empty means the virtual root
	out  io.Writer
	term *term.Terminal // set only while the interactive line editor is active
}

// New creates a Shell positioned at the virtual root, which lists My Drive and
// every accessible Shared Drive.
func New(ctx context.Context, c *gdrive.Client, out io.Writer) *Shell {
	return &Shell{ctx: ctx, c: c, out: out}
}

// command is a single REPL verb.
type command struct {
	run   func(s *Shell, args []string) error
	usage string
	help  string
}

// commands is the dispatch table, populated in commands.go.
var commands map[string]command

// Run reads commands until EOF (Ctrl-D) or a quit verb. When interactive and
// stdin is a terminal it uses a line editor with Tab completion; otherwise it
// falls back to a plain line scanner (pipes, one-shot, non-TTY).
func (s *Shell) Run(interactive bool) error {
	if interactive && term.IsTerminal(int(os.Stdin.Fd())) {
		return s.runTerminal()
	}
	return s.runScanner(interactive)
}

// runScanner is the plain, non-interactive read loop (no line editing).
func (s *Shell) runScanner(interactive bool) error {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for {
		if interactive {
			fmt.Fprintf(s.out, "gdrive:%s> ", s.pwd())
		}
		if !sc.Scan() {
			if interactive {
				fmt.Fprintln(s.out)
			}
			break
		}
		args, err := tokenize(sc.Text())
		if err != nil {
			fmt.Fprintln(s.out, "parse error:", err)
			continue
		}
		if len(args) == 0 {
			continue
		}
		if quit := s.dispatch(args); quit {
			break
		}
	}
	return sc.Err()
}

// runTerminal drives the interactive shell through a raw-mode line editor
// (golang.org/x/term) so the user gets line editing and Tab completion. It
// degrades to runScanner if raw mode cannot be entered. While active, command
// output is routed through a CRLF-translating writer so plain "\n" lines render
// correctly in raw mode.
func (s *Shell) runTerminal() error {
	fd := int(os.Stdin.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		return s.runScanner(true)
	}
	defer term.Restore(fd, old)

	rw := struct {
		io.Reader
		io.Writer
	}{os.Stdin, os.Stdout}
	t := term.NewTerminal(rw, "")
	t.AutoCompleteCallback = s.autoComplete
	s.term = t
	prevOut := s.out
	s.out = crlfWriter{os.Stdout}
	defer func() { s.term = nil; s.out = prevOut }()

	for {
		t.SetPrompt(fmt.Sprintf("gdrive:%s> ", s.pwd()))
		line, err := t.ReadLine()
		if errors.Is(err, io.EOF) {
			fmt.Fprint(s.out, "\n")
			return nil
		}
		if err != nil {
			return err
		}
		args, perr := tokenize(line)
		if perr != nil {
			fmt.Fprintln(s.out, "parse error:", perr)
			continue
		}
		if len(args) == 0 {
			continue
		}
		if quit := s.dispatch(args); quit {
			return nil
		}
	}
}

// crlfWriter translates a lone "\n" into "\r\n" so command output prints
// correctly while the terminal is in raw mode.
type crlfWriter struct{ w io.Writer }

func (c crlfWriter) Write(p []byte) (int, error) {
	out := make([]byte, 0, len(p)+8)
	for _, b := range p {
		if b == '\n' {
			out = append(out, '\r', '\n')
		} else {
			out = append(out, b)
		}
	}
	if _, err := c.w.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Execute runs a single command (non-interactive one-shot mode).
func (s *Shell) Execute(args []string) error {
	if len(args) == 0 {
		return nil
	}
	if name := args[0]; name == "quit" || name == "exit" || name == "bye" {
		return nil
	}
	cmd, ok := commands[args[0]]
	if !ok {
		return fmt.Errorf("unknown command %q (try 'help')", args[0])
	}
	return friendlyErr(cmd.run(s, args[1:]))
}

// friendlyErr rewrites well-known Drive API misconfigurations into an
// actionable message instead of surfacing the raw googleapi JSON dump. Unknown
// errors pass through unchanged.
func friendlyErr(err error) error {
	if err == nil {
		return nil
	}
	var ge *googleapi.Error
	if errors.As(err, &ge) && ge.Code == 403 {
		disabled := strings.Contains(ge.Message, "has not been used in project") ||
			strings.Contains(err.Error(), "SERVICE_DISABLED")
		for _, e := range ge.Errors {
			if e.Reason == "accessNotConfigured" {
				disabled = true
			}
		}
		if disabled {
			enableURL := "https://console.cloud.google.com/apis/library/drive.googleapis.com"
			project := "<your-project-id>"
			// Prefer the exact, project-specific activation URL Google returned.
			if u := activationURL(err.Error()); u != "" {
				enableURL = u
			}
			if p := projectNumber(err.Error()); p != "" {
				project = p
			}
			return fmt.Errorf("the Google Drive API is disabled for this OAuth client's Google Cloud project.\n"+
				"Enable it, wait ~1 minute for it to propagate, then retry:\n"+
				"  • Console: %s\n"+
				"  • or:      gcloud services enable drive.googleapis.com --project=%s", enableURL, project)
		}
	}
	return err
}

var (
	reActivationURL = regexp.MustCompile(`https://[^\s"]*drive\.googleapis\.com[^\s"]*overview\?project=\d+`)
	reProjectNumber = regexp.MustCompile(`project[s/=]+(\d+)`)
)

// activationURL extracts Google's exact "enable this API" console URL from an
// error message, or "" if none is present.
func activationURL(msg string) string {
	return reActivationURL.FindString(msg)
}

// projectNumber extracts the GCP project number from an error message, or "".
func projectNumber(msg string) string {
	if m := reProjectNumber.FindStringSubmatch(msg); m != nil {
		return m[1]
	}
	return ""
}

// --- Tab completion ---

// autoComplete is the term.Terminal callback. It acts only on Tab: it completes
// the active token (command verb, remote path, or local path) and, when several
// candidates remain, prints them above the prompt like sftp.
func (s *Shell) autoComplete(line string, pos int, key rune) (string, int, bool) {
	if key != '\t' {
		return "", 0, false
	}
	left := line[:pos]
	newLeft, candidates := s.completeInput(left)
	if len(candidates) > 1 && s.term != nil {
		fmt.Fprintf(s.term, "\r\n%s\r\n", strings.Join(candidates, "  "))
	}
	if newLeft == left {
		return "", 0, false
	}
	return newLeft + line[pos:], len(newLeft), true
}

// completeInput computes the completion for the text left of the cursor. It
// returns the rewritten left text (unchanged if nothing to complete) and, when
// the result is ambiguous, the list of candidate names to display.
func (s *Shell) completeInput(left string) (string, []string) {
	toks, err := tokenize(left)
	if err != nil { // e.g. an unterminated quote — don't guess
		return left, nil
	}
	endsSpace := left == "" || strings.HasSuffix(left, " ") || strings.HasSuffix(left, "\t")
	idx, active := len(toks), ""
	if !endsSpace && len(toks) > 0 {
		idx, active = len(toks)-1, toks[idx-1]
	}

	// Gather candidate names for this position.
	var names []string
	if idx == 0 {
		names = completionVerbs()
	} else {
		dir, _ := splitPath(active)
		switch argKind(toks[0], idx) {
		case "remote":
			names = s.remoteNames(dir)
		case "local":
			names = s.localNames(dir)
		default:
			return left, nil
		}
	}

	base := active
	if idx > 0 {
		_, base = splitPath(active)
	}
	matches := filterByPrefix(names, base)
	if len(matches) == 0 {
		return left, nil
	}

	// Build the completed token: keep the directory prefix, replace the base.
	completedBase := longestCommonPrefix(matches)
	pathPart := completedBase
	if idx > 0 {
		dir, _ := splitPath(active)
		pathPart = dir + completedBase
	}
	rendered := quoteArg(pathPart)
	// A single, fully-resolved file gets a trailing space; a directory (ends in
	// "/") does not, so the user can Tab straight into it.
	if len(matches) == 1 && !strings.HasSuffix(matches[0], "/") {
		rendered += " "
	}

	newLeft := left[:lastTokenStart(left)] + rendered
	if len(matches) > 1 {
		return newLeft, matches
	}
	return newLeft, nil
}

// Complete returns shell-completion candidates for a command line given as the
// already-split words after the program name, where the final word is the one
// being completed (possibly empty). It powers external shell completion (e.g.
// the zsh script) and reuses the same candidate logic as interactive Tab. Each
// returned candidate is the full word value (directory prefix included), with
// folders/drives suffixed by "/". Errors yield no candidates.
func (s *Shell) Complete(words []string) []string {
	if len(words) <= 1 {
		prefix := ""
		if len(words) == 1 {
			prefix = words[0]
		}
		return filterByPrefix(completionVerbs(), prefix)
	}
	verb := words[0]
	argIndex := len(words) - 1
	cur := words[argIndex]
	dir, base := splitPath(cur)
	var names []string
	switch argKind(verb, argIndex) {
	case "remote":
		names = s.remoteNames(dir)
	case "local":
		names = s.localNames(dir)
	default:
		return nil
	}
	matches := filterByPrefix(names, base)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, dir+m)
	}
	return out
}

// completionVerbs returns the command verbs offered for first-token completion.
func completionVerbs() []string {
	names := append(sortedCommandNames(), "quit", "exit", "bye")
	sort.Strings(names)
	return names
}

// argKind reports whether argument argIndex of verb names a remote path, a
// local path, or neither (no completion).
func argKind(verb string, argIndex int) string {
	switch verb {
	case "ls", "cd", "rm", "mkdir":
		if argIndex == 1 {
			return "remote"
		}
	case "get":
		switch argIndex {
		case 1:
			return "remote"
		case 2:
			return "local"
		}
	case "put":
		switch argIndex {
		case 1:
			return "local"
		case 2:
			return "remote"
		}
	case "lcd", "lls":
		if argIndex == 1 {
			return "local"
		}
	}
	return ""
}

// remoteNames lists the entries of remote directory dir (relative to the cwd or
// absolute) as completion candidates, folders suffixed with "/". At the virtual
// root it returns the drive names. Any error yields no candidates.
func (s *Shell) remoteNames(dir string) []string {
	stack, err := s.resolveDir(dir)
	if err != nil {
		return nil
	}
	if len(stack) == 0 {
		drives, err := s.driveList()
		if err != nil {
			return nil
		}
		names := make([]string, 0, len(drives))
		for _, d := range drives {
			names = append(names, d.Name+"/")
		}
		return names
	}
	files, err := s.c.List(s.ctx, currentDriveID(stack), currentID(stack))
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		n := f.Name
		if gdrive.IsFolder(f) {
			n += "/"
		}
		names = append(names, n)
	}
	return names
}

// localNames lists the entries of local directory dir as completion candidates,
// directories suffixed with "/". Any error yields no candidates.
func (s *Shell) localNames(dir string) []string {
	if dir == "" {
		dir = "."
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() {
			n += "/"
		}
		names = append(names, n)
	}
	return names
}

// filterByPrefix returns the names that start with prefix.
func filterByPrefix(names []string, prefix string) []string {
	var out []string
	for _, n := range names {
		if strings.HasPrefix(n, prefix) {
			out = append(out, n)
		}
	}
	return out
}

// longestCommonPrefix returns the longest string that prefixes every name.
func longestCommonPrefix(names []string) string {
	if len(names) == 0 {
		return ""
	}
	p := names[0]
	for _, n := range names[1:] {
		for !strings.HasPrefix(n, p) {
			p = p[:len(p)-1]
			if p == "" {
				return ""
			}
		}
	}
	return p
}

// quoteArg double-quotes s when it contains a space so it round-trips through
// tokenize; otherwise it is returned unchanged.
func quoteArg(s string) string {
	if strings.ContainsAny(s, " \t") {
		return `"` + s + `"`
	}
	return s
}

// lastTokenStart returns the byte index in s where the final token begins, or
// len(s) when s ends at a token boundary (so a new token starts there). It
// honors single and double quotes the same way tokenize does.
func lastTokenStart(s string) int {
	start, inToken := 0, false
	var quote rune
	for i, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			if !inToken {
				start, inToken = i, true
			}
			quote = r
		case r == ' ' || r == '\t':
			inToken = false
		default:
			if !inToken {
				start, inToken = i, true
			}
		}
	}
	if !inToken {
		return len(s)
	}
	return start
}

// dispatch runs one parsed command line; it returns true when the session
// should end.
func (s *Shell) dispatch(args []string) (quit bool) {
	name, rest := args[0], args[1:]
	switch name {
	case "quit", "exit", "bye":
		return true
	}
	cmd, ok := commands[name]
	if !ok {
		fmt.Fprintf(s.out, "%s: unknown command (try 'help')\n", name)
		return false
	}
	if err := cmd.run(s, rest); err != nil {
		fmt.Fprintf(s.out, "%s: %v\n", name, friendlyErr(err))
	}
	return false
}

// --- working-directory helpers ---

// pwd renders the current remote directory as an absolute path.
func (s *Shell) pwd() string {
	if len(s.cwd) == 0 {
		return "/"
	}
	var b strings.Builder
	for _, r := range s.cwd {
		b.WriteByte('/')
		b.WriteString(r.Name)
	}
	return b.String()
}

// currentID returns the Drive ID of the folder at the tip of stack, or "" at
// the virtual root (which is not a real folder).
func currentID(stack []gdrive.Ref) string {
	if len(stack) == 0 {
		return ""
	}
	return stack[len(stack)-1].ID
}

// currentDriveID returns the Shared Drive ID the stack is inside, or "" for the
// virtual root and for My Drive. It is carried on the first path element.
func currentDriveID(stack []gdrive.Ref) string {
	if len(stack) == 0 {
		return ""
	}
	return stack[0].DriveID
}

// driveList returns the virtual-root entries: a synthesized My Drive followed by
// every accessible Shared Drive.
func (s *Shell) driveList() ([]gdrive.Ref, error) {
	shared, err := s.c.ListDrives(s.ctx)
	if err != nil {
		return nil, err
	}
	out := make([]gdrive.Ref, 0, len(shared)+1)
	out = append(out, gdrive.Ref{ID: gdrive.RootID, Name: myDriveName, DriveID: ""})
	for _, d := range shared {
		// A Shared Drive's root folder ID is the drive ID; carry it as DriveID
		// so listings/lookups inside it scope to that drive's corpus.
		out = append(out, gdrive.Ref{ID: d.ID, Name: d.Name, DriveID: d.ID})
	}
	return out, nil
}

// findDrive resolves a top-level drive name (at the virtual root) to its Ref,
// refusing to guess when several drives share a name.
func (s *Shell) findDrive(name string) (gdrive.Ref, error) {
	drives, err := s.driveList()
	if err != nil {
		return gdrive.Ref{}, err
	}
	var match []gdrive.Ref
	for _, d := range drives {
		if d.Name == name {
			match = append(match, d)
		}
	}
	switch len(match) {
	case 0:
		return gdrive.Ref{}, gdrive.ErrNotFound
	case 1:
		return match[0], nil
	default:
		return gdrive.Ref{}, gdrive.ErrAmbiguous
	}
}

// resolveDir resolves a path (absolute or relative, with . and .. segments) to
// a directory stack. It errors if any segment is missing or not a folder.
func (s *Shell) resolveDir(path string) ([]gdrive.Ref, error) {
	stack, segs, err := s.startStack(path)
	if err != nil {
		return nil, err
	}
	for _, seg := range segs {
		switch seg {
		case "", ".":
			continue
		case "..":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		default:
			if len(stack) == 0 {
				// At the virtual root the first segment names a drive.
				ref, err := s.findDrive(seg)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", seg, err)
				}
				stack = append(stack, ref)
				continue
			}
			f, err := s.c.FindDir(s.ctx, currentDriveID(stack), currentID(stack), seg)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", seg, err)
			}
			stack = append(stack, gdrive.Ref{ID: f.Id, Name: f.Name, DriveID: currentDriveID(stack)})
		}
	}
	return stack, nil
}

// idPrefix marks a path segment as a raw Drive ID rather than a name to look
// up, e.g. "id:1A2b3C". It is an opt-in, unambiguous addressing form that never
// collides with a filename and introduces no command mode.
const idPrefix = "id:"

// parseIDArg reports whether seg is an "id:<ID>" reference and returns the bare
// ID. It matches only a single segment: an empty ID, or one containing "/" (so
// the token is really a path), is not treated as an ID. The prefix is literal
// and case-sensitive.
func parseIDArg(seg string) (id string, ok bool) {
	if !strings.HasPrefix(seg, idPrefix) {
		return "", false
	}
	id = seg[len(idPrefix):]
	if id == "" || strings.Contains(id, "/") {
		return "", false
	}
	return id, true
}

// resolveFile resolves a path to a single file or folder. A bare "id:<ID>"
// argument is resolved directly by ID, bypassing name navigation. Otherwise the
// leading directory components must exist; the final component is looked up and
// returned.
func (s *Shell) resolveFile(path string) (*drive.File, error) {
	if id, ok := parseIDArg(path); ok {
		return s.c.GetByID(s.ctx, id)
	}
	dir, base := splitPath(path)
	switch base {
	case "":
		return nil, fmt.Errorf("%s: invalid path", path)
	case ".", "..":
		// These name directories, never files; resolveDir handles them.
		return nil, fmt.Errorf("%q: not a file", base)
	}
	stack := s.cwd
	if dir != "" || strings.HasPrefix(path, "/") {
		var err error
		stack, err = s.resolveDir(dir)
		if err != nil {
			return nil, err
		}
	}
	if len(stack) == 0 {
		// At the virtual root the only entries are drives, which are directories.
		return nil, fmt.Errorf("%s: is a drive, not a file", base)
	}
	f, err := s.c.FindOne(s.ctx, currentDriveID(stack), currentID(stack), base)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", base, err)
	}
	return f, nil
}

// startStack returns the initial directory stack and the path split into
// segments to walk. A leading "id:<ID>" segment seeds the stack from that Drive
// folder — an absolute, name-independent anchor that must resolve to a folder.
// Otherwise an absolute path starts at the virtual root and a relative path
// starts at a copy of the cwd.
func (s *Shell) startStack(path string) ([]gdrive.Ref, []string, error) {
	segs := strings.Split(path, "/")
	if id, ok := parseIDArg(segs[0]); ok {
		f, err := s.c.GetByID(s.ctx, id)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", segs[0], err)
		}
		if !gdrive.IsFolder(f) {
			return nil, nil, fmt.Errorf("%s: not a directory", segs[0])
		}
		// A Shared Drive item carries its DriveID so deeper lookups stay in the
		// right corpus; a My Drive item has an empty DriveID, as the rest of the
		// code expects.
		seed := gdrive.Ref{ID: f.Id, Name: f.Name, DriveID: f.DriveId}
		return []gdrive.Ref{seed}, segs[1:], nil
	}
	if strings.HasPrefix(path, "/") {
		return nil, segs, nil
	}
	stack := make([]gdrive.Ref, len(s.cwd))
	copy(stack, s.cwd)
	return stack, segs, nil
}

// splitPath splits a remote path into its directory part and final element,
// ignoring any trailing slash.
func splitPath(path string) (dir, base string) {
	path = strings.TrimRight(path, "/")
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[:i+1], path[i+1:]
	}
	return "", path
}

// tokenize splits a command line into arguments, honoring single and double
// quotes so names may contain spaces.
func tokenize(line string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inToken := false
	var quote rune // 0, '\'' or '"'
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inToken = true
		case r == ' ' || r == '\t' || r == '\r':
			if inToken {
				args = append(args, cur.String())
				cur.Reset()
				inToken = false
			}
		default:
			cur.WriteRune(r)
			inToken = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote", quote)
	}
	if inToken {
		args = append(args, cur.String())
	}
	return args, nil
}

// sortedCommandNames returns the command verbs in alphabetical order.
func sortedCommandNames() []string {
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

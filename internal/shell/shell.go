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
	ctx context.Context
	c   *gdrive.Client
	cwd []gdrive.Ref // path from the virtual root; empty means the virtual root
	out io.Writer
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

// Run reads commands until EOF (Ctrl-D) or a quit verb. When interactive it
// prints a prompt before each line.
func (s *Shell) Run(interactive bool) error {
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
	out = append(out, shared...)
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
	stack, segs := s.startStack(path)
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

// resolveFile resolves a path to a single file or folder. The leading
// directory components must exist; the final component is looked up and
// returned.
func (s *Shell) resolveFile(path string) (*drive.File, error) {
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

// startStack returns the initial stack (root for absolute paths, a copy of the
// cwd otherwise) and the path split into segments.
func (s *Shell) startStack(path string) ([]gdrive.Ref, []string) {
	if strings.HasPrefix(path, "/") {
		return nil, strings.Split(path, "/")
	}
	stack := make([]gdrive.Ref, len(s.cwd))
	copy(stack, s.cwd)
	return stack, strings.Split(path, "/")
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

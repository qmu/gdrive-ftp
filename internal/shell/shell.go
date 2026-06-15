// Package shell implements the interactive, FTP-like command loop over a
// gdrive.Client: ls, cd, pwd, get, put, mkdir, rm and local-side helpers.
package shell

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"gdrive-ftp/internal/gdrive"

	drive "google.golang.org/api/drive/v3"
)

// Shell holds the session state: the Drive client and the current remote
// working directory, represented as the chain of folders from the root.
type Shell struct {
	ctx context.Context
	c   *gdrive.Client
	cwd []gdrive.Ref // path from root; empty means the root folder
	out io.Writer
}

// New creates a Shell rooted at the Drive root folder.
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
	return cmd.run(s, args[1:])
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
		fmt.Fprintf(s.out, "%s: %v\n", name, err)
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

// currentID returns the Drive ID of the folder at the tip of stack (RootID for
// the empty/root stack).
func currentID(stack []gdrive.Ref) string {
	if len(stack) == 0 {
		return gdrive.RootID
	}
	return stack[len(stack)-1].ID
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
			f, err := s.c.FindDir(s.ctx, "", currentID(stack), seg)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", seg, err)
			}
			stack = append(stack, gdrive.Ref{ID: f.Id, Name: f.Name})
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
	f, err := s.c.FindOne(s.ctx, "", currentID(stack), base)
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

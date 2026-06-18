package shell

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gdrive-ftp/internal/gdrive"

	drive "google.golang.org/api/drive/v3"
)

func init() {
	commands = map[string]command{
		"ls":    {run: (*Shell).cmdLs, usage: "ls [dir]", help: "list a remote directory (default: current)"},
		"cd":    {run: (*Shell).cmdCd, usage: "cd [dir]", help: "change remote directory (no arg: go to root)"},
		"pwd":   {run: (*Shell).cmdPwd, usage: "pwd", help: "print the remote working directory"},
		"find":  {run: (*Shell).cmdFind, usage: "find <pattern> [dir]", help: "search for files/folders by name (substring)"},
		"get":   {run: (*Shell).cmdGet, usage: "get <remote> [local]", help: "download a file (Google docs are exported)"},
		"put":   {run: (*Shell).cmdPut, usage: "put <local> [remote]", help: "upload a local file"},
		"mkdir": {run: (*Shell).cmdMkdir, usage: "mkdir <name>", help: "create a remote folder"},
		"rm":    {run: (*Shell).cmdRm, usage: "rm <name>", help: "move a remote file/folder to the trash"},
		"lcd":   {run: (*Shell).cmdLcd, usage: "lcd [dir]", help: "change the local working directory"},
		"lls":   {run: (*Shell).cmdLls, usage: "lls [dir]", help: "list a local directory"},
		"lpwd":  {run: (*Shell).cmdLpwd, usage: "lpwd", help: "print the local working directory"},
		"help":  {run: (*Shell).cmdHelp, usage: "help [cmd]", help: "show command help"},
		"?":     {run: (*Shell).cmdHelp, usage: "? [cmd]", help: "alias for help"},
	}
}

func (s *Shell) cmdLs(args []string) error {
	stack := s.cwd
	if len(args) > 0 {
		arg := args[0]
		_, base := splitPath(arg)
		// Pure directory references (/, ., .., trailing slash) and top-level
		// drive names list a folder. Anything else may name a single file,
		// which we show as one row.
		if base == "" || base == "." || base == ".." || strings.HasSuffix(arg, "/") || s.singleDriveArg(arg) {
			var err error
			if stack, err = s.resolveDir(arg); err != nil {
				return err
			}
		} else {
			f, err := s.resolveFile(arg)
			if err != nil {
				return err
			}
			if !gdrive.IsFolder(f) {
				return s.emit([]fileEntry{toFileEntry(f)}, func() {
					fmt.Fprintf(s.out, "%12s  %s  %s\n", sizeStr(f), modTime(f.ModifiedTime), f.Name)
				})
			}
			// Re-resolve as a directory so the drive context (DriveID) is
			// threaded into the listing below.
			if stack, err = s.resolveDir(arg); err != nil {
				return err
			}
		}
	}
	// The virtual root lists the available drives, not a folder's children.
	if len(stack) == 0 {
		return s.listDrives()
	}
	files, err := s.c.List(s.ctx, currentDriveID(stack), currentID(stack))
	if err != nil {
		return err
	}
	entries := make([]fileEntry, 0, len(files))
	for _, f := range files {
		entries = append(entries, toFileEntry(f))
	}
	return s.emit(entries, func() {
		for _, f := range files {
			name := f.Name
			if gdrive.IsFolder(f) {
				name += "/"
			}
			fmt.Fprintf(s.out, "%12s  %s  %s\n", sizeStr(f), modTime(f.ModifiedTime), name)
		}
	})
}

// listDrives prints the virtual-root entries (My Drive plus each Shared Drive)
// as folders.
func (s *Shell) listDrives() error {
	drives, err := s.driveList()
	if err != nil {
		return err
	}
	entries := make([]fileEntry, 0, len(drives))
	for _, d := range drives {
		entries = append(entries, fileEntry{Name: d.Name, ID: d.ID, IsFolder: true})
	}
	return s.emit(entries, func() {
		for _, d := range drives {
			fmt.Fprintf(s.out, "%12s  %s  %s\n", "-", modTime(""), d.Name+"/")
		}
	})
}

// singleDriveArg reports whether arg selects a single top-level drive (a
// directory at the virtual root), so ls lists it rather than guessing
// file-vs-folder.
func (s *Shell) singleDriveArg(arg string) bool {
	trimmed := strings.Trim(arg, "/")
	if trimmed == "" || strings.Contains(trimmed, "/") {
		return false
	}
	if strings.HasPrefix(arg, "/") {
		return true // /Drive selects a drive regardless of cwd
	}
	return len(s.cwd) == 0 // a bare name at the virtual root is a drive
}

func (s *Shell) cmdCd(args []string) error {
	target := "/"
	if len(args) > 0 {
		target = args[0]
	}
	stack, err := s.resolveDir(target)
	if err != nil {
		return err
	}
	s.cwd = stack
	return nil
}

func (s *Shell) cmdPwd(args []string) error {
	return s.emit(pwdResult{Path: s.pwd()}, func() {
		fmt.Fprintln(s.out, s.pwd())
	})
}

func (s *Shell) cmdGet(args []string) error {
	if len(args) < 1 {
		return usageErr("get <remote> [local]")
	}
	f, err := s.resolveFile(args[0])
	if err != nil {
		return err
	}
	if gdrive.IsFolder(f) {
		return fmt.Errorf("%s is a directory", f.Name)
	}

	local := ""
	if len(args) >= 2 {
		local = args[1]
	}

	// Choose the on-disk name and the streaming function. Google-native docs
	// have no raw bytes, so they are exported to an Office/PNG format.
	var name string
	var write func(io.Writer) (int64, error)
	exported := gdrive.IsGoogleDoc(f)
	if exported {
		mime, ext, ok := gdrive.ExportFormat(f.MimeType)
		if !ok {
			return fmt.Errorf("%s is a Google %s and has no exportable format", f.Name, shortType(f.MimeType))
		}
		name = f.Name + ext
		write = func(w io.Writer) (int64, error) { return s.c.Export(s.ctx, f.Id, mime, w) }
	} else {
		name = f.Name
		write = func(w io.Writer) (int64, error) {
			n, err := s.c.Download(s.ctx, f.Id, w)
			// f.Size is authoritative for binary files; catch silent truncation.
			if err == nil && f.Size > 0 && n != f.Size {
				return n, fmt.Errorf("short download: got %s of %s", byteCount(n), byteCount(f.Size))
			}
			return n, err
		}
	}

	dest, n, err := saveToFile(resolveLocalDest(local, name), write)
	if err != nil {
		return err
	}
	res := actionResult{Action: "downloaded", Name: f.Name, Dest: dest, Size: n}
	if exported {
		res.Action = "exported"
		res.MimeType = f.MimeType
	}
	return s.emit(res, func() {
		if exported {
			fmt.Fprintf(s.out, "exported %s -> %s (%s, %s)\n", f.Name, dest, shortType(f.MimeType), byteCount(n))
		} else {
			fmt.Fprintf(s.out, "downloaded %s -> %s (%s)\n", f.Name, dest, byteCount(n))
		}
	})
}

// resolveLocalDest decides the local path for a download. An empty local writes
// name into the current directory; a local naming an existing directory (or one
// ending in a path separator) places name inside it; otherwise local is used
// verbatim.
func resolveLocalDest(local, name string) string {
	if local == "" {
		return name
	}
	if strings.HasSuffix(local, "/") || strings.HasSuffix(local, string(os.PathSeparator)) {
		return filepath.Join(local, name)
	}
	if info, err := os.Stat(local); err == nil && info.IsDir() {
		return filepath.Join(local, name)
	}
	return local
}

// saveToFile streams write's output to dest atomically: it writes to a temp
// file in the destination directory and renames it into place only after the
// transfer and close both succeed. An interrupted or failed transfer therefore
// never truncates or overwrites an existing good file.
func saveToFile(dest string, write func(io.Writer) (int64, error)) (string, int64, error) {
	tmp, err := os.CreateTemp(filepath.Dir(dest), "."+filepath.Base(dest)+".part-*")
	if err != nil {
		return "", 0, err
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()

	n, err := write(tmp)
	if err != nil {
		return "", 0, err
	}
	if err := tmp.Close(); err != nil {
		return "", 0, err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return "", 0, err
	}
	committed = true
	return dest, n, nil
}

func (s *Shell) cmdPut(args []string) error {
	if len(args) < 1 {
		return usageErr("put <local> [remote]")
	}
	local := args[0]
	in, err := os.Open(local)
	if err != nil {
		return err
	}
	defer in.Close()
	if st, err := in.Stat(); err != nil {
		return err
	} else if st.IsDir() {
		return fmt.Errorf("%s is a directory (recursive upload is not supported)", local)
	}

	name := filepath.Base(local)
	parent := s.cwd
	if len(args) >= 2 {
		dest := args[1]
		// A bare id: target always names the destination folder; it is never a
		// rename target, so surface a resolution failure instead of treating the
		// token as a filename.
		if _, ok := parseIDArg(dest); ok {
			stack, derr := s.resolveDir(dest)
			if derr != nil {
				return derr
			}
			parent = stack
		} else if stack, derr := s.resolveDir(dest); derr == nil {
			// If the whole destination resolves to an existing folder, upload INTO
			// it under the local basename (FTP-style, symmetric with get; a trailing
			// slash resolves the same way). Only when it does not name an existing
			// folder is the final component treated as the target filename (rename).
			parent = stack
		} else {
			dir, base := splitPath(dest)
			if dir != "" || strings.HasPrefix(dest, "/") {
				if parent, err = s.resolveDir(dir); err != nil {
					return err
				}
			}
			if base != "" {
				name = base
			}
		}
	}

	if len(parent) == 0 {
		return fmt.Errorf("cannot upload to the virtual root; cd into a drive first")
	}
	f, err := s.c.Upload(s.ctx, currentDriveID(parent), currentID(parent), name, in)
	if err != nil {
		return err
	}
	return s.emit(actionResult{Action: "uploaded", Name: name, ID: f.Id, Size: f.Size}, func() {
		fmt.Fprintf(s.out, "uploaded %s -> %s (%s)\n", local, name, byteCount(f.Size))
	})
}

func (s *Shell) cmdMkdir(args []string) error {
	if len(args) < 1 {
		return usageErr("mkdir <name>")
	}
	if _, ok := parseIDArg(args[0]); ok {
		// A bare id: target names a parent folder, not the new folder's name.
		return fmt.Errorf("%s: an id: target names a parent folder; append /<name>", args[0])
	}
	dir, base := splitPath(args[0])
	parent := s.cwd
	var err error
	if dir != "" || strings.HasPrefix(args[0], "/") {
		if parent, err = s.resolveDir(dir); err != nil {
			return err
		}
	}
	if base == "" {
		return fmt.Errorf("%s: invalid name", args[0])
	}
	if len(parent) == 0 {
		return fmt.Errorf("cannot create a folder at the virtual root; cd into a drive first")
	}
	f, err := s.c.Mkdir(s.ctx, currentID(parent), base)
	if err != nil {
		return err
	}
	return s.emit(actionResult{Action: "created", Name: f.Name, ID: f.Id}, func() {
		fmt.Fprintf(s.out, "created %s/ (%s)\n", f.Name, f.Id)
	})
}

func (s *Shell) cmdRm(args []string) error {
	if len(args) < 1 {
		return usageErr("rm <name>")
	}
	f, err := s.resolveFile(args[0])
	if err != nil {
		return err
	}
	if err := s.c.Trash(s.ctx, f.Id); err != nil {
		return err
	}
	return s.emit(actionResult{Action: "trashed", Name: f.Name, ID: f.Id}, func() {
		fmt.Fprintf(s.out, "trashed %s\n", f.Name)
	})
}

// cmdFind searches for files/folders whose name contains <pattern> (a
// case-insensitive substring) using Drive's native `name contains` query,
// printing each match's full path. The search is scoped to the current drive
// (My Drive's corpus, or the Shared Drive of the cwd); an optional [dir] anchor
// (a path or id:) retargets it, and when the anchor is a folder the results are
// narrowed to that folder's subtree. find never mutates and presents all
// matches — act on one afterward via its id:.
func (s *Shell) cmdFind(args []string) error {
	if len(args) < 1 {
		return usageErr("find <pattern> [dir]")
	}
	pattern := args[0]

	// Resolve the optional anchor to a directory stack; default to the cwd.
	stack := s.cwd
	if len(args) >= 2 {
		var err error
		if stack, err = s.resolveDir(args[1]); err != nil {
			return err
		}
	}
	driveID := currentDriveID(stack)

	files, err := s.c.Search(s.ctx, driveID, pattern)
	if err != nil {
		return err
	}

	cache := map[string]*drive.File{}

	// driveName labels rendered paths; stopID is the corpus root the parent-walk
	// halts at (the Shared Drive root == driveID, or the My Drive root folder).
	driveName := "My Drive"
	stopID := driveID
	if driveID != "" {
		if d, derr := s.findGet(driveID, cache); derr == nil {
			driveName = d.Name
		} else if len(stack) >= 1 {
			driveName = stack[0].Name
		}
	} else if r, rerr := s.c.GetByID(s.ctx, gdrive.RootID); rerr == nil {
		stopID = r.Id
	}

	// Narrow to a subtree only when an explicit anchor names a real subfolder
	// (not a whole-drive root or the virtual root).
	narrowTo := ""
	if len(args) >= 2 {
		if tip := currentID(stack); tip != "" && tip != gdrive.RootID && tip != driveID {
			narrowTo = tip
		}
	}

	entries := []fileEntry{}
	for _, f := range files {
		// Drive's `contains` is normalization-flavored; re-filter for a true
		// case-insensitive substring.
		if !nameContains(f.Name, pattern) {
			continue
		}
		path, ancestors := s.findPath(f, driveName, stopID, cache)
		if narrowTo != "" && !ancestors[narrowTo] {
			continue
		}
		e := toFileEntry(f)
		e.Path = path
		entries = append(entries, e)
	}

	return s.emit(entries, func() {
		for _, e := range entries {
			line := e.Path
			if e.IsFolder {
				line += "/"
			}
			fmt.Fprintln(s.out, line)
		}
	})
}

// findGet is GetByID with a per-find cache to avoid re-fetching the same folder.
func (s *Shell) findGet(id string, cache map[string]*drive.File) (*drive.File, error) {
	if f, ok := cache[id]; ok {
		return f, nil
	}
	f, err := s.c.GetByID(s.ctx, id)
	if err != nil {
		return nil, err
	}
	cache[id] = f
	return f, nil
}

// findPath builds f's absolute path by walking its parent chain up to the corpus
// root (stopID), prefixed with driveName, and returns the path plus the set of
// ancestor folder IDs (used for subtree narrowing). Unresolvable or cyclic
// ancestry yields a best-effort path from the portion that resolved. The depth
// cap is a backstop against pathological parent chains.
func (s *Shell) findPath(f *drive.File, driveName, stopID string, cache map[string]*drive.File) (string, map[string]bool) {
	names := []string{f.Name}
	ancestors := map[string]bool{}
	cur := f
	for depth := 0; depth < 64; depth++ {
		if len(cur.Parents) == 0 {
			break
		}
		pid := cur.Parents[0]
		if pid == stopID || ancestors[pid] {
			break
		}
		parent, err := s.findGet(pid, cache)
		if err != nil {
			break // ancestry not fully resolvable; stop with a partial path
		}
		ancestors[pid] = true
		// A parentless ancestor is the corpus root; driveName already covers it.
		if len(parent.Parents) == 0 {
			break
		}
		names = append([]string{parent.Name}, names...)
		cur = parent
	}
	var b strings.Builder
	b.WriteByte('/')
	b.WriteString(driveName)
	for _, n := range names {
		b.WriteByte('/')
		b.WriteString(n)
	}
	return b.String(), ancestors
}

// nameContains reports whether name contains pattern as a case-insensitive
// substring — the exact semantics find promises atop Drive's looser query.
func nameContains(name, pattern string) bool {
	return strings.Contains(strings.ToLower(name), strings.ToLower(pattern))
}

func (s *Shell) cmdLcd(args []string) error {
	dir := ""
	if len(args) > 0 {
		dir = args[0]
	}
	if dir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dir = home
		} else {
			return err
		}
	}
	if err := os.Chdir(dir); err != nil {
		return err
	}
	return s.cmdLpwd(nil)
}

func (s *Shell) cmdLls(args []string) error {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		size := "-"
		if info, err := e.Info(); err == nil && !e.IsDir() {
			size = byteCount(info.Size())
		}
		if e.IsDir() {
			name += "/"
		}
		fmt.Fprintf(s.out, "%12s  %s\n", size, name)
	}
	return nil
}

func (s *Shell) cmdLpwd(args []string) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	fmt.Fprintln(s.out, wd)
	return nil
}

func (s *Shell) cmdHelp(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "quit", "exit", "bye":
			fmt.Fprintln(s.out, "quit | exit | bye      end the session")
			return nil
		}
		if c, ok := commands[args[0]]; ok {
			fmt.Fprintf(s.out, "%-22s %s\n", c.usage, c.help)
			return nil
		}
		return fmt.Errorf("no such command %q", args[0])
	}
	fmt.Fprintln(s.out, "Commands:")
	for _, name := range sortedCommandNames() {
		c := commands[name]
		fmt.Fprintf(s.out, "  %-22s %s\n", c.usage, c.help)
	}
	fmt.Fprintln(s.out, "  quit | exit | bye      end the session")
	fmt.Fprintln(s.out, "Any remote path may be given as id:<DriveID> to target a file/folder")
	fmt.Fprintln(s.out, "directly by its Google Drive ID, e.g. get id:1A2b3C, put f.txt id:0Bx,")
	fmt.Fprintln(s.out, "rm id:1A2b3C, ls id:0Bx, cd id:0Bx, mkdir id:0Bx/NewFolder.")
	return nil
}

// --- formatting helpers ---

func usageErr(usage string) error { return fmt.Errorf("usage: %s", usage) }

func sizeStr(f *drive.File) string {
	if gdrive.IsFolder(f) {
		return "-"
	}
	if gdrive.IsGoogleDoc(f) {
		return "gdoc"
	}
	return byteCount(f.Size)
}

func modTime(rfc3339 string) string {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return strings.Repeat(" ", len("2006-01-02 15:04"))
	}
	return t.Local().Format("2006-01-02 15:04")
}

// byteCount renders n as a human-readable size.
func byteCount(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + "B"
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// shortType turns a Google MIME type into a friendly label (e.g. "document").
func shortType(mime string) string {
	if i := strings.LastIndex(mime, "."); i >= 0 {
		return mime[i+1:]
	}
	return mime
}

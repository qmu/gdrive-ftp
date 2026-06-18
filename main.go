// Command gdrive-ftp is a small FTP-style client for Google Drive. It opens an
// interactive shell supporting ls, cd, pwd, get (download), put (upload),
// mkdir and rm, or runs a single command passed on the command line.
//
//	gdrive-ftp                 # interactive shell
//	gdrive-ftp auth            # run the OAuth consent flow and exit
//	gdrive-ftp ls /            # one-shot: list the root folder
//	gdrive-ftp get report.pdf  # one-shot: download a file
//
// On first run (and on "auth") it performs the OAuth consent flow using an
// OAuth "Desktop app" client_credentials.json (see -creds) and caches the token
// under -token.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"gdrive-ftp/internal/audit"
	"gdrive-ftp/internal/auth"
	"gdrive-ftp/internal/gdrive"
	"gdrive-ftp/internal/shell"

	"golang.org/x/term"
)

func main() {
	creds := flag.String("creds", defaultCredsPath(), "path to OAuth client credentials.json")
	token := flag.String("token", defaultTokenPath(), "path to the cached auth token")
	jsonOut := flag.Bool("json", false, "emit machine-readable JSON output")
	noLog := flag.Bool("no-log", false, "disable the audit log of Drive mutations")
	flag.Usage = usage
	flag.Parse()
	args := flag.Args()

	// "completion zsh" prints a zsh completion script; it needs no Drive auth.
	if len(args) >= 1 && args[0] == "completion" {
		if len(args) == 2 && args[1] == "zsh" {
			fmt.Print(zshCompletion)
			return
		}
		fatal(fmt.Errorf("usage: %s completion zsh", filepath.Base(os.Args[0])))
	}

	// "log" opens the read-only audit-log browser. Like completion, it reads only
	// the local log file and needs no Drive auth, so it branches before auth.Client.
	if len(args) >= 1 && args[0] == "log" {
		runLog(*jsonOut)
		return
	}

	// Cancel in-flight Drive calls cleanly on Ctrl-C.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// "__complete" is the hidden helper the zsh script calls on Tab. It must
	// never trigger the interactive OAuth flow (that would hang the shell), so it
	// only runs with an already-cached token and stays silent otherwise.
	if len(args) >= 1 && args[0] == "__complete" {
		completeForShell(ctx, *creds, *token, args[1:])
		return
	}

	hc, err := auth.Client(ctx, *creds, *token)
	if err != nil {
		fatal(err)
	}

	// "auth" is a standalone subcommand: running the OAuth flow (done above) and
	// caching the token is all it does, so report success and exit.
	if args := flag.Args(); len(args) == 1 && args[0] == "auth" {
		fmt.Printf("Authorized. Token cached at %s\n", *token)
		return
	}

	client, err := gdrive.New(ctx, hc)
	if err != nil {
		fatal(err)
	}
	var auditLog *audit.Logger
	if !*noLog {
		auditLog = audit.New(defaultLogPath())
	}
	sh := shell.New(ctx, client, os.Stdout, *jsonOut, auditLog)

	// One-shot mode: any positional args form a single command.
	if args := flag.Args(); len(args) > 0 {
		if err := sh.Execute(args); err != nil {
			if *jsonOut {
				// JSON mode: error envelope on stderr, still exit non-zero.
				shell.EncodeErrorJSON(os.Stderr, err)
				os.Exit(1)
			}
			fatal(err)
		}
		return
	}

	fmt.Println("Connected to Google Drive. Type 'help' for commands, 'quit' to exit.")
	if err := sh.Run(true); err != nil {
		fatal(err)
	}
}

// runLog reads the audit log and presents it: as a JSON array under -json, as
// plain rows when stdout is not a terminal (the agent/pipe path), and otherwise
// as the interactive tig-like browser. It reads the local log only — no auth.
func runLog(jsonOut bool) {
	entries, err := audit.Read(defaultLogPath())
	if err != nil {
		fatal(err)
	}
	switch {
	case jsonOut:
		if err := audit.WriteJSON(os.Stdout, entries); err != nil {
			fatal(err)
		}
	case !term.IsTerminal(int(os.Stdout.Fd())):
		audit.WriteText(os.Stdout, entries)
	case len(entries) == 0:
		fmt.Println("No Drive operations have been logged yet.")
	default:
		if err := audit.Browse(entries); err != nil {
			fatal(err)
		}
	}
}

// completeForShell prints shell-completion candidates (one per line) for the
// given command words. It is invoked by the zsh completion script on Tab and
// must stay silent on any error so a Tab press never spews output.
func completeForShell(ctx context.Context, creds, token string, words []string) {
	// Bail before auth.Client so a Tab press never launches the OAuth flow.
	if _, err := os.Stat(token); err != nil {
		return
	}
	hc, err := auth.Client(ctx, creds, token)
	if err != nil {
		return
	}
	client, err := gdrive.New(ctx, hc)
	if err != nil {
		return
	}
	sh := shell.New(ctx, client, os.Stdout, false, nil) // completion: never JSON, never logs
	for _, c := range sh.Complete(words) {
		fmt.Println(c)
	}
}

// zshCompletion is the script emitted by "gdrive-ftp completion zsh". Enable it
// with:  source <(gdrive-ftp completion zsh)   (after compinit), or save it to a
// file named _gdrive-ftp on your $fpath.
const zshCompletion = `#compdef gdrive-ftp
# zsh completion for gdrive-ftp. Completes command verbs, remote Drive paths
# (queried live), and local paths. Enable with:
#   source <(gdrive-ftp completion zsh)
_gdrive_ftp() {
  local -a cands
  cands=( ${(f)"$(gdrive-ftp __complete "${(@)words[2,CURRENT]}" 2>/dev/null)"} )
  compadd -- $cands
}
compdef _gdrive_ftp gdrive-ftp
`

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: %s [flags] [command args...]\n\n", filepath.Base(os.Args[0]))
	fmt.Fprintln(os.Stderr, "Flags:")
	flag.PrintDefaults()
	fmt.Fprintln(os.Stderr, "\nWith no command, an interactive FTP-like shell is started.")
	fmt.Fprintln(os.Stderr, "Use 'auth' to run the OAuth consent flow and exit.")
	fmt.Fprintln(os.Stderr, "Use 'log' to browse the audit log of Drive changes (j/k to move, q to quit).")
	fmt.Fprintln(os.Stderr, "Use 'completion zsh' to print a zsh completion script (see README).")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "gdrive-ftp:", err)
	os.Exit(1)
}

// configDir returns ~/.config/gdrive-ftp (or the OS-appropriate equivalent).
func configDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ".gdrive-ftp"
	}
	return filepath.Join(dir, "gdrive-ftp")
}

// defaultCredsPath prefers ./credentials.json when present, else the config dir.
func defaultCredsPath() string {
	if _, err := os.Stat("credentials.json"); err == nil {
		return "credentials.json"
	}
	return filepath.Join(configDir(), "credentials.json")
}

func defaultTokenPath() string {
	return filepath.Join(configDir(), "token.json")
}

// defaultLogPath is the append-only audit log of Drive mutations, kept beside
// the token under the config dir.
func defaultLogPath() string {
	return filepath.Join(configDir(), "audit.jsonl")
}

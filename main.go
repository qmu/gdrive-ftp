// Command gdrive-ftp is a small FTP-style client for Google Drive. It opens an
// interactive shell supporting ls, cd, pwd, get (download), put (upload),
// mkdir and rm, or runs a single command passed on the command line.
//
//	gdrive-ftp                 # interactive shell
//	gdrive-ftp ls /            # one-shot: list the root folder
//	gdrive-ftp get report.pdf  # one-shot: download a file
//
// On first run it performs the OAuth consent flow using an OAuth "Desktop app"
// client_credentials.json (see -creds) and caches the token under -token.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"gdrive-ftp/internal/auth"
	"gdrive-ftp/internal/gdrive"
	"gdrive-ftp/internal/shell"
)

func main() {
	creds := flag.String("creds", defaultCredsPath(), "path to OAuth client credentials.json")
	token := flag.String("token", defaultTokenPath(), "path to the cached auth token")
	flag.Usage = usage
	flag.Parse()

	// Cancel in-flight Drive calls cleanly on Ctrl-C.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	hc, err := auth.Client(ctx, *creds, *token)
	if err != nil {
		fatal(err)
	}
	client, err := gdrive.New(ctx, hc)
	if err != nil {
		fatal(err)
	}
	sh := shell.New(ctx, client, os.Stdout)

	// One-shot mode: any positional args form a single command.
	if args := flag.Args(); len(args) > 0 {
		if err := sh.Execute(args); err != nil {
			fatal(err)
		}
		return
	}

	fmt.Println("Connected to Google Drive. Type 'help' for commands, 'quit' to exit.")
	if err := sh.Run(true); err != nil {
		fatal(err)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: %s [flags] [command args...]\n\n", filepath.Base(os.Args[0]))
	fmt.Fprintln(os.Stderr, "Flags:")
	flag.PrintDefaults()
	fmt.Fprintln(os.Stderr, "\nWith no command, an interactive FTP-like shell is started.")
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

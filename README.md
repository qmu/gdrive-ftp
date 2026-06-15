# gdrive-ftp

A tiny **FTP-style client for Google Drive**, written in Go. It gives you a
familiar interactive shell — `ls`, `cd`, `pwd`, `get` (download), `put`
(upload), `mkdir`, `rm` — that talks to your Drive over the official Drive v3
API.

The shell opens at a **virtual root** that lists *My Drive* alongside every
Shared Drive you can access; the drive name is the first component of every path.

```
$ gdrive-ftp
Connected to Google Drive. Type 'help' for commands, 'quit' to exit.
gdrive:/> ls
           -                    My Drive/
           -                    Engineering Team/
gdrive:/> cd "My Drive"
gdrive:/My Drive> ls
           -  2026-05-01 09:12  Photos/
           -  2026-04-22 18:30  Work/
       1.4MB  2026-06-10 11:02  budget.xlsx
        gdoc  2026-06-12 14:55  notes
gdrive:/My Drive> cd Work
gdrive:/My Drive/Work> put ./report.pdf
uploaded ./report.pdf -> report.pdf (820.4KB)
gdrive:/My Drive/Work> get budget.xlsx
downloaded budget.xlsx -> budget.xlsx (1.4MB)
gdrive:/My Drive/Work> cd "/Engineering Team/specs"
gdrive:/Engineering Team/specs> quit
```

## Build

Requires Go 1.24+.

```sh
go build -o gdrive-ftp .
```

## One-time Google setup

The app talks to Drive on *your* behalf, so it needs an OAuth client that you
own.

1. Go to the [Google Cloud Console](https://console.cloud.google.com/) and
   create (or pick) a project.
2. **APIs & Services → Library →** enable the **Google Drive API**.
3. **APIs & Services → OAuth consent screen →** configure it (User type
   *External* is fine for personal use) and add your Google account under
   **Test users**.
4. **APIs & Services → Credentials → Create Credentials → OAuth client ID →**
   choose application type **Desktop app**. Download the JSON.
5. Save that file as `credentials.json` next to the binary, or at
   `~/.config/gdrive-ftp/credentials.json`, or pass it with `-creds`.

On first run the app opens your browser for consent, then caches the resulting
token at `~/.config/gdrive-ftp/token.json` so you only authorize once. The
token is refreshed automatically on later runs.

> **Keep `credentials.json` and `token.json` private** — they grant access to
> your Drive. They are git-ignored by default.

## Usage

```
gdrive-ftp [flags] [command args...]
```

With no command it starts the interactive shell. With a command it runs that
single command and exits (handy for scripts):

```sh
gdrive-ftp ls /
gdrive-ftp get /Work/report.pdf ./report.pdf
gdrive-ftp put ./photo.jpg /Photos/photo.jpg
```

### Flags

| Flag      | Default                                | Meaning                                            |
|-----------|----------------------------------------|----------------------------------------------------|
| `-creds`  | `./credentials.json` or config dir     | OAuth client `credentials.json`                    |
| `-token`  | `~/.config/gdrive-ftp/token.json`      | Where to cache the auth token                      |
| `-manual` | off                                    | Paste the auth code on the terminal (headless host) |

On a machine with no local browser (e.g. SSH), use `-manual`: open the printed
URL anywhere, approve, then copy the `code=` value from the page your browser
is redirected to and paste it back.

## Commands

| Command                | Description                                              |
|------------------------|---------------------------------------------------------|
| `ls [dir]`             | List a remote directory (default: current).             |
| `cd [dir]`             | Change remote directory. No argument (or `/`) goes to the virtual root listing all drives. |
| `pwd`                  | Print the remote working directory.                     |
| `get <remote> [local]` | Download a file. Google-native docs are exported (Docs→docx, Sheets→xlsx, Slides→pptx, Drawings→png). |
| `put <local> [remote]` | Upload a local file. Re-uploading the same name replaces the file's content. |
| `mkdir <name>`         | Create a remote folder.                                 |
| `rm <name>`            | Move a remote file/folder to the **trash** (reversible).|
| `lcd [dir]`            | Change the *local* working directory.                   |
| `lls [dir]`            | List a *local* directory.                               |
| `lpwd`                 | Print the *local* working directory.                    |
| `help [cmd]`           | Show command help.                                      |
| `quit` / `exit` / `bye`| End the session.                                        |

Paths may be absolute (`/My Drive/Work/docs`) or relative (`../Photos`), and
`.`/`..` work as expected; the first path component selects a drive (`My Drive`
or a Shared Drive). Names containing spaces can be quoted: `cd "My Drive"`,
`get "my file.pdf"`.

## Notes & limitations

- **Scope:** the app requests full Drive access
  (`https://www.googleapis.com/auth/drive`) so all of the commands above work.
- **`rm` trashes, it does not permanently delete** — files land in Drive's
  trash and can be restored from the web UI.
- **Google-native files** (Docs/Sheets/Slides) have no raw bytes, so `get`
  *exports* them to an Office/PNG format and appends the matching extension.
- **Names are matched exactly and case-sensitively.** Drive allows several
  items with the same name in one folder; when a name is ambiguous the client
  refuses the operation (`ambiguous name`) rather than guess a target — so `rm`
  and `put` never act on the wrong file. `cd` requires the name to resolve to a
  single folder.
- **`get` is atomic**: it downloads to a temp file and renames it into place
  only on full success, so an interrupted transfer never corrupts an existing
  local copy. Passing an existing directory (or a trailing `/`) as the
  destination drops the file inside it under its remote name.
- Directory upload/download is not supported (single files only), matching the
  minimal FTP feature set.
- **Drives:** the session starts at a virtual root listing **My Drive** and every
  Shared Drive you can access; `cd` into one to work inside it. The virtual root
  itself holds no files, so `get`/`put`/`mkdir`/`rm` there report "cd into a
  drive first". `ls` of the virtual root only lists drives you are a member of —
  a folder merely shared with you appears inside the owning drive, not as a
  top-level entry.

## Project layout

```
main.go                     CLI wiring, flags, interactive vs one-shot
internal/auth/auth.go       OAuth2 consent flow + token caching/refresh
internal/gdrive/client.go   Drive v3 wrapper (list/find/upload/download/export/trash)
internal/shell/shell.go     REPL, path resolution, tokenizer
internal/shell/commands.go  Command implementations
```

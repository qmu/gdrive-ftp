# gdrive-ftp

A tiny **FTP-style client for Google Drive**, written in Go. It gives you a
familiar interactive shell — `ls`, `cd`, `pwd`, `get` (download), `put`
(upload), `mkdir`, `rm` — that talks to your Drive over the official Drive v3
API.

> [!WARNING]
> This requests **full Drive access** and can upload, overwrite (`put` replaces
> a same-named file's content), and trash files across My Drive and your Shared
> Drives. `rm` trashes (reversible); nothing is hard-deleted. Keep
> `credentials.json` / `token.json` private — they grant access to your Drive.

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

On first run the app walks you through OAuth consent over the terminal (works
the same locally or over SSH — see [Authorizing](#authorizing)), then caches the
resulting token at `~/.config/gdrive-ftp/token.json` so you only authorize once.
The token is refreshed automatically on later runs.

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
| `-json`   | `false`                                | Emit machine-readable JSON instead of text         |

### Authorizing

The first run authorizes over the terminal — no local browser or callback server
needed, so it works the same on your laptop or a headless/SSH host. The consent
URL is printed; press **`c`** then Enter to copy it to your **local** clipboard
(sent via the OSC 52 terminal escape, so it works through SSH if your terminal
supports it), press **`o`** to try opening a browser on this host, or copy it
manually. Open it in your browser, approve, and the browser is redirected to a
`http://127.0.0.1` URL that fails to load — paste that **entire** URL back at the
prompt (pasting just the `code=` value also works). The `state` is verified to
guard against CSRF.

## Commands

| Command                | Description                                              |
|------------------------|---------------------------------------------------------|
| `ls [dir]`             | List a remote directory (default: current).             |
| `cd [dir]`             | Change remote directory. No argument (or `/`) goes to the virtual root listing all drives. |
| `pwd`                  | Print the remote working directory.                     |
| `get <remote> [local]` | Download a file. Google-native docs are exported (Docs→docx, Sheets→xlsx, Slides→pptx, Drawings→png). |
| `put <local> [remote]` | Upload a local file. If `remote` is an existing folder, the file is uploaded **into** it under its local name; otherwise `remote`'s final component is the target filename. Re-uploading the same name replaces that file's content. |
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

**Addressing by Drive ID.** Anywhere a remote path is expected, an `id:<DriveID>`
token targets a file or folder **directly by its Google Drive ID**, skipping name
navigation. It is opt-in and unambiguous, so it never collides with a filename
and needs no flag or mode — handy for scripts, and the only way to act on an item
whose name is ambiguous or otherwise unreachable by path:

```
get   id:1A2b3CdEfGh ./report.pdf   # download a file by ID
put   ./report.pdf id:0BxParent     # upload INTO a folder by ID
rm    id:1A2b3CdEfGh                # trash a file/folder by ID
ls    id:0BxParent                  # list a folder by ID
cd    id:0BxParent                  # cd into a folder by ID
mkdir id:0BxParent/NewFolder        # create under a parent folder by ID
get   id:0BxParent/report.pdf       # an id: folder can anchor a longer path
```

A bare `id:` used where a folder is required (`cd`, `ls`, `put` target, the parent
of `mkdir`) must resolve to a folder, else the command fails with `not a directory`.
`mkdir id:<parent>` alone is rejected — append `/<name>` for the new folder.

**Tab completion** (like `sftp`): in the interactive shell, press **Tab** to
complete command names, remote paths (folders and files fetched live from
Drive — at the top level it completes drive names), and local paths for `lcd`/
`lls`/`put`. When several entries match, they're listed above the prompt. (Only
in an interactive terminal; piped/one-shot input is unaffected.)

**zsh completion at your shell prompt** — to complete `gdrive-ftp ls <Tab>`
directly in zsh (not inside the interactive shell), enable the bundled script.
Add this to your `~/.zshrc` (after `compinit`):

```zsh
source <(gdrive-ftp completion zsh)
```

Then `gdrive-ftp ls <Tab>`, `gdrive-ftp cd qmu-<Tab>`, etc. complete remote
Drive paths (and `gdrive-ftp put ./file <Tab>` completes the remote target).
It uses your cached token and stays silent if you haven't authorized yet
(run `gdrive-ftp auth` first). Each Tab makes a live Drive call, so expect a
brief pause on large folders.

## JSON output

Pass the global `-json` flag to switch every command from human-formatted text to
compact, machine-readable JSON — handy for scripts and AI agents. The contract:

- **Results go to stdout** as a single JSON value: `ls` emits an **array** of file
  objects; `get`/`put`/`mkdir`/`rm` emit a single result **object**; `pwd` emits
  `{"path":"…"}`. Output is one line, newline-terminated.
- **Errors go to stderr** as `{"error":"…"}` and the process still exits non-zero.
- Keys use stable, domain names: `name`, `id`, `mimeType`, `isFolder`, `size`
  (omitted for folders and Google-native docs), `modifiedTime` (RFC 3339). Action
  objects carry `action` (`downloaded`/`exported`/`uploaded`/`created`/`trashed`),
  plus `dest`/`size`/`id` as relevant.

```sh
$ gdrive-ftp -json ls "/My Drive/Work"
[{"name":"report.pdf","id":"1A2b","mimeType":"application/pdf","isFolder":false,"size":840000,"modifiedTime":"2026-06-10T11:02:00Z"}]

$ gdrive-ftp -json put ./report.pdf id:0BxParentFolder
{"action":"uploaded","name":"report.pdf","id":"1A2b","size":840000}

$ gdrive-ftp -json get /nope
{"error":"no such file or directory"}      # → stderr, exit 1
```

The local-only interactive helpers (`lls`/`lpwd`/`lcd`) and `help` are unaffected
by `-json`; so is Tab/zsh completion.

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
- **Transfers are byte-for-byte binary.** `put` streams your file straight to
  Drive's media-upload endpoint and `get` writes the raw response to disk — no
  base64, no JSON envelope, no conversion. PDFs, Office files (DOCX/XLSX/PPTX),
  ZIPs, images, and arbitrary binaries upload and download identically (verified
  by SHA-256 round-trip), at any size — unlike API clients that wrap content as
  text/base64 and choke on binary or large files. (Uploaded Office files stay
  binary; only Google-*native* Docs/Sheets/Slides are exported on `get`.)
- **`get` is atomic**: it downloads to a temp file and renames it into place
  only on full success, so an interrupted transfer never corrupts an existing
  local copy. A binary download is also length-checked against Drive's reported
  size, so a truncated transfer fails loudly instead of silently. Passing an
  existing directory (or a trailing `/`) as the destination drops the file
  inside it under its remote name.
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
main.go                                   CLI wiring, flags, interactive vs one-shot
internal/auth/auth.go                     OAuth2 consent flow + token caching/refresh
internal/gdrive/client.go                 Drive v3 wrapper (list/find/upload/download/export/trash)
internal/shell/shell.go                   REPL, path resolution, tokenizer, completion
internal/shell/commands.go                Command implementations
plugins/gdrive-ftp/skills/gdrive-ftp/     The agent skill (how to drive this CLI)
.claude-plugin/marketplace.json           Claude Code plugin marketplace
.agents/plugins/marketplace.json          Codex plugin marketplace
```

## Agent skill / plugin

This repo ships a skill that teaches a coding agent how to drive the CLI for
Google Drive (one-shot commands, the drive/path model, auth prerequisite, and
gotchas). It installs as a plugin on Claude Code and OpenAI Codex, or via the
cross-agent skills CLI:

| Agent | Install |
| ----- | ------- |
| **Claude Code** | `/plugin marketplace add qmu/gdrive-ftp`, then enable the `gdrive-ftp` plugin |
| **OpenAI Codex** | `codex plugin marketplace add qmu/gdrive-ftp --ref main`<br>`codex plugin add gdrive-ftp@gdrive-ftp` |
| **Cursor / OpenCode / others** | `npx skills add qmu/gdrive-ftp` |

> The plugin ships the **skill**; the `gdrive-ftp` **binary** must be built or
> installed separately (see [Build](#build)) and on your `PATH`. Authorize once
> with `gdrive-ftp auth` before agent use.

A Claude session working with this repo as its cwd also auto-discovers the skill
(via `.claude/skills/gdrive-ftp`, a symlink into `plugins/`); no install needed.

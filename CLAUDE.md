# CLAUDE.md — ionix-file-manager

## Project Overview

**ionix-file-manager** (`ifm`) is a keyboard-driven terminal file manager written in Go. It uses the Bubbletea TUI framework (Elm-inspired architecture) with Kitty graphics protocol support for image previews. Features dual-pane navigation, search, macros, audio playback, archive extraction, and multi-selection.

## Tech Stack

- **Language:** Go 1.26.2
- **TUI Framework:** charmbracelet/bubbletea v1.3.10
- **UI Components:** charmbracelet/bubbles v1.0.0
- **Styling:** charmbracelet/lipgloss v1.1.0
- **License:** MIT

## Build & Run

```bash
make build        # Compile to ./build/ifm
make install      # Install to $PREFIX/bin/ifm (default: /usr/local)
make uninstall    # Remove installed binary
make clean        # Remove ./build directory
```

There are no test commands — the project does not currently have tests.

## Project Structure

```
main.go                         # Entry point, CLI arg handling
internal/
  clipboard/clipboard.go        # System clipboard (wl-copy → xclip → OSC 52 fallback)
  config/config.go              # Config & macro loading/saving (JSON)
  fs/entry.go                   # Directory listing, file type detection
  fs/ops.go                     # File ops: copy, move, delete, mkdir, brace expansion
  git/status.go                 # Git repo detection (cached) & per-file status parsing
  kitty/graphics.go             # Kitty terminal graphics protocol encoding
  ui/model.go                   # Main TUI model (~5000 lines): all state, Update(), View()
  ui/styles.go                  # Centralized lipgloss styles & color system
  ui/signals_linux.go           # SIGSTOP/SIGCONT for Linux/Darwin
  ui/signals_other.go           # Signal fallbacks for other platforms
```

## Architecture

- **Elm architecture** via bubbletea: single `Model` struct → `Update(msg)` → `View()` cycle
- **Modal stack** pattern: context menu, rename, delete confirmation, image viewer, audio player, command palette, macro runner — each overrides input handling when active
- **Async messages** for file loading, image decoding, audio probing, editor spawning
- **Per-pane state**: main pane and optional split pane have independent navigation and selection

## Key Types

- `ui.Model` — All application state (CWD, entries, cursor, modals, clipboard, config)
- `config.Config` — Persisted settings: ShowDetails, ShowHidden, Colors, Favorites
- `config.Macro` — User commands with variable substitution ($FILE, $FILES, $DIR, $NAME, $INPUT)
- `fs.Entry` — File/directory entry with Name, Path, IsDir, Info
- `focus` enum — focusList, focusSidebar, focusSplit

## Configuration

Config files are JSON, located at:
- Linux: `$XDG_CONFIG_HOME/ifm/config.json` and `macros.json`
- macOS: `~/Library/Application Support/ifm/`
- Windows: `%AppData%\ifm\`

## Code Conventions

- **Naming:** CamelCase exports, camelCase private. Modal types suffixed with `Modal`/`Model`. Booleans prefixed `is`/`show`/`can`.
- **Import aliases:** `appfs` for `internal/fs`, `appconfig` for `internal/config`
- **Error handling:** Return `error`, display in status bar. No panics on normal operation — graceful degradation.
- **No tests exist** currently.
- **Single large file** (`model.go` ~5000 lines) contains the bulk of UI logic. Section dividers (`//---`) organize it internally.
- **Render functions** are broken into methods: `renderSidebar`, `renderFileList`, `renderDetails`, etc.

## External Tool Dependencies (runtime)

- **Clipboard:** `wl-copy` (Wayland), `xclip` (X11), or OSC 52 terminal escape
- **Audio:** `mpv` (preferred, IPC seeking) or `ffplay` (fallback); `ffprobe` for duration
- **Archives:** `tar`, `unzip`, `gzip`, `bzip2`, `xz`
- **Images:** Kitty graphics protocol (checked via `$TERM` or `$KITTY_PID`)

## Packaging

- **AUR:** `ionix-file-manager-git` package via `PKGBUILD` + `aur-publish.sh`
- **Build flags:** `-trimpath -mod=readonly` for reproducible builds
- **Version scheme:** `r<commit-count>.<short-hash>` (auto-computed from git)

# hiho

Browser history archiver and terminal viewer for Linux, written in Go.
Supports Firefox, Chrome, and Brave, including native and Flatpak installations.

## Install and run

Requires Go (see `go.mod`) and `xdg-open` to open URLs.

```sh
make install  # installs hi and ho to ~/.local/bin
hi            # export history to ~/Documents/history_<browser>_<hostname>.json
ho            # browse saved history in a Bubble Tea TUI
```

Ensure `~/.local/bin` is on your `PATH`. Run `hi` again to update the archives.

## Automatic exports

Run `crontab -e` and add this line to export history every hour:

```cron
0 * * * * "$HOME/.local/bin/hi" >> "$HOME/hi.log" 2>&1
```

This uses the installed binary without relying on cron's `PATH`. Check
`~/hi.log` for export messages and errors.

## Viewer

- **Tab / Shift-Tab:** switch URL, machine hostname, and browser filters.
- **Arrow keys / mouse wheel:** navigate; click to highlight.
- **Space / Ctrl-click:** mark URLs; **Alt-Space:** insert a filter space.
- **Enter:** open marked URLs (or the highlighted URL) in your default browser.
- **Esc:** clear filters or quit; **Ctrl-C:** quit.

URL search matches literal text, case-insensitively; all space-separated terms
must match. Each URL appears once, using its newest visit within the selected
filters. Results sort newest-first, or by visit count then date when filtering.

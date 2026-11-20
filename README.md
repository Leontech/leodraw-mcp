<h1 align="center">LeoDraw MCP</h1>

<p align="center">
  <b>Let Claude draw in CorelDRAW.</b><br>
  An MCP server that gives AI assistants (Claude Desktop, Claude Code, Cursor, VS Code …) hands in CorelDRAW on Windows.
</p>

<p align="center">
  <a href="https://github.com/Leontech/leodraw-mcp/releases"><b>⬇ Download for Windows</b></a>
  &nbsp;·&nbsp; <a href="https://github.com/Leontech/leodraw-mcp/releases">All releases</a>
  &nbsp;·&nbsp; <a href="README.cs.md">Česky</a>
  &nbsp;·&nbsp; <a href="https://ko-fi.com/leotechcz">☕ Buy me a coffee</a>
</p>

<p align="center">
  <a href="https://github.com/Leontech/leodraw-mcp/releases"><img alt="Release" src="https://img.shields.io/github/v/release/Leontech/leodraw-mcp?include_prereleases"></a>
  <img alt="Windows 10/11" src="https://img.shields.io/badge/Windows-10%20%7C%2011-0078D6">
  <a href="LICENSE"><img alt="GPL-3.0 License" src="https://img.shields.io/badge/license-GPL--3.0-blue"></a>
</p>

> **Work in progress (v0.1).** Unofficial project – not affiliated with Corel or Anthropic (see [Disclaimer](#disclaimer)).

## What it does

LeoDraw connects to the CorelDRAW instance running on your PC (COM automation) and exposes it as MCP tools:

| Area | Tools |
|---|---|
| App & documents | `corel_status`, `corel_launch`, `list_documents`, `activate_document`, `new_document`, `open_document`, `save_document`, `save_document_as` (also older CDR versions), `close_document`, `undo`, `redo` |
| Pages | `list_pages`, `add_pages`, `delete_page`, `set_page`, `activate_page` |
| Layers | `list_layers`, `create_layer`, `delete_layer`, `set_layer`, `set_active_layer`, `move_to_layer` |
| Inspect | `list_shapes`, `get_shape`, `find_shapes` (also by region, compact output), `get_path` (geometry as SVG path data), `select_shapes`, `clear_selection`, `set_shape_props`, `list_fonts` |
| Preview | `render_preview` – PNG of the page / region / shapes / selection, the AI sees what it drew |
| Draw | `create_rectangle`, `create_ellipse` (also arcs and pies), `create_polygon` (also stars), `create_line`, `create_path` (SVG path data → native curve), `import_file` (file, URL or base64), `import_svg`, `create_qr_code` |
| Text | `create_text`, `create_paragraph_text`, `set_text`, `set_text_style`, `replace_text`, `fill_template` (`{{placeholders}}`) |
| Transform | `transform_shapes` (move, size, scale, rotate, skew, flip), `align_shapes`, `distribute_shapes`, `duplicate_shapes`, `order_shapes`, `delete_shapes`, `copy_shapes` (to another page) |
| Style | `set_fill` (colour, none, linear / radial / conical / square gradient), `set_outline` (colour, width, hairline, caps, joins) |
| Shaping | `group`, `ungroup`, `combine`, `break_apart`, `convert_to_curves`, `shape_boolean` (weld, trim, intersect, simplify), `offset_path` |
| Bitmaps | `trace_bitmap` (PowerTRACE) |
| Production | `preflight` (checks before printing / cutting), `prepare_for_cutting` (curves + hairline + no fill) |
| Export | `export` – PDF, SVG, DXF, DWG, EPS, AI, PLT, EMF, PNG, JPG, TIFF, WebP …; page, all pages, selection or shapes |
| Developer | `batch` (many steps, one undo), `api_describe` (CorelDRAW type library), `server_reload`, `call_tool` |

Every modifying tool call is exactly one undo step in CorelDRAW.

More tools (boolean shaping, contour, bitmap tracing, preflight for laser and plotter cutting, QR codes …) are on the way.

Conventions: millimetres, page coordinates with origin at the top-left corner and Y down (like SVG), shapes referenced by CorelDRAW `StaticID`.

Single `.exe`, no runtime, no dependencies (Go standard library only).

## Requirements

- Windows 10 / 11 x64
- CorelDRAW (developed and tested with **CorelDRAW 2026**; older versions may work)
- An MCP client, e.g. [Claude Desktop](https://claude.ai/download)

## Setup

### Option A – MCP Bundle (Claude Desktop, one click)

Download `leodraw-mcp.mcpb` from [Releases](https://github.com/Leontech/leodraw-mcp/releases) and open it with a double-click –
Claude Desktop installs the server; for a new version open the new `.mcpb` the same way.
Use either the bundle or option B, not both – otherwise the tools are listed twice.

### Option B – leodraw-mcp.exe

1. Download **leodraw-mcp.zip** from [Releases](https://github.com/Leontech/leodraw-mcp/releases) and unpack it anywhere (e.g. `C:\Tools\LeoDraw`).
2. **Double-click `leodraw-mcp.exe`.** A window shows the status (Claude Desktop, registration, CorelDRAW) and a big **Register in Claude** button.
   Only LeoDraw's own entry is written to Claude Desktop's configuration (the file is backed up first). Nothing is installed into CorelDRAW.
3. Quit Claude Desktop completely (also from the system tray) and start it again. Start CorelDRAW and ask Claude to draw something.

From a terminal: `leodraw-mcp.exe --install` / `--uninstall`.

> Windows SmartScreen may warn about an unknown publisher the first time – the exe is not code-signed. Choose *More info → Run anyway*, or build it yourself from source (below).

**Claude Desktop – manual setup:** add to `%APPDATA%\Claude\claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "leodraw": {
      "command": "C:\\Tools\\LeoDraw\\leodraw-mcp.exe"
    }
  }
}
```

**Claude Code:**

```
claude mcp add leodraw -- C:\Tools\LeoDraw\leodraw-mcp.exe
```

Options: `--install` / `--uninstall`, `--gui`, `--launch` (start CorelDRAW automatically), `--toolsets=app,inspect,…`, `--log-file`, `--list-tools`, `--version`.
Log: `%LOCALAPPDATA%\LeoDraw\logs\leodraw-mcp.log`.

## Building from source

Go 1.24+, no CGO, no external modules. From the repository root (PowerShell):

```powershell
$env:GOOS="windows"; $env:GOARCH="amd64"; $env:CGO_ENABLED="0"
go build -trimpath -ldflags "-s -w" -o leodraw-mcp.exe ./cmd/leodraw-mcp
```

Tests (also on Linux / macOS): `go test ./internal/mcp ./internal/geom ./internal/colors`.
Every version change in `cmd/leodraw-mcp/main.go` pushed to `main` is built and released by GitHub Actions.

| | |
|---|---|
| `cmd/leodraw-mcp` | start, options, registration window (`gui.html`), Claude Desktop registration |
| `internal/mcp` | minimal MCP server (JSON-RPC over stdio) |
| `internal/com` | IDispatch automation client on ole32/oleaut32, STA worker thread |
| `internal/corel` | CorelDRAW helpers (connection, units, coordinates, shapes, colours) |
| `internal/tools` | the MCP tools |
| `internal/geom`, `internal/colors` | coordinate conversion, colour parsing |

## Feedback and support

Bugs and ideas: [GitHub Issues](https://github.com/Leontech/leodraw-mcp/issues) or the [form on leotech.cz](https://leotech.cz/download?fb=leodraw&lang=en). Author: **Leoš Vilím** – [leotech.cz](https://leotech.cz).

LeoDraw is free. If it saves you time, you can [buy me a coffee on Ko-fi ☕](https://ko-fi.com/leotechcz) – thank you!

## Disclaimer

LeoDraw MCP is an independent, **unofficial** project. It is not affiliated with, endorsed by or sponsored by
**Corel Corporation** or **Anthropic**. CorelDRAW is a trademark of Corel Corporation; Claude is a trademark of
Anthropic, PBC. All other trademarks belong to their respective owners.

## License

LeoDraw MCP is free software: you can redistribute it and/or modify it under the terms of the
[GNU General Public License](LICENSE) as published by the Free Software Foundation, either version 3
of the License, or (at your option) any later version. It is distributed WITHOUT ANY WARRANTY.

In short: you may use it for anything (including commercial work), but if you distribute a modified
version, you must publish its source code under the same license.

© 2026 Leoš Vilím

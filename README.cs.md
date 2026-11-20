<h1 align="center">LeoDraw MCP</h1>

<p align="center">
  <b>Ať Claude kreslí v CorelDRAW.</b><br>
  MCP server, který dá AI asistentům (Claude Desktop, Claude Code, Cursor, VS Code …) ruce v CorelDRAW na Windows.
</p>

<p align="center">
  <a href="https://github.com/Leontech/leodraw-mcp/releases"><b>⬇ Stáhnout pro Windows</b></a>
  &nbsp;·&nbsp; <a href="https://github.com/Leontech/leodraw-mcp/releases">Všechny verze</a>
  &nbsp;·&nbsp; <a href="README.md">English</a>
  &nbsp;·&nbsp; <a href="https://ko-fi.com/leotechcz">☕ Kup mi kafe</a>
</p>

<p align="center">
  <a href="https://github.com/Leontech/leodraw-mcp/releases"><img alt="Release" src="https://img.shields.io/github/v/release/Leontech/leodraw-mcp?include_prereleases"></a>
  <img alt="Windows 10/11" src="https://img.shields.io/badge/Windows-10%20%7C%2011-0078D6">
  <a href="LICENSE"><img alt="GPL-3.0 License" src="https://img.shields.io/badge/license-GPL--3.0-blue"></a>
</p>

> **Ve vývoji (v0.1).** Neoficiální projekt – nemá nic společného se společnostmi Corel ani Anthropic (viz [Upozornění](#upozornění)).

## Co umí

LeoDraw se připojí k CorelDRAW spuštěnému na vašem PC (automatizace přes COM) a nabídne ho jako MCP nástroje:

| Oblast | Nástroje |
|---|---|
| Aplikace a dokumenty | `corel_status`, `corel_launch`, `list_documents`, `activate_document`, `new_document`, `open_document`, `save_document`, `save_document_as` (i do starší verze CDR), `close_document`, `undo`, `redo` |
| Stránky | `list_pages`, `add_pages`, `delete_page`, `set_page`, `activate_page` |
| Vrstvy | `list_layers`, `create_layer`, `delete_layer`, `set_layer`, `set_active_layer`, `move_to_layer` |
| Čtení | `list_shapes`, `get_shape`, `find_shapes` (i podle oblasti, úsporný výpis), `get_path` (geometrie jako SVG cesta), `select_shapes`, `clear_selection`, `set_shape_props`, `list_fonts` |
| Náhled | `render_preview` – PNG stránky / oblasti / objektů / výběru, AI vidí, co nakreslila |
| Kreslení | `create_rectangle`, `create_ellipse` (i oblouk a výseč), `create_polygon` (i hvězda), `create_line`, `create_path` (SVG cesta → nativní křivka), `import_file` (soubor, URL nebo base64), `import_svg`, `create_qr_code` |
| Text | `create_text`, `create_paragraph_text`, `set_text`, `set_text_style`, `replace_text`, `fill_template` (`{{pole}}`) |
| Transformace | `transform_shapes` (posun, velikost, měřítko, rotace, zkosení, zrcadlení), `align_shapes`, `distribute_shapes`, `duplicate_shapes`, `order_shapes`, `delete_shapes`, `copy_shapes` (na jinou stránku) |
| Styl | `set_fill` (barva, žádná, přechod lineární / radiální / kuželový / čtvercový), `set_outline` (barva, tloušťka, vlasová čára, konce, spoje) |
| Tvarování | `group`, `ungroup`, `combine`, `break_apart`, `convert_to_curves`, `shape_boolean` (svařit, oříznout, průnik, zjednodušit), `offset_path` |
| Bitmapy | `trace_bitmap` (PowerTRACE) |
| Výroba | `preflight` (kontrola před tiskem / řezáním), `prepare_for_cutting` (křivky + vlasová čára + bez výplně) |
| Export | `export` – PDF, SVG, DXF, DWG, EPS, AI, PLT, EMF, PNG, JPG, TIFF, WebP …; stránka, všechny stránky, výběr nebo objekty |
| Pro vývoj | `batch` (více kroků, jedno Zpět), `api_describe` (typová knihovna CorelDRAW), `server_reload`, `call_tool` |

Každé volání měnícího nástroje je v CorelDRAW přesně jeden krok zpět.

Další nástroje (booleovské operace, kontura, vektorizace bitmap, kontrola pro laser a plotr, QR kódy …) přibývají.

Konvence: milimetry, souřadnice stránky s počátkem vlevo nahoře a osou Y dolů (jako SVG), objekty se odkazují přes `StaticID` z CorelDRAW.

Jedno `.exe`, žádný runtime, žádné závislosti (jen standardní knihovna Go).

## Požadavky

- Windows 10 / 11 x64
- CorelDRAW (vyvíjeno a testováno s **CorelDRAW 2026**; starší verze mohou fungovat)
- MCP klient, např. [Claude Desktop](https://claude.ai/download)

## Instalace

### Možnost A – balíček MCP Bundle (Claude Desktop, jedním klikem)

Stáhněte `leodraw-mcp.mcpb` z [Releases](https://github.com/Leontech/leodraw-mcp/releases) a otevřete ho dvojklikem –
Claude Desktop server nainstaluje; novou verzi nainstalujete stejně, otevřením nového `.mcpb`.
Použijte buď balíček, nebo možnost B, ne obojí – jinak budou nástroje v seznamu dvakrát.

### Možnost B – leodraw-mcp.exe

1. Stáhněte **leodraw-mcp.zip** z [Releases](https://github.com/Leontech/leodraw-mcp/releases) a rozbalte kamkoli (např. `C:\Tools\LeoDraw`).
2. **Dvakrát klikněte na `leodraw-mcp.exe`.** Okno ukáže stav (Claude Desktop, registrace, CorelDRAW) a velké tlačítko **Zaregistrovat do Claude**.
   Do nastavení Claude Desktop se zapíše jen řádek LeoDraw (soubor se předtím zazálohuje). Do CorelDRAW se nic neinstaluje.
3. Claude Desktop úplně ukončete (i z oblasti u hodin) a spusťte znovu. Spusťte CorelDRAW a požádejte Clauda, ať něco nakreslí.

Z příkazové řádky: `leodraw-mcp.exe --install` / `--uninstall`.

> Windows SmartScreen může poprvé varovat před neznámým vydavatelem – exe není digitálně podepsané. Zvolte *Další informace → Přesto spustit*, nebo si ho sestavte ze zdrojáků (níže).

**Claude Desktop – ručně:** do `%APPDATA%\Claude\claude_desktop_config.json` přidejte:

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

Přepínače: `--install` / `--uninstall`, `--gui`, `--launch` (spustit CorelDRAW automaticky), `--toolsets=app,inspect,…`, `--log-file`, `--list-tools`, `--version`.
Log: `%LOCALAPPDATA%\LeoDraw\logs\leodraw-mcp.log`.

## Sestavení ze zdrojáků

Go 1.24+, bez CGO, bez externích modulů. Z kořene repozitáře (PowerShell):

```powershell
$env:GOOS="windows"; $env:GOARCH="amd64"; $env:CGO_ENABLED="0"
go build -trimpath -ldflags "-s -w" -o leodraw-mcp.exe ./cmd/leodraw-mcp
```

Testy (i na Linuxu / macOS): `go test ./internal/mcp ./internal/geom ./internal/colors`.
Každou změnu verze v `cmd/leodraw-mcp/main.go` poslanou do `main` sestaví a vydá GitHub Actions.

## Zpětná vazba a podpora

Chyby a nápady: [GitHub Issues](https://github.com/Leontech/leodraw-mcp/issues) nebo [formulář na leotech.cz](https://leotech.cz/download?fb=leodraw&lang=cs). Autor: **Leoš Vilím** – [leotech.cz](https://leotech.cz).

LeoDraw je zdarma. Když vám šetří čas, můžete mi [koupit kafe na Ko-fi ☕](https://ko-fi.com/leotechcz) – díky!

## Upozornění

LeoDraw MCP je nezávislý **neoficiální** projekt. Společnosti **Corel Corporation** ani **Anthropic** s ním nemají
nic společného, nepodporují ho ani nesponzorují. CorelDRAW je ochranná známka společnosti Corel Corporation,
Claude je ochranná známka společnosti Anthropic, PBC. Ostatní ochranné známky patří jejich vlastníkům.

## Licence

LeoDraw MCP je svobodný software: můžete ho šířit a upravovat podle podmínek [GNU General Public License](LICENSE)
vydané Free Software Foundation, verze 3 nebo (podle vaší volby) kterékoli novější. Šíří se BEZ JAKÉKOLI ZÁRUKY.

Zkráceně: můžete ho používat k čemukoli (i komerčně), ale když šíříte upravenou verzi, musíte zveřejnit její
zdrojové kódy pod stejnou licencí. Závazné je anglické znění licence.

© 2026 Leoš Vilím

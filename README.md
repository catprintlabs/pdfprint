# pdfprint

A robust PDF printer for Windows — a port of the CUPS/`foomatic-rip` approach
that macOS and Linux use.

## The problem

Windows has no PDF processor in its print path. Its native graphics model is
GDI/XPS, so printing a PDF means either shelling out to Adobe (fragile,
licensing) or blasting a full-page bitmap at the printer (huge, and text/vectors
lose crispness). macOS and Linux don't have this problem: their print stack
(CUPS) translates the PDF into the printer's **native language** — PostScript
for PostScript printers, PCL for PCL printers — and lets the printer's own RIP
do the rasterizing at device resolution.

`pdfprint` brings that same approach to Windows.

## ⬇️ Download

**[Download the latest Windows build →](https://github.com/catprintlabs/pdfprint/releases/latest/download/pdfprint-windows-amd64.zip)**
(`pdfprint-windows-amd64.zip`) — extract and run; Ghostscript is bundled, so there's
nothing else to install. Then see [Quick start](#quick-start).

(Or browse [all releases](https://github.com/catprintlabs/pdfprint/releases/latest),
including the bare `pdfprint.exe` / `stamp.exe`.)

## Quick start

### 1. Install

1. **[Download `pdfprint-windows-amd64.zip`](https://github.com/catprintlabs/pdfprint/releases/latest/download/pdfprint-windows-amd64.zip)**
   (or pick it from the [Releases page](https://github.com/catprintlabs/pdfprint/releases/latest)).
2. Right-click it → **Extract All** → pick a folder, e.g. `C:\pdfprint`.

That's the whole install — no installer, no admin rights. Windows `.exe`s need no
"make executable" step, and **Ghostscript is bundled**: a `gs\` folder extracts
right alongside the binaries, so there's nothing else to download. Just keep the
three items together — `pdfprint.exe`, `stamp.exe`, and `gs\`.

Extracting does **not** put `pdfprint` on your PATH, so out of the box you run it
from a terminal opened *in* that folder (Shift+right-click the folder → *Open
PowerShell window here*). To type just `pdfprint` from anywhere, add the folder to
your PATH once (no admin needed; reopen the terminal afterward). **Replace
`C:\pdfprint` with the folder you actually extracted to in step 2** — this is the
folder that contains `pdfprint.exe`, not a fixed path:

```powershell
setx PATH "$env:PATH;C:\pdfprint"   # <-- change C:\pdfprint to YOUR extract folder
```

> Already have 64-bit Ghostscript installed, or want to use your own copy? The
> bundled `gs\` is optional — see [Ghostscript](#ghostscript).

### 2. Print

The output device and transport are detected automatically from the printer, so
the simplest case is just:

```bat
pdfprint --printer "<printer name>" job.pdf
```

`--printer` accepts any **unique substring** of an installed printer's name, so you
rarely need the full string:

```bat
pdfprint --list-printers                        :: see exact names
pdfprint --printer "LaserJet" job.pdf           :: will match "LaserJet 600"
pdfprint --printer "<name>" --dry-run job.pdf   :: preview device+transport, prints nothing
```

Printing is **1:1 with no scaling** by default. That's the whole tool for most
uses; everything below is detail and control.

*Note: A first print may show two one-time prompts:*
- ***"Windows protected your PC"** — the exe is unsigned; click **More info → Run
  anyway**. (Code-signing would remove this.)*
- *A **firewall prompt** on the first network print — **Allow** it (needed to reach
  a network printer).*

## How it works

`pdfprint` replicates the CUPS pipeline on Windows:

```
PDF ─▶ [parse PPD] ─▶ [build Ghostscript command] ─▶ [run gs] ─▶ PCL/PS ─▶ [send raw: TCP or spooler]
```

1. **Parse the PPD** (optional). A PPD describes the printer and, for
   non-PostScript printers, names the Ghostscript output device
   (`*FoomaticRIPCommandLine`), page sizes, duplex, and resolution — exactly what
   `foomatic-rip` reads. If no PPD is supplied on the command line, `pdfprint` detects the device from the
   printer instead (see [Devices](#devices--auto-detection)).
2. **Build the Ghostscript command.** Pick the device (`pxlcolor`/`pxlmono` for
   PCL-XL/PCL6, `ljet4` for PCL5, `ps2write` for PostScript), resolution, page
   geometry, duplex, and copy count — from the PPD or detection, all overridable by
   flags.
3. **Run Ghostscript.** `gs` translates the PDF into the printer's native
   language. Text and vectors stay as native PCL/PS commands; only content the
   language can't express (transparency, gradients) is rasterized — same as CUPS.
4. **Send it the raw data.** The bytes reach the device untouched, by whichever
   transport fits the printer (chosen automatically — see
   [Transport](#transport-how-the-bytes-reach-the-printer)): for a **network**
   printer, a direct raw-TCP socket (AppSocket/JetDirect, port 9100); for a
   **local/USB** printer, the Windows spooler with the `RAW` datatype
   (`OpenPrinter`/`StartDocPrinter`/`WritePrinter`). Either way the host print
   driver is bypassed — the printer receives its native language directly.

In summary: Ghostscript is the engine, and this tool is the orchestration layer
Windows is missing.

## Detailed Usage

### Selecting the printer

- `--printer <name-or-substring>` — the installed printer. Any substring that
  **uniquely** identifies one printer works. If a
  substring matches more than one printer, `pdfprint` lists the matches and stops.
- `--host <ip[:port]>` (with `--port`, default 9100) — skip the installed queues
  entirely and dial a network printer directly over raw TCP. Works on any OS.
- `--list-printers` — print the exact installed names and exit.
- `--list-trays` — list every installed printer with its input trays and the
  paper loaded in each, then exit. Tray/loaded-media state lives on the device,
  so this **queries each printer over the network** (IPP, then SNMP) with a short
  timeout, skipping printers with no reachable IP (USB/local) or that don't
  answer. Use `--probe` (below) to inspect a single printer's trays.

### Devices & auto-detection

A `--device` selects a Ghostscript output device — i.e. which **page-description
language** Ghostscript emits. The goal is to match what the printer's own RIP
understands, so text and vectors stay crisp at device resolution.

| `--device` | Emits | Color | Generation | Use for |
|---|---|---|---|---|
| `pxlcolor` | PCL-XL (PCL 6) | color | newer | Modern color laser printers |
| `pxlmono`  | PCL-XL (PCL 6) | grayscale | newer | Same printers, black-and-white |
| `ljet4`    | PCL5e | mono | older | Legacy LaserJets / PCL5-only printers |
| `ps2write` | PostScript (level 2) | color | — | PostScript printers / RIPs |

**Two families:** *PCL* (HP's Printer Command Language) comes in the older,
text-oriented *PCL5* (`ljet4`) and the newer, compact **binary** *PCL-XL / PCL 6*
(`pxlcolor` / `pxlmono`) that most laser printers made in the last ~20 years
speak. *PostScript* (`ps2write`) is Adobe's language, understood by PostScript
printers and imagesetters.

**You usually don't pick this.** With no `--device` and no PPD, `pdfprint`
**asks the printer** which languages it accepts — over IPP (port 631), falling
back to SNMP (161) — and chooses accordingly, preferring native PCL-XL. It always
reports what it detected and what it chose. If it **can't** detect (the printer is
unreachable, or you're writing to a file), it **refuses rather than guess** —  you must pass
`--device`. 

To inspect detection without printing — including the **input trays and the
paper loaded in each** (parsed from IPP `media-col-ready`, or the SNMP Printer-MIB
`prtInputTable` as a fallback):

```bat
pdfprint --probe --printer "<name>"
:: <model>; languages: PCL, PostScript, PDF, ...; color; duplex (via IPP)
:: media (supported): na_letter_8.5x11in, na_legal_8.5x14in, ...
:: trays:
::   Tray 1: empty / not reported
::   Tray 2: Letter (stationery)
::   Tray 3: Legal
:: suggested: --device pxlcolor (PCL-XL, color)
```

(For all printers at once, use `--list-trays`.)

Resolution order: explicit `--device` → the PPD's device → probe the printer →
error. A PPD's device is inferred from its Foomatic command line, then
`cupsFilter` hints, then the model name.

> **Output size depends on content:** all devices keep text/vectors as native
> commands and rasterize only what the language can't express. A vector-heavy
> page is far smaller as PCL-XL than PostScript; a raster/image-heavy page can be
> *larger* as PCL-XL than PostScript.

### Transport: how the bytes reach the printer

Getting Ghostscript's output *to the device* is the part that's Windows-specific
and subtle. `pdfprint` picks the transport automatically (`--transport auto`, the
default) from the printer you name:

- **Network printer → direct raw TCP (port 9100).** The tool opens a socket to
  the device and streams the PCL/PS — the same thing CUPS's `socket://` backend
  and `lp -o raw` do. **Nothing to install: no port, no queue, no driver.** It
  discovers the device IP from the queue you already have (a Standard TCP/IP
  port's host address, or a **WSD** queue's PnP `LocationInformation`).
- **Local/USB printer → Windows RAW spooler** (`WritePrinter` with the `RAW`
  datatype), where raw passthrough works correctly.

_**Why not always the spooler?** Because spooling the RAW datatype to a **WSD
port** or a **V4 print driver** silently *fails*: the spooler accepts the job,
reports success, and the device prints nothing (WSD/V4 force jobs through the
XPS/print-filter pipeline, which discards raw PCL/PS). Most modern network
printers install as WSD by default, so `pdfprint` routes around this by talking
to the device directly._

Overrides for edge cases:

- `--transport socket` — force raw TCP for a named printer (errors if no IP can
  be discovered).
- `--transport spooler` — force the Windows spooler (for a genuinely
  raw-capable/local queue).
- `--host <ip>` — bypass discovery and dial directly (as above).

`--dry-run` shows the resolved transport (and device) without printing.

### Page size & scaling

No-scaling is the **default** (`--scale none`): the page is placed 1:1 and the
media is locked to the requested size (`-dDEVICEWIDTHPOINTS/HEIGHTPOINTS` +
`-dFIXEDMEDIA`), so an over-sized page clips rather than shrinks. Pass
`--scale fit` only if you *want* Ghostscript to scale pages to the sheet.

`--page-size <keyword>` works with or without a PPD — with a PPD it uses the
PPD's `*PaperDimension`; without one it uses a built-in table (Letter, Legal, A4,
A3, Tabloid, Ledger, Executive, Statement). With no `--page-size`, gs uses the
PDF's own MediaBox (still 1:1).

You can also specify the page size in absolute dimensions:  i.e. `--page-size 8.5x14in` or `--page-size 210x297mm`

### Other options

- `--copies N`, `--duplex none|long|short` — emitted as native `setpagedevice`.
- `--color auto|color|mono` — overrides the detected color mode (also steers the
  auto device toward `pxlcolor` vs `pxlmono`).
- `--resolution <dpi>` — defaults to the PPD's `DefaultResolution`.
- `--gs <path>` — Ghostscript binary; auto-detected if omitted.
- **Verbosity:** `--quiet` / `-q` (errors only) · default (progress + what was
  detected) · `-v` (adds the gs command, gs path, PPD, and probe detail).

### Reading & writing streams

Read the PDF from **stdin** with `-` as the input path. Write the raw stream to a
file with `--output <file>`, or to **stdout** with `--output -` (for piping).

```bat
:: capture the raw PCL/PS to a file (no printer needed — good for inspection)
pdfprint --device pxlcolor --output out.pcl job.pdf
```

#### Printing from macOS/Linux (CUPS, raw)

`--printer` targets the *Windows* spooler. On macOS/Linux, emit the raw stream to
stdout and pipe it into `lp -o raw`, which sends the bytes straight to the
printer — bypassing the CUPS driver so nothing rescales (the same thing the
Windows raw-TCP path does):

```sh
lpstat -e     # find the exact CUPS queue name

pdfprint --scale none --device ps2write --page-size Legal --output - job.pdf \
  | lp -d <queue-name> -o raw
```

(`--host <ip>` also works cross-platform if you prefer to skip CUPS entirely.)

## Ghostscript

`pdfprint` uses **Ghostscript** as its rendering engine — it shells out to `gs` as
a separate process (it does *not* link `libgs`). The Windows release **bundles** it,
so most users install nothing: a working gs is just two files (`gswin64c.exe` +
`gsdll64.dll`, ~24 MB — gs ROM-embeds its resources into the DLL), and they ride in
the `gs\` folder that extracts beside `pdfprint.exe`.

**Using your own copy.** You don't need the bundled `gs\` if you already have
64-bit Ghostscript, or want to point at a specific build. `pdfprint` resolves gs in
this order:

1. `--gs <path>` — an explicit path you pass.
2. A gs bundled next to the exe (`<exedir>\gs\gswin64c.exe`).
3. A system-wide install (`C:\Program Files\gs\gs*\bin\gswin64c.exe`).
4. `gswin64c` / `gs` on your PATH.

So a bundled gs is used deterministically when present, and an existing system gs
still works when nothing is bundled. Install 64-bit Ghostscript from
<https://ghostscript.com/releases/>, or drop a `gs\` folder beside the exe yourself
with `scripts/vendor-gs.ps1`.

The bundled Ghostscript is included **unmodified** under the **AGPL-3.0**
(`gs\GHOSTSCRIPT-*.txt`); see [License](#license). The bare `pdfprint.exe` /
`stamp.exe` are also published as separate Release assets, for when Ghostscript is
already present.

## Testing

**Unit tests** — pure Go, no external deps (no printer or gs needed), safe
anywhere. They cover PPD parsing (`internal/ppd`), gs command construction
(`internal/gs`), the capability probe + BER encoding (`internal/probe`), the
printer-name matcher (`internal/spool`), and the stamp overlay (`internal/stamp`):

```sh
make test
# or, if Go isn't on PATH (fresh Windows shell):
& "C:\Program Files\Go\bin\go.exe" test ./...
```

**Preview without paper** — `--dry-run` shows the resolved device, gs command,
and transport with no side effects; `--probe` shows detected capabilities:

```bat
pdfprint --printer "<name>" --dry-run job.pdf
pdfprint --probe --printer "<name>"
```

**Render to a file and check geometry** — verifies gs output without a printer;
rasterize the result to confirm exact media (e.g. Legal = 5100×8400 px @ 600 dpi):

```bat
pdfprint --scale none --device pxlmono --page-size Legal --output out.pcl job.pdf
```

**Self-documenting smoke test** — `stamp` overlays a timestamp, host, the print
command, and notes onto the page, so the paper records exactly what produced it
(and disambiguates otherwise-identical test pages):

```bat
:: Windows (make usually isn't installed):
powershell -File scripts\smoke-test.ps1 -Printer "<name>"
powershell -File scripts\smoke-test.ps1 -HostIp <printer-ip>
```

```sh
# macOS/Linux dev host:
make print-test HOST=<printer-ip>
```

## Building

Requires **Go 1.22+** to build, and **Ghostscript** at runtime (auto-detected).
The project is pure Go with **no third-party dependencies** — the Windows
spooler, registry discovery, and IPP/SNMP probing are hand-rolled. The
Windows-only code is behind a `//go:build windows` tag, so the whole project
builds and tests on macOS/Linux too (use `--output` or `--host` there).

**Windows** (no `make` required — `scripts/build.ps1` finds Go on PATH or at
`C:\Program Files\Go`):

```powershell
./scripts/build.ps1              # builds pdfprint.exe and stamp.exe
./scripts/build.ps1 -OutDir dist # build into dist/
./scripts/build.ps1 -Clean       # remove the built exes
```

**macOS/Linux** (via `make`):

```sh
make            # build host binaries (pdfprint, stamp)
make windows    # cross-compile pdfprint.exe + stamp.exe (windows/amd64)
make test       # run unit tests
```

### Cloning (Git LFS required)

PDF test fixtures under `testdata/` are stored with
**[Git LFS](https://git-lfs.com)**. Install LFS **before cloning**, or you'll get
pointer files instead of real PDFs:

```sh
# macOS: brew install git-lfs   |   Windows: bundled with Git for Windows,
#                                    or: winget install GitHub.GitLFS
git lfs install     # once per machine
git clone <repo-url>

# already cloned before installing LFS? fix in place:
git lfs install && git lfs pull
```

`.gitattributes` declares the LFS globs (`testdata/*.pdf`).

## Packaging & releasing

`pdfprint` is can be **bundled inside a larger app (i.e.Electron)
app**, not installed on its own.

**Ghostscript travels as a sibling.** The Windows gs build ROM-embeds its
resources into `gsdll64.dll`, so a working gs is just two files (~24 MB):
`gswin64c.exe` + `gsdll64.dll`. `scripts/vendor-gs.ps1` copies them into
`vendor/gs/`. `pdfprint` resolves gs as: `--gs` flag → a gs bundled next to the
exe (`<exedir>\gs\gswin64c.exe`) → system install → PATH. So a bundled gs is used
deterministically, while an existing system gs still works if nothing is bundled.

**Ship both as Electron `extraResources`.** Lay out `resources/bin/` as
`pdfprint.exe`, `stamp.exe`, `gs/gswin64c.exe`, `gs/gsdll64.dll`, and spawn from
`process.resourcesPath` (gs is auto-found as a sibling, or pass `--gs`):

```jsonc
// electron-builder config
"extraResources": [ { "from": "resources/bin", "to": "bin" } ]
```

```js
const { execFile } = require("node:child_process");
const path = require("node:path");
const bin = path.join(process.resourcesPath, "bin");
execFile(path.join(bin, "pdfprint.exe"),
  ["--printer", printerName, jobPdfPath],
  (err, stdout, stderr) => { /* ... */ });
```

**Binaries are built in CI, not committed** — a **GitHub Actions** workflow
([.github/workflows/release.yml](.github/workflows/release.yml)) produces the
release artifacts on a version tag, so no Windows machine is needed to cut a
release (you can do it from macOS/Linux).

### Cutting a release

```sh
# 1. Make sure main is committed, tests pass, and it's pushed.
make test && git push origin main

# 2. Tag with a semver version — the workflow triggers on tags matching v*.
git tag v0.1.0
git push origin v0.1.0
```

Pushing the tag runs the workflow, which:

1. runs the unit tests (the release is gated on them),
2. cross-compiles `pdfprint.exe` / `stamp.exe` (windows/amd64) from Linux,
3. downloads the official Windows Ghostscript and packages a self-contained
   **`pdfprint-windows-amd64.zip`** — exes + bundled `gs\` + its AGPL
   license/source (see [Ghostscript](#ghostscript)),
4. publishes all three (the two exes and the zip) as **GitHub Release assets**.

**Verify:** the **Actions** tab shows the `release` run green, and the
**Releases** page lists `pdfprint.exe`, `stamp.exe`, and
`pdfprint-windows-amd64.zip`. Download the zip and confirm it runs.

> The gs-bundling step runs for the first time on the first tag. If it fails, the
> Actions log shows where (usually the gs download or the 7z/`find` paths). To
> retry: **Re-run jobs** on the Actions tab, or delete and re-push the tag
> (`git push --delete origin v0.1.0 && git tag -d v0.1.0`, then re-tag). The
> publish step re-uploads assets with `--clobber`, so retries are safe.

The Electron app pulls the bare `pdfprint.exe` / `stamp.exe` assets (plus a
vendored gs) into `resources/bin/` at package time; `electron-builder` publishes
the packaged app and `electron-updater` auto-updates clients.

## Status

Verified end-to-end on **macOS and three different Windows 11 machines**, against
**two printer brands**: a Legal no-scaling test page prints 1:1 (measured on
paper — tick *N* lands exactly *N* inches from center) via all three output paths —
PostScript (`ps2write`), PCL-XL (`pxlmono`/`pxlcolor`), and PCL5e (`ljet4`) —
matching in size across platforms. On Windows it prints through a printer's
*existing WSD queue* with no port/queue/driver setup — the tool auto-discovers the
device IP, auto-detects the device via IPP, and streams over raw TCP. No-scaling is
guaranteed via `-dDEVICEWIDTHPOINTS/HEIGHTPOINTS` + `-dFIXEDMEDIA` and confirmed by
rasterizing output to exact pixel dimensions.

### Not yet done / next

- Full Foomatic option substitution (the `%A`–`%Z` group encoding and
  `*FoomaticRIPOptionSetting` code injection) — currently we use the device plus
  standard `setpagedevice` options, not the PPD's per-option code snippets.
- Auto-locating an installed printer's PPD (we can list/match printers and probe
  them, but not yet read their driver PPD).
- `UIConstraints` (rejecting incompatible option combinations).
- Media source / input-tray selection; N-up and page ranges (the `pdftopdf`
  pre-filter stage).

## License

`pdfprint` invokes Ghostscript as a **separate executable** (via process exec),
not by linking `libgs` — so Ghostscript remains an independent work under its own
license: **AGPL-3.0** (GNU Affero General Public License v3.0). If you distribute
a build that **bundles** Ghostscript, that copy carries AGPL-3.0 obligations
(make its source and license available, per the AGPL); alternatively, obtain a
commercial Ghostscript license from Artifex.

`pdfprint`'s own source is licensed separately under the **MIT License** (see
[LICENSE](LICENSE)) — exec'ing Ghostscript is mere aggregation, so gs's AGPL does
not extend to pdfprint's code.

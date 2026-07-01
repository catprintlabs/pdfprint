# pdfprint

A robust PDF printer for Windows — a port of the CUPS/`foomatic-rip` approach
that macOS and Linux use.

## The problem

Windows has no PDF rasterizer in its print path. Its native graphics model is
GDI/XPS, so printing a PDF means either shelling out to Adobe (fragile,
licensing) or blasting a full-page bitmap at the printer (huge, and text/vectors
lose crispness). macOS and Linux don't have this problem: their print stack
(CUPS) translates the PDF into the printer's **native language** — PostScript
for PostScript printers, PCL for PCL printers — and lets the printer's own RIP
do the rasterizing at device resolution.

## What this does

`pdfprint` replicates that pipeline on Windows:

```
PDF ──▶ [parse PPD] ──▶ [build Ghostscript command] ──▶ [run gs] ──▶ PCL/PS ──▶ [Windows spooler, RAW]
```

1. **Parse the PPD.** The PPD describes the printer and, for non-PostScript
   printers, names the Ghostscript output device (`*FoomaticRIPCommandLine`),
   page sizes, duplex, and resolution. This is exactly what `foomatic-rip`
   reads.
2. **Build the Ghostscript command.** Pick the device (`pxlcolor`/`pxlmono` for
   PCL-XL/PCL6, `ljet4` for PCL5, `ps2write` for PostScript), resolution, page
   geometry, duplex, and copy count — from the PPD, overridable by flags.
3. **Run Ghostscript.** `gs` translates the PDF into the printer's native
   language. Text and vectors stay as native PCL/PS commands; only content PCL
   can't express (transparency, gradients) is rasterized — same as CUPS.
4. **Spool it RAW.** The bytes go straight to the Windows spooler with the `RAW`
   datatype (`OpenPrinter`/`StartDocPrinter`/`WritePrinter`), bypassing any host
   driver — the printer receives its native language directly.

Ghostscript is the engine (AGPL — fine for internal use). This tool is the
orchestration layer Windows is missing.

## Running on Windows (real printer)

First run, start to finish:

```bat
:: 1. Install Ghostscript (64-bit) from https://ghostscript.com/releases/
::    pdfprint auto-detects C:\Program Files\gs\gs*\bin\gswin64c.exe — no PATH edit needed.

:: 2. Get the EXACT printer name (this is what --printer must match):
pdfprint --list-printers

:: 3. Dry-run: see the Ghostscript command without printing anything:
pdfprint --ppd printer.ppd --page-size Legal --dry-run job.pdf

:: 4. Print. Simplest case — an 8.5x14 (Legal) PDF to a Legal-only printer, no scaling:
pdfprint --device pxlmono --page-size Legal --printer "HP LaserJet 4200" job.pdf
```

No-scaling is the **default** (`--scale none`): the page is placed 1:1 and the
media is locked to the requested size (`-dDEVICEWIDTHPOINTS/HEIGHTPOINTS` +
`-dFIXEDMEDIA`), so an over-sized page clips rather than shrinks. Pass
`--scale fit` only if you *want* Ghostscript to scale pages to the sheet.

`--page-size` works with or without a PPD — with a PPD it uses the PPD's
`*PaperDimension`; without one it uses a built-in table (Letter, Legal, A4, A3,
Tabloid, Ledger, Executive, Statement).

## Usage

```sh
# Print to a Windows printer, device inferred from the PPD:
pdfprint --ppd hp.ppd --printer "HP LaserJet 600" job.pdf

# Force device/resolution, 2 copies, long-edge duplex:
pdfprint --device pxlcolor --resolution 600 --copies 2 --duplex long \
         --printer "HP LaserJet 600" job.pdf

# See the exact Ghostscript command without running it:
pdfprint --ppd hp.ppd --dry-run job.pdf

# Local test on macOS/Linux — capture the raw PCL/PS to a file:
pdfprint --ppd hp.ppd --device pxlcolor --output out.pcl job.pdf
```

Read the PDF from stdin with `-` as the input path. Write the raw stream to
stdout with `--output -`, which is how you print from macOS/Linux (below).

### Printing from macOS/Linux (CUPS, raw)

The `--printer` flag targets the *Windows* spooler. On macOS/Linux, emit the raw
stream to stdout and pipe it into `lp -o raw`, which sends the bytes straight to
the printer — bypassing the CUPS driver so nothing rescales:

```sh
# Find the exact CUPS queue name:
lpstat -e

# Print the no-scaling test page (8.5x14 Legal) 1:1 — PostScript path:
pdfprint --scale none --device ps2write --page-size Legal --output - testdata/legal_ruler.pdf \
  | lp -d <queue-name> -o raw -t "pdfprint no-scaling test"

# Same page via the PCL-XL path (compare PCL vs PS on the same printer):
pdfprint --scale none --device pxlmono --page-size Legal --output - testdata/legal_ruler.pdf \
  | lp -d <queue-name> -o raw -t "pdfprint no-scaling test (PCL-XL)"
```

Both were verified end-to-end on a Xerox VersaLink C620 (Legal loaded): ticks
measure exactly 1.00 in and tick *N* lands *N* inches from the center crosshair —
i.e. true 1:1, no scaling. Confirm the queue reached the printer with
`lpstat -o <queue-name>` (empty queue = job sent).

Key flags: `--list-printers`, `--page-size`, `--scale none|fit`, `--copies N`,
`--duplex none|long|short`, `--color auto|color|mono`, `--device`, `--gs <path>`
(auto-detected if omitted), `--dry-run`, `-v`.

### Devices

| Printer type        | `--device`            | Output           |
|---------------------|-----------------------|------------------|
| HP / PCL6 (PCL-XL)  | `pxlcolor` / `pxlmono`| PCL-XL           |
| Older HP / PCL5     | `ljet4`               | PCL5             |
| PostScript          | `ps2write`            | PostScript       |

If `--device` is omitted, it's inferred from the PPD (Foomatic command line,
then `cupsFilter` hints, then the model name).

## Cloning the repo (Git LFS required)

The PDF test fixtures under `testdata/` (larger sample tickets can be several MB)
are stored with **[Git LFS](https://git-lfs.com)**, not directly in Git history.
You must have Git LFS installed **before you clone**, or you'll get tiny text
pointer files instead of the actual PDFs.

Install it once per machine:

```sh
# macOS (Homebrew):
brew install git-lfs

# Windows: install "Git for Windows" (git-lfs is bundled), or standalone:
#   winget install GitHub.GitLFS      (or download from https://git-lfs.com)

# then, once, per user account (all OSes):
git lfs install
```

Then clone normally — LFS files download automatically:

```sh
git clone <repo-url>
```

Already cloned *before* installing LFS? Fix it in place:

```sh
git lfs install
git lfs pull        # replace the pointer files with the real PDFs
```

`.gitattributes` declares which files use LFS (`testdata/*.pdf`). To add more
large binary fixtures, `git lfs track "<glob>"` and commit the updated
`.gitattributes`.

## Build

Requires Go 1.22+ and, at runtime, Ghostscript (`gswin64c.exe` on Windows).

```sh
make            # build host binary
make windows    # cross-compile pdfprint.exe (windows/amd64)
make test       # run tests
```

The Windows spooler layer is behind a `//go:build windows` tag, so the whole
project builds and tests on macOS/Linux; use `--output <file>` there.

## Status

First vertical slice: PPD parse → device/resolution/page-size/duplex/copies →
gs → RAW spooler. Verified end-to-end on macOS producing real PCL-XL and
PostScript; the `.exe` cross-compiles.

Added for real-printer testing: Ghostscript auto-detection on Windows,
`--list-printers`, `--page-size` (PPD or built-in table), and guaranteed
no-scaling (`-dDEVICEWIDTHPOINTS/HEIGHTPOINTS` + `-dFIXEDMEDIA`), verified by
rasterizing output to exactly 5100×8400 px (8.5×14" @ 600 dpi).

Printed the Legal no-scaling test page (`testdata/legal_ruler.pdf`) on a real
Xerox VersaLink C620 via `lp -o raw`, both PostScript (`ps2write`) and PCL-XL
(`pxlmono`) paths: measured 1:1, no scaling. See "Printing from macOS/Linux".

### Not yet done / next

- Full Foomatic option substitution (the `%A`-`%Z` group encoding and
  `*FoomaticRIPOptionSetting` code injection) — currently we use device +
  standard `setpagedevice` options, not the PPD's per-option code snippets.
- Reading the driver's PPD automatically (we can list printers now, but not yet
  locate their PPDs).
- `UIConstraints` handling (rejecting incompatible option combinations).
- Media source / input tray selection.
- N-up, page ranges (the `pdftopdf` pre-filter stage).

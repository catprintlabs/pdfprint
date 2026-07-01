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

Read the PDF from stdin with `-` as the input path.

### Devices

| Printer type        | `--device`            | Output           |
|---------------------|-----------------------|------------------|
| HP / PCL6 (PCL-XL)  | `pxlcolor` / `pxlmono`| PCL-XL           |
| Older HP / PCL5     | `ljet4`               | PCL5             |
| PostScript          | `ps2write`            | PostScript       |

If `--device` is omitted, it's inferred from the PPD (Foomatic command line,
then `cupsFilter` hints, then the model name).

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

### Not yet done / next

- Full Foomatic option substitution (the `%A`-`%Z` group encoding and
  `*FoomaticRIPOptionSetting` code injection) — currently we use device +
  standard `setpagedevice` options, not the PPD's per-option code snippets.
- Enumerating installed Windows printers / reading the driver's PPD
  automatically.
- `UIConstraints` handling (rejecting incompatible option combinations).
- Media source / input tray selection.
- N-up, scaling, page ranges (the `pdftopdf` pre-filter stage).

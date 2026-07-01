# pdfprint — design & decision log

This document records *how this project came to be the way it is*: the problem
framing, the decisions we made and why, the alternatives we rejected, and what
was actually verified. It's meant to let anyone (including future us) reconstruct
the reasoning without having been in the room.

## 1. The original ask

> "Let's build a robust PDF printer for the PC. macOS can do it, Linux can do
> it — can we reverse-engineer their solutions and port to the PC (Windows)?"

Context: CatPrint is a commercial printing operation. The pain is printing PDFs
**on Windows**, which is unreliable compared to macOS/Linux.

## 2. Why macOS/Linux "can do it" and Windows can't

- **macOS**: CUPS + Quartz. PDF is the native graphics model; the print stack
  speaks PDF end-to-end.
- **Linux**: CUPS + Ghostscript/poppler. The CUPS filter chain translates PDF
  into the printer's native language.
- **Windows**: the native graphics model is GDI/XPS. There is **no PDF
  rasterizer/translator in the print path**. So printing a PDF means either
  shelling out to Adobe (fragile, licensing) or bringing your own engine.

That last point is the entire reason this project exists.

## 3. The key pivot: NOT "PDF → one big bitmap"

The first architecture proposed was PDF → full-page bitmap → spooler. **Mitch
rejected this**, correctly:

> "PDF to bitmap is not a great solution, I don't think it's actually how print
> drivers print PDF. I think they translate the PDF to some other printer-
> specific format, i.e. PCL. I think we need PDF → PCL plus PPD file."

This is right, and it matches how CUPS actually works:

- The **PPD** describes the printer and, for non-PostScript printers, literally
  contains a **Ghostscript command line** (`*FoomaticRIPCommandLine`) naming the
  output *device*.
- **`foomatic-rip`** (a ~2000-line Perl script in the CUPS chain) reads the PPD,
  extracts that gs command, substitutes the user's options, and runs Ghostscript
  to emit the printer's native language:
  - PostScript printer → `ps2write`/`pdftops` → **PostScript** (printer's RIP
    finishes the job)
  - PCL printer → gs `pxlcolor`/`pxlmono` (PCL-XL/PCL6) or `ljet4` (PCL5) → **PCL**
- The bytes are sent to the spooler **raw**, bypassing any host driver.

One honest nuance we kept in mind: PCL-XL still *embeds* raster for content it
can't express as vectors (transparency, gradients) — Ghostscript rasterizes only
those parts. Text and simple vectors stay as native PCL at device resolution.
This is exactly the CUPS behavior, and it's why it's both robust and compact.

**Conclusion:** the project is a **`foomatic-rip` clone for Windows**:
`parse PPD → build gs command → run gs → write RAW to the Windows spooler`.

## 4. Decisions (and who made them)

| Decision | Choice | Why | Rejected alternatives |
|---|---|---|---|
| Direction | Print existing PDFs → physical printers | The actual pain point | Virtual "Print to PDF"; full print-server |
| Rendering engine | **Ghostscript** | Already does PDF→PCL/PS; runs on Windows; it's literally what CUPS/foomatic use | pdfium/MuPDF (would need to write our own PCL emitter) |
| Licensing | **AGPL Ghostscript OK** (Mitch) | Tool is **internal-use only**, not distributed | Commercial gs license / permissive engine (unnecessary for internal use) |
| Language | **Go** (Mitch confirmed) | Single static `.exe`, trivial cross-compile from his Mac (`GOOS=windows`), direct `winspool.drv` syscalls, shells out to gs like foomatic does | Python+PyInstaller (must build exe on Windows), C#/.NET (new lang for a Ruby/JS shop), Ruby (no clean single-exe, awkward Win32) |
| Device selection | **PPD-driven** (Mitch: "detect from PPD") | Faithful to foomatic; works across mixed fleets | Hardcoding a device |
| Delivery | **CLI / EXE** | Scriptable, integrates into existing workflow | Windows service, library/SDK, GUI (all deferred) |

## 5. Environment as found (dev machine = Mitch's Mac)

- macOS (darwin arm64). **No Go, no Ghostscript, no .NET** initially; had Python
  3.9, Ruby (rbenv/bundler), Node/Yarn → a Ruby/JS shop.
- Installed via brew: **Go 1.26.x**, **Ghostscript 10.07.1**.
- macOS's own CUPS filters are Apple's CoreGraphics ones (`cgpdftops`,
  `cgpdftoraster`) — Apple replaced Ghostscript. So the model we port is the
  **Linux/foomatic** one, not what's on this Mac.
- Project lives at `~/catprintlabs/pdfprint`, its own git repo.

## 6. Architecture (as built)

```
cmd/pdfprint      CLI: flags → pipeline.Config
internal/ppd      PPD parser (quoted multi-line values, OpenUI/CloseUI options,
                  defaults, Foomatic fields)
internal/gs       Ghostscript command builder + device inference
internal/spool    winspool RAW writer (//go:build windows) + file fallback (!windows)
internal/pipeline orchestration: parse → build → run gs → stream to sink
```

- **Device inference order**: explicit `--device` → `-sDEVICE=` in the PPD's
  Foomatic command line → `cupsFilter`/model-name heuristics → error asking for
  `--device`.
- **Cross-platform build trick**: the spooler syscalls are behind
  `//go:build windows`; a `!windows` stub returns a helpful error. So the whole
  thing builds and *tests* on the Mac, and `--output <file>` captures the raw
  stream for inspection there.
- **Copies/duplex/page-size**: emitted as native PostScript `setpagedevice`
  (`/NumCopies`, `/Duplex`+`/Tumble`, and the PPD PageSize choice's own code).

## 7. What was verified (first slice)

On the Mac, end-to-end, real output — not mocks:

- PDF → **PCL-XL color** (`pxlcolor`): output carries the real PJL header
  `%-12345X@PJL SET RENDERMODE=COLOR` and the `) HP-PCL XL;…` binary signature.
- `--color mono` + `pxlmono` → `RENDERMODE=GRAYSCALE`.
- `--device ps2write` → `%!PS-Adobe-3.0` PostScript.
- Device **inferred from the PPD** (pxlcolor) with no `--device`.
- `--copies 2` → `/NumCopies 2`; `--page-size A4` → A4 geometry; stdin (`-`).
- Error paths: no inferable device, and no output target — both give actionable
  messages.
- Parser unit tests pass; **`windows/amd64` .exe cross-compiles** (PE32+).

## 8. Deliberately deferred (see README "Next")

- Full Foomatic option substitution (`%A`-`%Z` group encoding +
  `*FoomaticRIPOptionSetting` code injection). We currently use device +
  standard `setpagedevice`, not the PPD's per-option PostScript snippets.
- Enumerating installed Windows printers / auto-locating the driver's PPD.
- `*UIConstraints` (rejecting incompatible option combos).
- Media source / input-tray selection.
- N-up, scaling, page ranges (the `pdftopdf` pre-filter stage).

## 8a. "No scaling" and exact media (added for real-printer testing)

Mitch's first real test: send an 8.5×14 (Legal) PDF to a Legal-only printer with
**no scaling**. Requirements this drove:

- **Guaranteed 1:1.** Default is `--scale none`: no fit, page placed at native
  size; over-sized content clips rather than shrinks.
- **Exact media, even without a PPD** (bare `--device` run against a real PCL
  printer), via a built-in size table plus the PPD's `*PaperDimension`.

**Bug found and fixed (important).** The first implementation set the size with
`-c "<</PageSize[612 1008]>>setpagedevice"` *after* `-dFIXEDMEDIA`. We caught —
by rasterizing the output to PNG and measuring — that it silently produced
**Letter (8.5×11), not Legal**: `-dFIXEDMEDIA` locks the media to gs's default
(Letter) at init, so the later `setpagedevice` was ignored. The fix is the only
reliable method: set the media at device init with
`-dDEVICEWIDTHPOINTS=<w> -dDEVICEHEIGHTPOINTS=<h> -dFIXEDMEDIA`. Now verified to
rasterize to exactly 5100×8400 px = 8.5×14" @ 600 dpi. Locked in by unit tests
in `internal/gs/gs_test.go`.

Lesson: trust rasterized pixel dimensions, not the PCL/PS header, when verifying
geometry.

Also added: Ghostscript auto-detection (the Windows installer usually skips
PATH) and `--list-printers` (winspool `EnumPrinters`) so the exact `--printer`
name is easy to get.

## 9. How to pick up from here

```sh
make        # build host binary       make windows  # pdfprint.exe
make test   # unit tests              make fixture  # regenerate testdata/hello.pdf
./pdfprint --ppd testdata/sample.ppd --dry-run testdata/hello.pdf   # see the gs command
```

The next meaningful increment is Foomatic option substitution in `internal/gs`
(so vendor PPDs' own option code is honored), then Windows printer/PPD discovery.

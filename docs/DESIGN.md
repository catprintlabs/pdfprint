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
| Licensing | **AGPL Ghostscript OK**; pdfprint itself **MIT** | gs is exec'd as a separate process (mere aggregation), so its AGPL doesn't reach pdfprint's code. Repo is public and the standalone zip bundles gs, so that gs copy ships with its AGPL license + source link. | Making pdfprint AGPL (needless copyleft on our own tool); a commercial gs license (unnecessary) |
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
  Foomatic command line → `cupsFilter`/model-name heuristics → default to
  `pxlcolor` (`pxlmono` when `--color mono`), since PCL-XL is the most widely
  supported laser language. (A PostScript-only printer needs `--device ps2write`.)
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

## 8b. Windows transport: raw TCP vs the spooler (WSD/V4 silently drop RAW)

The first Windows real-printer test (2026-07-01) exposed the deepest gotcha of
the whole project. `pdfprint --printer "<name>"` reported success, the job left
the queue — and **nothing printed**. Investigation:

- The printer's queue was a **WSD port** with a **V4 print driver** (how modern
  network printers install by default).
- **WSD ports and V4 drivers silently discard the RAW datatype.** They force jobs
  through the XPS/print-filter pipeline; raw PCL/PS is not passed to the device.
  The spooler still returns success and drains the queue — a silent failure.
- Proof it wasn't our bug: pausing the queue showed the full 166 KB spooled
  correctly (so `WritePrinter` worked), yet the device rendered nothing. Sending
  the *same bytes* to the device's raw TCP port (9100) printed perfectly.

We considered the fixes and rejected the setup-heavy ones:

| Option | Verdict | Why |
|---|---|---|
| Require users to hand-create a Standard TCP/IP (raw 9100) queue on a v3 driver | ❌ | Works (we proved it), but per-printer manual setup + a driver install — unacceptable for deployment (Mitch: "we can't be going adding new drivers"). |
| Auto-create/tear-down a transient raw queue | ❌ | Needs admin, installs a driver, mutates system state. |
| **Direct raw-TCP socket to port 9100 (AppSocket/JetDirect)** | ✅ **chosen** | Exactly what CUPS `socket://` / `lp -o raw` do. Zero OS setup: no port, queue, or driver. Bypasses the WSD/V4 pipeline entirely. |

**Decision: for network printers, talk to the device directly over TCP 9100; keep
the spooler only for local/USB queues.** Transport is chosen automatically
(`spool.ResolvePrinter`): a WSD or Standard-TCP/IP port ⇒ resolve the device IP
and use a socket; anything else ⇒ spooler. Overrides: `--host <ip>` (skip
discovery), `--transport socket|spooler`.

IP discovery is **registry-only** (no `golang.org/x/sys` dependency — hand-rolled
advapi32 reads, matching the existing winspool syscalls):
- Standard TCP/IP port → `…\Print\Monitors\Standard TCP/IP Port\Ports\<port>` →
  `HostName` (+ `PortNumber` when `Protocol`=RAW).
- WSD port → `…\Enum\SWD\PRINTENUM\*`, match `FriendlyName` = printer name, parse
  the host from `LocationInformation` (`http://IP:port/…`). These keys grant
  `BUILTIN\Users:ReadKey`, so discovery works non-elevated; the one ACL-restricted
  spot (the `Properties\{DEVPKEY}` subkey) is deliberately not read.

Graceful degradation: if discovery fails, `ResolvePrinter` returns an actionable
error telling the user to pass `--host` — never a silent dead end (the very
failure mode we were fixing).

Lesson (companion to 8a's "trust the pixels"): on Windows, **"the spooler
accepted it" does not mean "the printer got it."** Verify on paper, and treat
WSD/V4 + RAW as a known dead end.

## 8c. Self-documenting smoke tests (`stamp`)

Because identical test pages (the PS vs PCL paths of the same ruler) are
impossible to tell apart on paper, we added `cmd/stamp` + `internal/stamp`: it
overlays a timestamp, host, the print command, and notes onto every page via a
Ghostscript `EndPage` procedure (page geometry preserved — no scaling). The
printed sheet now documents exactly what produced it. `make print-test` and
`scripts/smoke-test.ps1` chain stamp → pdfprint into one command. This replaced
hand-made static labeled fixtures (deleted).

## 8d. Detecting the device instead of guessing it

Once auto-routing worked, the open question was *which gs device* to use when the
caller didn't say. A brief "default to `pxlcolor`" stopgap was rejected by
Catmando: **detect, report, and never silently guess** — a wrong PDL prints
garbage or nothing. So `internal/probe` asks the printer directly:

- **IPP** (TCP 631, primary): a hand-rolled Get-Printer-Attributes call reads
  `document-format-supported`, `color-supported`, `sides-supported`, and the
  model. Richest source; 631 is open on the test fleet.
- **SNMP** (UDP 161, fallback): walks the Printer MIB's
  `prtInterpreterLangDescription` column plus `sysDescr`.

Both are hand-rolled (no `golang.org/x/sys`, no SNMP/IPP lib) — same ethos as the
registry/spooler syscalls. Mapping (`Caps.SuggestDevice`): PCL advertised →
`pxlcolor`/`pxlmono` (prefer native PCL-XL; generic `application/vnd.hp-PCL`
counts as PCL-XL, since PCL5-only IPP printers are extinct), else PostScript →
`ps2write`, explicit-PCL5-only → `ljet4`. Resolution order: `--device` → PPD →
probe → **error** (never assume). Verified against the real C620 over IPP (reads
its full capability set and picks `pxlcolor`); inspect with `--probe`.

Two smaller companions landed with it:
- **Verbosity**: `--quiet`/`-q` (errors only), normal (progress + detection), `-v`
  (gs command, gs path, PPD, probe detail). `--dry-run` shows the resolved device
  + command + transport with no side effects.
- **Partial printer names**: `--printer` accepts any substring that *uniquely*
  identifies one installed printer (exact match wins); ambiguous or absent
  matches error with the candidates. So `--printer "(FC:82:A2)"` selects one of
  five identically-named C620s.

## 9. How to pick up from here

```sh
make        # build host binary       make windows  # pdfprint.exe
make test   # unit tests              make fixture  # regenerate testdata/hello.pdf
./pdfprint --ppd testdata/sample.ppd --dry-run testdata/hello.pdf   # see the gs command
```

The next meaningful increment is Foomatic option substitution in `internal/gs`
(so vendor PPDs' own option code is honored), then Windows printer/PPD discovery.

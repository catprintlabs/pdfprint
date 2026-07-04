# CLAUDE.md — project state & continuation notes

Durable, in-repo context for Claude (and humans). This travels with the repo, so
whoever pulls it — including on a different machine/OS — starts with the same
picture. See `docs/DESIGN.md` for the *why* (architecture & decision log); this
file is the *where we are & what's next*.

## What this tool is (one line)
`pdfprint` gives Windows the PDF print path it lacks: PDF → Ghostscript → the
printer's native language (PCL-XL / PostScript) → the device (raw TCP for network
printers, RAW spooler for local/USB), at 1:1 with no scaling. macOS/Linux is the
dev/test host; Windows is the real target.

## Verified so far (as of 2026-07-01, on macOS)
End-to-end pipeline proven on a real **Xerox VersaLink C620** (Legal loaded),
printing via `pdfprint --output - | lp -o raw` (raw bypasses the CUPS driver):

| Fixture | PostScript (`ps2write`) | PCL-XL |
|---------|-------------------------|--------|
| `testdata/legal_ruler.pdf` (vector no-scaling ruler) | ✅ 171 KB | ✅ `pxlmono` 19 KB |
| real color imposition ticket (~12 MB, see note) | ✅ 6.6 MB | ✅ `pxlcolor` 11.7 MB |

> The color/raster path was first verified with a real production ticket that was
> **removed from the repo and its history for containing customer PII** (a repo
> delete+recreate purged the leaked LFS object). The committed fixture is now the
> **sanitized** `testdata/W260701_1546917_ticket.pdf` (customer "Internal Proof",
> no personal data) — the size figures above are from the original run.

- **1:1 confirmed**: on the ruler print, tick N measures exactly N inches from
  the center crosshair; PCL-XL and PostScript output were visually identical.
- Size inversion worth remembering: PCL-XL is far *smaller* than PS for vector
  pages (ruler) but *larger* for rasterized image pages (the ticket).

### Windows (2026-07-02) — the real target, verified across a fleet ✅
The Windows path (the whole reason this tool exists) is proven end-to-end on
**three different Windows 11 machines** against **two different printer brands**,
including one printer driven via **PCL5e (`ljet4`)**. So all three language paths
are now exercised on real hardware, not just PCL-XL/PostScript:
`ps2write` (PostScript), `pxlcolor`/`pxlmono` (PCL-XL / PCL 6), **and `ljet4`
(PCL5e)**. `pdfprint --printer "<name>"` prints `legal_ruler.pdf` at 1:1 via each
printer's **existing WSD queue** — the tool auto-discovers the device IP and
streams over raw TCP. Output matched across machines and matched the macOS runs
in size (PS ~166 KB, PCL-XL ~19 KB), confirmed on paper.

First proven on the Xerox VersaLink C620 (details below); the three-box /
two-brand run extended that to a second printer brand and the PCL5e path.
**TODO (fill in when convenient):** record the second printer's exact brand/model
(the one using `ljet4`/PCL5e) and the two additional Win11 boxes — useful for the
next machine to reproduce.

Dev box for Windows testing: Go 1.26.x and Ghostscript 10.07.1 installed (gs at
`C:\Program Files\gs\gs10.07.1\bin\gswin64c.exe`, auto-detected). The C620 fleet
is WSD; the unit tested is "Xerox VersaLink C620 (FC:90:A7)" at **10.0.1.151**
(raw port 9100 open).

## Test fixtures (`testdata/`, PDFs via Git LFS)
- `legal_ruler.pdf` / `legal_ruler.ps` — the **no-scaling test page**. 8.5×14"
  Legal (612×1008 pt), 1-inch ticks labeled in inches from center, edge frames
  at 0"/0.25"/0.5", center crosshair. `.ps` is the source; `.pdf` is what we print.
- `letter_ruler.pdf` / `letter_ruler.ps` — the **Letter** (8.5×11", 612×792 pt)
  variant of the ruler, same structure as the Legal one.
- `W260701_1546917_ticket.pdf` — a **sanitized** imposition ticket (612×1008
  Legal, ImageMagick raster, ~12 MB; customer "Internal Proof", no PII). The
  real-workload color fixture; replaced an earlier PII-bearing ticket.
- `hello.pdf` — small Letter fixture (gitignored; regenerate with `make fixture`).

## How to run the smoke test
**macOS/Linux** (this host): raw stream to stdout, pipe into CUPS.
```sh
pdfprint --scale none --device ps2write  --page-size Legal --output - <pdf> | lp -d <queue> -o raw
pdfprint --scale none --device pxlcolor --page-size Legal --output - <pdf> | lp -d <queue> -o raw
```
Find the queue with `lpstat -e`; confirm delivery with `lpstat -o <queue>` (empty = sent).
The C620 queue used for testing was `Xerox_VersaLink_C620__FC_90_A7_` (Mac only;
Windows uses the printer's display name instead).

**Self-documenting smoke test** (`cmd/stamp` + `internal/stamp`): `stamp` overlays
a timestamp, host, the print command, and notes onto a PDF via gs, so the printed
page records what produced it (and disambiguates identical test pages — this is
why the old static `legal_ruler_PS/PCL.pdf` variants were deleted). One-liners:
```bat
:: Windows (no make): stamps then prints, auto-routing the WSD queue to raw TCP
powershell -File scripts\smoke-test.ps1 -Printer "Xerox VersaLink C620 (FC:90:A7)"
powershell -File scripts\smoke-test.ps1 -HostIp 10.0.1.151
```
```sh
make print-test HOST=10.0.1.151          # Unix host (raw TCP is cross-platform)
make print-test PRINTER="<name>"         # (Windows-with-make)
```

## Transport: how bytes reach the printer (the big Windows lesson)
Getting gs output *to the device* is the Windows-specific, non-obvious part.
`pdfprint` chooses the transport automatically (`--transport auto`, default) from
the named printer — implemented in `spool.ResolvePrinter` (`internal/spool`):

- **Network printer → direct raw TCP, port 9100** (AppSocket/JetDirect). Opens a
  socket and streams PCL/PS — the analog of CUPS `socket://` / `lp -o raw`.
  **No OS setup: no port, no queue, no driver.** IP is discovered from the queue
  you already have: a Standard TCP/IP port's `HostName` (registry), or a **WSD**
  queue's PnP `LocationInformation` (registry: `…\Enum\SWD\PRINTENUM\*`, matched
  by `FriendlyName`, IP parsed from the `http://IP:port/…` URL).
- **Local/USB → Windows RAW spooler** (`WritePrinter`, `RAW` datatype).

**DON'T REGRESS THIS — why the spooler isn't used for network printers:**
spooling the RAW datatype to a **WSD port** or a **V4 driver** *silently fails* —
the spooler reports success, the job drains from the queue, and the device prints
**nothing** (WSD/V4 force jobs through the XPS/print-filter pipeline, which
discards raw PCL/PS). Proven cleanly: a *paused* queue held the full 166 KB (so
`pdfprint` wrote it correctly), yet nothing rendered; the identical bytes sent to
the device's raw 9100 socket printed perfectly. Most network printers install as
WSD by default, so socket-9100 is the right default for them.

Overrides: `--host <ip>` (+ `--port`, default 9100) dials directly, no discovery,
any OS; `--transport socket|spooler` forces a path. `--dry-run` prints the chosen
transport without printing anything (safe, no dial/spool):
```
output: printer "Xerox VersaLink C620 (FC:90:A7)" via socket 10.0.1.151:9100
        (WSD port "WSD-..." -> raw TCP 10.0.1.151:9100)
```
Discovery note: the registry keys read are `Users:ReadKey` (verified), so it works
**non-elevated**. If a read ever fails, `ResolvePrinter` returns an actionable
error pointing at `--host` (no silent dead end). We deliberately avoided a
`golang.org/x/sys` dep — registry reads are hand-rolled advapi32 syscalls in
`spool_windows.go`, matching the existing hand-rolled winspool code.

## Device auto-detection (don't guess the PDL)
Choosing the gs device (`internal/probe`, added 2026-07-02). Order: explicit
`--device` → PPD (`InferDevice`) → **probe the printer** → **refuse** (never guess
— a wrong PDL prints garbage/nothing; this reversed an earlier "default to
pxlcolor" stopgap).
- **IPP** (TCP 631, primary): hand-rolled Get-Printer-Attributes →
  `document-format-supported`, `color-supported`, `sides-supported`, model.
  **SNMP** (UDP 161, fallback): walks `prtInterpreterLangDescription`
  (`.1.3.6.1.2.1.43.15.1.1.5`) + sysDescr. Both hand-rolled, no deps.
- **Mapping** (`Caps.SuggestDevice`): PCL advertised → `pxlcolor`/`pxlmono`
  (prefer native PCL-XL; generic `vnd.hp-PCL` counts as PCL-XL — PCL5-only IPP
  printers are extinct); else PostScript → `ps2write`; explicit PCL5-only →
  `ljet4`. `--color` overrides color/mono.
- Always **reports** what it detected + chose; on failure, errors telling the
  user to pass `--device`. Inspect with `pdfprint --probe --host <ip>` (or
  `--printer <name>`). Verified on the real C620 via IPP (picks `pxlcolor`).

## Paper size & the media check
- `--page-size` takes a keyword (Letter/Legal/A4/A3/Tabloid/Ledger/Executive/
  Statement or a PPD name) **or exact dimensions**: `8.5x11in`, `216x279mm`,
  `21x29.7cm`, or bare points `612x792` (`gs.parseCustomSize`). No `--page-size`
  → gs uses the PDF's own MediaBox (still 1:1), and `pipeline.readPDFSize` reads
  that MediaBox (best-effort regex, cleartext) just to report/compare it.
- The resolved job size is always printed (`page size: 8.5x14 in (612x1008 pt)`,
  with `, from PDF` when read from the file), and the
  IPP probe reads the printer's **loaded** media (`media-ready`). On mismatch we
  print a **non-fatal WARNING** ("printer has X loaded, but this job is Y — load
  matching paper…") — Catmando's call: warn, never fail (loading paper is a
  physical step). The warning bypasses `--quiet` (it affects the physical output).
  `probe.MediaLoaded` compares within ~5pt, orientation-independent.

## Trays & loaded paper (per-tray reporting, added 2026-07-04)
`Caps.Trays []Tray{Source,Size,Type}` reports each input tray and the paper in it.
Two device-side sources (a user asked for this; only the printer knows it):
- **IPP** `media-col-ready` — a *collection* attribute (nested: `media-size`→
  `x/y-dimension` in **hundredths of a mm**, plus `media-source`/`media-type`).
  Required a real IPP collection parser (`parseCollection` in `ipp.go`, tags
  begCollection 0x34 / endCollection 0x37 / memberName 0x4A). `media-source-
  supported` fills in advertised-but-empty trays. `dimsToLabel` maps dims → size
  name (Letter/Legal/A4…), else `WxHmm`.
- **SNMP** fallback — walks Printer-MIB `prtInputTable`: `prtInputDescription`
  (.43.8.2.1.18) + `prtInputMediaName` (.12), joined by row index (`snmpTrays`).
- **Surfaced two ways:** `--probe` prints a `trays:` block for one printer;
  **`--list-trays`** lists all printers with their trays. `--list-trays` probes
  each over the network (3s timeout), skips USB/local/unreachable — deliberately
  NOT folded into the fast, local `--list-printers`, and NOT gated behind `-v`
  (which means "verbose logging", not "do network I/O"). Chose `--list-trays`
  over `--list-printers -v` / `--list-printers-verbose` for exactly that reason.
- Parser is unit-tested with synthetic IPP bytes (`TestParseIPPTrays`,
  `TestDimsToLabel`, `TestPrettySource`).
- **Real-printer check (2026-07-04, partial ✅):** validated over IPP against a
  live **Brother HL-3170CDW** (home LAN, `--probe --host <ip>`). It parsed the
  real IPP response cleanly and reported **two trays** — `Manual` and `Tray 1`
  (from `media-source-supported`; IPP found the manual feeder that SNMP's
  `prtInputTable` missed — SNMP showed only `TRAY1`). Both sizes came back
  "empty / not reported" because this printer genuinely doesn't report loaded
  media (SNMP `prtInputMediaName` = "Unknown"). Also a real finding: the
  HL-3170CDW advertises **only Apple/PWG Raster over IPP — no PCL/PS** — so the
  probe correctly hit "no targetable device" (refuse-rather-than-guess worked).
- **Still to verify:** (a) an actual **loaded-size label** populated from
  `media-col-ready` (needs a printer that reports it — the **Xerox C620** is the
  target, was asleep during the check); (b) the **SNMP tray path end-to-end**
  (IPP answered first on the Brother, so `snmpTrays` wasn't exercised live —
  though `snmpwalk` confirmed the exact OIDs return the expected rows);
  (c) `--list-trays` on Windows (macOS is IPP `--host` only).

## Verbosity
`--quiet`/`-q` (errors only) · normal (progress + detection summary) · `-v`
(adds gs command, gs path, PPD, probe detail). `--dry-run` shows the resolved
device + gs command + transport with no side effects (the probe is a read).

## Partial printer names
`--printer` accepts any substring that **uniquely** identifies one installed
printer (exact name wins over substrings); ambiguous/absent → error listing the
candidates. E.g. `--printer "(FC:82:A2)"` picks one of five identically-named
C620s. Implemented in `spool.matchName` (pure/tested) via `spool.MatchPrinter`,
applied in `pipeline.Run` and `--probe`. The 5 C620s are distinct units on
distinct IPs (FC:90:A7=.151, FC:82:A2=.153, …), each auto-discovered.

## How to run on Windows (fresh machine)
1. **Install Git LFS _before_ cloning** (or `git lfs install && git lfs pull`
   after) — else `testdata/*.pdf` come down as pointer files. `winget install GitHub.GitLFS`.
2. **Install Ghostscript (64-bit)** — auto-detected at `C:\Program Files\gs\gs*\bin\gswin64c.exe`.
   winget has no official Artifex pkg; grab the installer from the Artifex GitHub
   releases (`ghostpdl-downloads`) and run `/S` (silent), or from ghostscript.com.
3. **Install Go 1.22+** (`winget install GoLang.Go`). Build isn't required to run —
   `go run ./cmd/pdfprint …` works; `go build -o pdfprint.exe ./cmd/pdfprint` for a binary.
4. **Print** using the printer name you already have (no setup):
   ```bat
   pdfprint.exe --scale none --device pxlmono --page-size Legal --printer "<name>" testdata\legal_ruler.pdf
   ```
   `--list-printers` shows exact names; `--dry-run` shows the resolved transport first.

## No-scaling: how it's enforced (don't regress this)
gs args: `-dDEVICEWIDTHPOINTS/-dDEVICEHEIGHTPOINTS` (exact media in points) +
`-dFIXEDMEDIA` (lock it) + `-dPDFFitPage=false` (place 1:1, oversized clips
rather than shrinks). A `-c setpagedevice` *after* `-dFIXEDMEDIA` is ignored, so
media must be set at device-init via those flags. **This must always be set** —
without it gs falls back to its Letter default for the device, so a Legal PDF
prints as Letter and the printer waits for the wrong paper. Precedence:
`--page-size` (keyword or `WxH`) → the PDF's own MediaBox (`readPDFSize`, auto) →
PPD default. So a Legal PDF now auto-detects as Legal; you no longer need to pass
`--page-size Legal` manually (though it still works, and overrides).

## Tests: what's automated vs. manual
- **Automated** (`make test`, no external deps): unit tests over PPD parsing
  (`internal/ppd`), gs *command construction* (`internal/gs`), and the stamp
  prolog/PS-escaping (`internal/stamp`) — they assert the right args/output are
  built (fixed media, no-scaling, device inference, EndPage overlay, etc.).
- **NOT yet automated**: actually running gs on a PDF and asserting the rendered
  output is exactly 612×1008 with valid PCL/PS magic bytes. This end-to-end
  regression guard is the main test gap — proposed as a `make test-integration`
  target gated on gs being present. The physical print is unavoidably manual.

## Distribution (Electron app + CI releases)
`pdfprint` is a **helper binary bundled inside a larger Electron app**, not
installed standalone. Decisions made 2026-07-02:
- **Ghostscript ships as a sibling.** Only 2 files needed (`gswin64c.exe` +
  `gsdll64.dll`, ~24 MB — resources are ROM'd into the DLL). `scripts/vendor-gs.ps1`
  assembles them into `vendor/` (gitignored). `gs.FindBinary` prefers a gs next to
  the exe (`<exedir>\gs\gswin64c.exe`) over the system one; `--gs` overrides all.
- **Electron packaging** puts `pdfprint.exe` + `stamp.exe` + `gs/` in
  `extraResources`; the main process spawns them from `process.resourcesPath`
  (passing `--gs` or relying on sibling detection). See README "Distribution".
- **Binaries are NOT committed.** `.github/workflows/release.yml` builds the
  Windows exes on a `v*` tag (pure-Go cross-compile from Linux, gated on
  `go test`) and uploads them as **GitHub Release assets** via the `gh` CLI. The
  Electron app pulls those assets at package time; `electron-builder` publishes the
  app and `electron-updater` auto-updates clients. (We briefly committed exes via
  LFS, then reverted to this source-only + Releases model.)
- `scripts/build.ps1` is for local Windows dev builds only.

## Open threads / next steps
- (Proposed) integration test: run `pdfprint --output` on `legal_ruler.pdf`,
  rasterize, assert 612×1008 + PCL/PS magic — turns today's manual check into CI.
- ✅ Windows real-printer verification (done — see "Verified so far / Windows").
- ✅ `make print-test` + `scripts/smoke-test.ps1` convenience targets (done).
- ✅ Transport auto-routing (WSD/V4 → raw TCP) with IP discovery (done).
- ⏳ Per-tray / loaded-paper reporting (`--probe` trays block + `--list-trays`,
  added 2026-07-04; see "Trays & loaded paper"). **IPP tray parsing validated on a
  real Brother HL-3170CDW** (2026-07-04). Remaining: a printer that reports a
  loaded *size* (Xerox C620), the SNMP tray path end-to-end, and `--list-trays`
  on Windows.
- PII-free dummy imposition ticket fixture (still pending — the color/raster path
  was verified once on a real ticket that was removed for PII).
- Longer-term (see `docs/DESIGN.md` "Not yet done"): full Foomatic option
  substitution, auto-locating a printer's PPD, UIConstraints, tray selection, N-up.
- Possible polish: LPR (515) fallback if a device has 9100 closed; IPv6 hosts in
  discovery; a `--transport` value to prefer spooler-over-socket for TCP/IP queues.

## Housekeeping
- PDF fixtures are Git LFS (`testdata/*.pdf`, see `.gitattributes`). The Windows
  `.exe`s are NOT committed — they build in CI and ship as GitHub Release assets
  (`.github/workflows/release.yml`); `.gitignore` ignores them.
- Regenerable outputs are gitignored: `testdata/*.out.*`, `testdata/*.proof.png`.
- **Experimental Crystal port** on branch `crystal-port` (not merged). A learning
  reimplementation under `crystal/`: Phase 1 done (PPD parser + gs command builder,
  `crystal spec` green, byte-identical gs command to the Go tool); Phases 2 (CLI)
  & 3 (winspool.drv FFI) pending. The Go tool on `main` is unaffected.

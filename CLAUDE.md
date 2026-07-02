# CLAUDE.md — project state & continuation notes

Durable, in-repo context for Claude (and humans). This travels with the repo, so
whoever pulls it — including on a different machine/OS — starts with the same
picture. See `docs/DESIGN.md` for the *why* (architecture & decision log); this
file is the *where we are & what's next*.

## Status flash (2026-07-02)
Windows real-printer testing/debugging is **in progress on a Windows PC and
reported "almost ready"** by Mitch. That work was **not yet pushed** as of this
note — it lives on the PC. **Next session: `git fetch` first**; the PC changes
(the `--printer` RAW-spooler path finally exercised on real hardware) should be
committed and pushed *from the PC*. If they aren't on `origin/main` yet, they're
still only on that machine. Everything below reflects the last macOS-side state.

## What this tool is (one line)
`pdfprint` gives Windows the PDF print path it lacks: PDF → Ghostscript → the
printer's native language (PCL-XL / PostScript) → RAW spooler, at 1:1 with no
scaling. macOS/Linux is the dev/test host; Windows is the real target.

## Verified so far (as of 2026-07-01, on macOS)
End-to-end pipeline proven on a real **Xerox VersaLink C620** (Legal loaded),
printing via `pdfprint --output - | lp -o raw` (raw bypasses the CUPS driver):

| Fixture | PostScript (`ps2write`) | PCL-XL |
|---------|-------------------------|--------|
| `testdata/legal_ruler.pdf` (vector no-scaling ruler) | ✅ 171 KB | ✅ `pxlmono` 19 KB |
| color imposition ticket (~12 MB, see note) | ✅ 6.6 MB | ✅ `pxlcolor` 11.7 MB |

> The color/raster path was first verified with a real production ticket that was
> **removed from the repo and its history for containing customer PII** (a
> repo-delete+recreate purged the leaked LFS object). The committed fixture is now
> the **sanitized** `testdata/W260701_1546917_ticket.pdf` (customer "Internal
> Proof", no personal data) — the size figures above are from the original run.

- **1:1 confirmed**: on the ruler print, tick N measures exactly N inches from
  the center crosshair; PCL-XL and PostScript output were visually identical.
- Size inversion worth remembering: PCL-XL is far *smaller* than PS for vector
  pages (ruler) but *larger* for rasterized image pages (the ticket).

## Test fixtures (`testdata/`, PDFs via Git LFS)
- `legal_ruler.pdf` / `legal_ruler.ps` — the **no-scaling test page**. 8.5×14"
  Legal (612×1008 pt), 1-inch ticks labeled in inches from center, edge frames
  at 0"/0.25"/0.5", center crosshair. `.ps` is the source; `.pdf` is what we print.
- `W260701_1546917_ticket.pdf` — a **sanitized** imposition ticket (612×1008
  Legal, ImageMagick raster, ~12 MB; customer "Internal Proof", no PII). The
  real-workload color fixture. Replaced an earlier PII-bearing ticket.
- `hello.pdf` — small Letter fixture (gitignored; regenerate with `make fixture`).

## How to run the smoke test
**macOS/Linux** (this host): raw stream to stdout, pipe into CUPS.
```sh
pdfprint --scale none --device ps2write  --page-size Legal --output - <pdf> | lp -d <queue> -o raw
pdfprint --scale none --device pxlcolor --page-size Legal --output - <pdf> | lp -d <queue> -o raw
```
Find the queue with `lpstat -e`; confirm delivery with `lpstat -o <queue>` (empty = sent).
The C620 queue used for testing was `Xerox_VersaLink_C620__FC_90_A7_` (Mac only;
the Windows spooler uses the printer's display name instead).

## >>> Continuing on Windows (the next milestone) <<<
The whole point of the tool is the **Windows RAW spooler path** (`--printer`),
which cannot be exercised on macOS. On the PC:

1. **Install Git LFS _before_ cloning** (or `git lfs install && git lfs pull`
   after) — else the `testdata/*.pdf` fixtures come down as pointer files, not
   PDFs. Git for Windows bundles git-lfs; else `winget install GitHub.GitLFS`.
2. **Install Ghostscript (64-bit)** from https://ghostscript.com/releases/ —
   `pdfprint` auto-detects `C:\Program Files\gs\gs*\bin\gswin64c.exe`.
3. **Build**: `go build -o pdfprint.exe ./cmd/pdfprint` (or `make windows` from a
   Unix host cross-compiles `pdfprint.exe`). Needs Go 1.22+.
4. **Get the exact printer name**: `pdfprint.exe --list-printers`.
5. **Print** the same fixtures 1:1 and compare to the macOS output:
   ```bat
   pdfprint.exe --scale none --device pxlmono  --page-size Legal --printer "<exact name>" testdata\legal_ruler.pdf
   pdfprint.exe --scale none --device pxlcolor --page-size Legal --printer "<exact name>" testdata\W260701_1546917_ticket.pdf
   ```
6. **Watch for**: the RAW spooler path actually reaching the printer; output
   matching the macOS prints; the no-scaling guarantee holding (ruler ticks =
   exactly 1"). Add `--dry-run` first to inspect the gs command.

## No-scaling: how it's enforced (don't regress this)
gs args: `-dDEVICEWIDTHPOINTS/-dDEVICEHEIGHTPOINTS` (exact media in points) +
`-dFIXEDMEDIA` (lock it) + `-dPDFFitPage=false` (place 1:1, oversized clips
rather than shrinks). A `-c setpagedevice` *after* `-dFIXEDMEDIA` is ignored, so
media must be set at device-init via those flags. **Gotcha:** the `--ppd` path
defaults media to the PPD's default (often Letter, 792 pt height) and would clip
a Legal page — always pass `--page-size Legal` (works with or without a PPD).

## Tests: what's automated vs. manual
- **Automated** (`make test`, no external deps): unit tests over PPD parsing
  (`internal/ppd`) and gs *command construction* (`internal/gs`) — they assert
  the right gs args are built (fixed media, no-scaling, device inference, etc.).
- **NOT yet automated**: actually running gs on a PDF and asserting the rendered
  output is exactly 612×1008 with valid PCL/PS magic bytes. This end-to-end
  regression guard is the main test gap — proposed as a `make test-integration`
  target gated on gs being present. The physical print is unavoidably manual.

## Open threads / next steps
- (Proposed) integration test: run `pdfprint --output` on `legal_ruler.pdf`,
  rasterize, assert 612×1008 + PCL/PS magic — turns today's manual check into CI.
- (Proposed) `make print-test PRINTER=<queue>` convenience target.
- Windows real-printer verification (section above).
- **Experimental Crystal port** on branch `crystal-port` (not merged). A learning
  reimplementation under `crystal/`. Phase 1 done: PPD parser + gs command builder
  ported, `crystal spec` green (11 examples), emits a byte-identical gs command to
  the Go tool. Phases 2 (Process/CLI) & 3 (winspool.drv FFI via `lib`/`fun`)
  pending — see `crystal/README.md` on that branch. Needs Crystal 1.20+ (`brew
  install crystal`). The Go tool on `main` is unaffected.
- Longer-term (see `docs/DESIGN.md` "Not yet done"): full Foomatic option
  substitution, auto-locating a printer's PPD, UIConstraints, tray selection, N-up.

## Housekeeping
- PDF fixtures are Git LFS (`testdata/*.pdf`, see `.gitattributes`).
- Regenerable outputs are gitignored: `testdata/*.out.*`, `testdata/*.proof.png`.

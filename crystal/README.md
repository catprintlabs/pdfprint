# pdfprint — Crystal port (work in progress)

A Crystal reimplementation of the Go `pdfprint` tool in the repo root, done as a
learning exercise. Same architecture: parse a PPD → build a Ghostscript command
→ run gs → stream the printer's native PCL/PS to output (a file/stdout now; the
Windows RAW spooler later).

Why this is a good fit: the Go code has **zero external dependencies** — it's
stdlib + shelling out to Ghostscript + one file of Win32 FFI. So the port needs
almost no libraries; Crystal's Ruby-like syntax makes the PPD/string parsing
read nicely, and its `lib`/`fun` C bindings will handle the Win32 spooler calls.

## Status

**Phase 1 — pure logic (done, runs & tested on macOS):**
- `src/pdfprint/ppd.cr` — PPD parser (port of `internal/ppd`)
- `src/pdfprint/gs.cr` — Ghostscript command builder (port of `internal/gs`)
- `spec/` — ports of the Go tests (`crystal spec`, 11 examples)

Verified the command builder emits a **byte-for-byte identical** gs command to
the Go tool for the Legal no-scaling case.

**Phase 2 — I/O & CLI (next):**
- `find` (locate the gs binary; Windows install-dir globbing under `flag?(:windows)`)
- pipeline (spawn gs via `Process`, stream stdout → sink, stderr → log)
- CLI via `OptionParser`
- `--output`/stdout so it can drive `lp -o raw` on macOS, as the Go version does

**Phase 3 — Windows spooler FFI:**
- `lib LibWinspool` bindings to `winspool.drv`
  (`OpenPrinterW`/`StartDocPrinterW`/`WritePrinter`/…), `String#to_utf16` for
  the wide-string args. Needs a Windows box to test.

## Build & test

Requires [Crystal](https://crystal-lang.org) (1.20+) and, at runtime,
Ghostscript.

```sh
crystal spec       # run the ported tests
```

## The one real tradeoff vs. Go

Cross-compiling to a ready-to-run Windows `.exe` from macOS is not one command
in Crystal (it's a two-step: `--cross-compile` to an object file on macOS, then
link on Windows with MSVC). Crystal's Windows support is also still "Preview" —
but the main gap there is the concurrency/event-loop, which this synchronous
tool doesn't use. Plan: build on the Windows box directly.

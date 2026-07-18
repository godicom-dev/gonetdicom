# gonetdicom TODO

Working notes for *gonetdicom*. The public overview lives in [README.md](README.md);
this file tracks deferred work.

## Status

DIMSE (association, C-ECHO / C-STORE / C-FIND / C-MOVE / C-GET / C-CANCEL,
DIMSE-N, role selection, user identity, storage commitment) and a DICOMweb MVP
(WADO-RS including rendered/bulkdata, STOW-RS, QIDO-RS) ship as of **v0.12.0**,
on [godicom](https://github.com/godicom-dev/godicom) `v0.24.0+`.

## Principles

- Prefer protocol behaviour and golden fixtures from the `pynetdicom/` submodule
  (and DICOM PS3.x). Do not reinvent DIMSE semantics.
- Call godicom for Dataset / pixel / JSON work. If godicom is blocking, fix
  godicom first — do not work around missing APIs here.
- Prefer named constants (`status`, godicom `tag` / `uid`) over bare hex.
- Where behaviour is equivalent, use Go strengths (goroutines, fan-out
  associations, streaming I/O) instead of mirroring a single-threaded pace.

## Deferred (need a concrete use case)

- Full SCP framework parity with pynetdicom
- DICOM WebSockets
- HTJ2K / JPEG encode for Accept renegotiation (blocked on JPEG encode upstream)
- Study / series multipart rendered; richer QIDO fuzzy matching
- Production origin-server auth and persistence backends
- Broader multi-PACS soak matrix in CI

## See also

- [README.md](README.md)
- [CHANGELOG.md](CHANGELOG.md)

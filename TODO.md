# gonetdicom TODO

Working notes for *gonetdicom*. The public overview lives in [README.md](README.md);
this file tracks deferred work.

## Status

v0.22.0, on [godicom](https://github.com/godicom-dev/godicom) `v0.40.0`:
DIMSE (association, C-ECHO / C-STORE / C-FIND / C-MOVE / C-GET / C-CANCEL,
DIMSE-N, role selection, user identity, asynchronous operations window, SOP
Class extended and common extended negotiation, storage commitment), streamed
inbound datasets and streamed handler results, admission control, the SOP Class
service class table with the statuses each may answer, Query/Retrieve identifier
validation, and DICOMweb (WADO-RS including frames, rendered at instance /
series / study level, and bulkdata, STOW-RS, QIDO-RS with the parameters about
the answer — `limit`, `offset`, `fuzzymatching`, `includefield` — the
Non-Patient Instance Service, and the top-level Search targets).

Where this stands against `pynetdicom/` is tracked in [PARITY.md](PARITY.md),
which lists the covered, partial and deliberately out-of-scope behaviour groups
rather than a raw test count.

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
- C-STORE transfer-syntax transcoding when the accepted presentation context
  differs from the stored dataset. godicom can encode JPEG and HTJ2K; this
  module does not transcode, and refuses the store instead of sending bytes
  under a syntax they are not in (`ae.ErrTransferSyntaxMismatch`). What is
  missing on this side is nothing — it is `godicom.Dataset.EncodeTo(io.Writer,
  uid.UID)`, which is the whole of the conversion and which godicom does not
  have yet. Working around it here would mean decoding and re-encoding pixels
  in this module, which Principles says not to do
- The association handle inside an SCP handler: a handler that wants to know who
  is asking, or to start a C-STORE sub-operation the request did not name, can
  only get that from the association, and handing the association to a handler
  hands it the `event.Set` state machine. This needs an ADR, not a signature
- Retrieval Capabilities (PS3.18 8.9), which no DICOMweb implementation in Go
  has and pynetdicom does not implement either
- Production origin-server auth and persistence backends
- A multi-PACS soak matrix in CI. `TestRetrieveSoak` soaks one association
  against this module's own SCP; it cannot tell us what a second archive's
  timing does to the same loop

## See also

- [README.md](README.md)
- [CHANGELOG.md](CHANGELOG.md)

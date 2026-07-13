# gonetdicom TODO

## Principles

- **DIMSE**: prefer pynetdicom source + tests under `pynetdicom/` (submodule).
- **DICOMweb**: PS3.18; reuse godicom `dicomjson` / `ReadFile` / `SaveAs` / `PixelBytes`.
- Do not re-implement Dataset/pixel logic here — call godicom.
- API shape: Go-idiomatic; no Python dynamic Association monkey-patching.

## Near term

1. ~~PDU / Association types + C-ECHO SCU roundtrip (local or mock)~~ ✅ Phase 1
2. C-STORE SCU (use godicom for dataset encode under negotiated TS)
3. Minimal SCP acceptor for Verification / C-STORE
4. Expand CI (coverage, golangci-lint) as packages grow

## Explicitly later

- Full SCP framework parity with pynetdicom
- TLS / DICOM WebSockets
- HTJ2K / JPEG encode for Accept renegotiation (blocked upstream on JPEG)

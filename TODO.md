# gonetdicom TODO

## Principles

- **DIMSE**: prefer pynetdicom source + tests under `pynetdicom/` (submodule).
- **DICOMweb**: PS3.18; reuse godicom `dicomjson` / `ReadFile` / `SaveAs` / `PixelBytes` / `Encode` / `DecodeDataset`.
- Do not re-implement Dataset/pixel logic here — call godicom.
- When godicom is blocking, fix godicom first.
- API shape: Go-idiomatic; no Python dynamic Association monkey-patching.

## Near term

1. ~~PDU / Association types + C-ECHO SCU roundtrip (local or mock)~~ ✅ Phase 1
2. ~~C-STORE SCU/SCP~~ ✅
3. ~~godicom Encode/Decode + StoreRequest.Data~~ ✅
4. ~~C-FIND SCU/SCP~~ ✅
5. ~~C-MOVE / C-GET SCU/SCP~~ ✅
6. ~~DICOMweb MVP (WADO/STOW/QIDO)~~ ✅
7. Expand CI (coverage, golangci-lint) as packages grow

## Explicitly later

- Full SCP framework parity with pynetdicom
- TLS / DICOM WebSockets
- HTJ2K / JPEG encode for Accept renegotiation (blocked upstream on JPEG)
- Richer QIDO fuzzy matching / WADO rendered / bulk data
- Production origin-server auth, persistence backends

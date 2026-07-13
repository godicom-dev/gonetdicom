# gonetdicom TODO

## Principles

- **DIMSE**: prefer pynetdicom source + tests under `pynetdicom/` (submodule).
- **DICOMweb**: PS3.18; reuse godicom `dicomjson` / `ReadFile` / `SaveAs` / `PixelBytes` / `Encode` / `DecodeDataset`.
- Do not re-implement Dataset/pixel logic here — call godicom.
- When godicom is blocking, fix godicom first.
- API shape: Go-idiomatic; no Python dynamic Association monkey-patching.
- **Behaviour + tests**: overall design and unit/golden tests strictly follow pynetdicom (and PS3.x), not reinvent protocol semantics.
- **Performance**: where behaviour is equivalent, exploit Go — goroutines, fan-out associations, streaming I/O, bounded worker pools — instead of mirroring Python's single-threaded pace.

## Near term

1. ~~PDU / Association types + C-ECHO SCU roundtrip (local or mock)~~ ✅ Phase 1
2. ~~C-STORE SCU/SCP~~ ✅
3. ~~godicom Encode/Decode + StoreRequest.Data~~ ✅
4. ~~C-FIND SCU/SCP~~ ✅
5. ~~C-MOVE / C-GET SCU/SCP~~ ✅
6. ~~DICOMweb MVP (WADO/STOW/QIDO)~~ ✅
7. ~~Expand CI (golangci-lint)~~ ✅
8. ~~Phase 4 harden (TLS helpers, structured logging, real-PACS soak stub)~~ ✅
9. ~~WADO-RS rendered (instance JPEG/PNG) + Pixel Data bulkdata~~ ✅
10. ~~v0.3.0 release~~ ✅
11. ~~SCP/SCU Role Selection (PDU 0x54, C-GET real-PACS prerequisite)~~ ✅
12. ~~C-CANCEL (FIND/MOVE/GET)~~ ✅
13. ~~Storage Commitment Push Model (N-ACTION / N-EVENT-REPORT)~~ ✅
14. ~~v0.4.0 release~~ ✅
15. ~~C-MOVE SCP → MoveDestination C-STORE sub-operations~~ ✅
16. ~~v0.5.0 release~~ ✅
17. ~~User Identity Negotiation~~ ✅
18. ~~v0.6.0 release~~ ✅
19. ~~Remaining DIMSE-N (N-GET/SET/CREATE/DELETE)~~ ✅
20. ~~v0.7.0 release~~ ✅
21. ~~Async new-association event report + parallel C-MOVE stores~~ ✅
22. ~~v0.8.0 release~~ ✅
23. ~~Study/series/instance metadata BulkDataURI~~ ✅

## Explicitly later

- Full SCP framework parity with pynetdicom
- DICOM WebSockets
- HTJ2K / JPEG encode for Accept renegotiation (blocked upstream on JPEG)
- Study/series multipart rendered; richer QIDO fuzzy matching
- Production origin-server auth, persistence backends
- Broader multi-PACS soak matrix in CI

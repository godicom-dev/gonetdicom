# gonetdicom

**Go DICOM networking library** — depends on [godicom](https://github.com/godicom-dev/godicom).

[![Go Version](https://img.shields.io/badge/Go-%3E%3D%201.26-%23007d9c)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

## Scope

| Library | Role |
|---------|------|
| **godicom** | pydicom port — files, Dataset, pixels, JSON |
| **gonetdicom** | Network layer — DIMSE (+ DICOMweb); **not** part of the pydicom port |

Behavioural reference for DIMSE: [pynetdicom](https://github.com/pydicom/pynetdicom) (git submodule `pynetdicom/`).  
DICOMweb follows DICOM PS3.18 (WADO-RS / QIDO-RS / STOW-RS).

```
gonetdicom
 └── github.com/godicom-dev/godicom
```

## Status

**Bootstrap** — module + submodule + dependency on godicom `v0.20.0`.  
No DIMSE/DICOMweb APIs yet; see roadmap below.

## Install

```bash
go get github.com/godicom-dev/gonetdicom@latest
```

Clone with submodule:

```bash
git clone --recurse-submodules https://github.com/godicom-dev/gonetdicom.git
```

## Roadmap (working plan)

### Phase 0 — Bootstrap ✅
- [x] `go.mod` + remote
- [x] `pynetdicom` submodule
- [x] depend on `godicom`

### Phase 1 — DIMSE foundation (pynetdicom-aligned)
- Association / AE title / presentation contexts
- PDU encode/decode
- C-ECHO SCU (smoke path)

### Phase 2 — Core DIMSE services
- C-STORE SCU/SCP
- C-FIND SCU (Patient/Study root)
- C-MOVE / C-GET as needed

### Phase 3 — DICOMweb MVP
- WADO-RS Retrieve Instance (`application/dicom`) + Metadata (`dicom+json`)
- STOW-RS Store
- Thin QIDO-RS

### Phase 4 — Harden
- Tests against pynetdicom fixtures / real PACS
- Timeouts, TLS, logging

## Layout

```
gonetdicom/
├── gonetdicom.go      # package docs
├── pynetdicom/        # submodule → pydicom/pynetdicom
├── go.mod
└── README.md
```

Packages will grow as Phase 1 lands (`ae`, `pdu`, `dimse`, later `dicomweb`).

## License

MIT — see [LICENSE](LICENSE).  
`pynetdicom/` retains its upstream license.

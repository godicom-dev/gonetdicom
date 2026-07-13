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

**Phase 1** — PDU encode/decode, Association SCU, C-ECHO SCU.

## Install

```bash
go get github.com/godicom-dev/gonetdicom@latest
```

Clone with submodule:

```bash
git clone --recurse-submodules https://github.com/godicom-dev/gonetdicom.git
```

## Quick start (C-ECHO SCU)

```go
package main

import (
	"context"
	"log"
	"time"

	"github.com/godicom-dev/gonetdicom/ae"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	assoc, err := ae.Dial(ctx, ae.Config{AETitle: "MYSCU"}, "pacs.example:11112", "ANY-SCP")
	if err != nil {
		log.Fatal(err)
	}
	defer assoc.Abort()

	if err := assoc.CEcho(ctx); err != nil {
		log.Fatal(err)
	}
	if err := assoc.Release(ctx); err != nil {
		log.Fatal(err)
	}
}
```

## Packages

| Package | Role |
|---------|------|
| `pdu` | A-ASSOCIATE / P-DATA-TF / A-RELEASE / A-ABORT |
| `dimse` | C-ECHO command sets (Implicit VR LE) |
| `ae` | Association SCU + `CEcho` |

## Roadmap (working plan)

### Phase 0 — Bootstrap ✅
- [x] `go.mod` + remote
- [x] `pynetdicom` submodule
- [x] depend on `godicom`

### Phase 1 — DIMSE foundation (pynetdicom-aligned) ✅
- [x] Association / AE title / presentation contexts
- [x] PDU encode/decode
- [x] C-ECHO SCU (smoke path)

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
├── ae/                # Association SCU
├── dimse/             # DIMSE command sets
├── pdu/               # Upper Layer PDUs
├── pynetdicom/        # submodule → pydicom/pynetdicom
├── go.mod
└── README.md
```

## License

MIT — see [LICENSE](LICENSE).

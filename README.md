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

**Phase 2 (partial)** — C-ECHO + C-STORE + C-FIND + C-MOVE/C-GET SCU/SCP; dataset encode/decode via godicom `v0.22.1+`.

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

## C-STORE SCU

Propose a storage presentation context, then send a godicom Dataset (or pre-encoded bytes):

```go
cfg := ae.Config{
	AETitle: "STORESCU",
	PresentationContexts: []ae.PresentationContext{{
		ID: 1,
		AbstractSyntax: "1.2.840.10008.5.1.4.1.1.7", // Secondary Capture
		TransferSyntaxes: []string{"1.2.840.10008.1.2"},
	}},
}
assoc, err := ae.Dial(ctx, cfg, "pacs.example:11112", "ANY-SCP")
// ...
ds := godicom.NewDataset()
ds.Set(godicom.NewDataElement(godicom.MustTag("SOPClassUID"), godicom.VRUI, "..."))
// ...
res, err := assoc.CStore(ctx, ae.StoreRequest{
	AffectedSOPClassUID:    "1.2.840.10008.5.1.4.1.1.7",
	AffectedSOPInstanceUID: "1.2.3.4.5",
	Data:                   ds, // encoded via godicom with negotiated TS
})
```

## C-FIND SCU

```go
cfg := ae.Config{
	AETitle: "FINDSCU",
	PresentationContexts: []ae.PresentationContext{{
		ID: 1,
		AbstractSyntax: ae.PatientRootQueryRetrieveInformationModelFind,
		TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
	}},
}
assoc, err := ae.Dial(ctx, cfg, "pacs.example:11112", "ANY-SCP")
query := godicom.NewDataset()
query.Set(godicom.NewDataElement(godicom.MustTag("QueryRetrieveLevel"), godicom.VRCS, "PATIENT"))
query.Set(godicom.NewDataElement(godicom.MustTag("PatientID"), godicom.VRLO, "*"))
matches, err := assoc.CFind(ctx, ae.FindRequest{
	QueryModel:     ae.PatientRootQueryRetrieveInformationModelFind,
	IdentifierData: query,
})
```

## C-MOVE / C-GET SCU

```go
// C-MOVE: peer stores to MoveDestination AE; SCU collects status responses.
matches, err := assoc.CMove(ctx, ae.MoveRequest{
	QueryModel:      ae.PatientRootQueryRetrieveInformationModelMove,
	MoveDestination: "STORESCP",
	IdentifierData:  query,
})

// C-GET: peer pushes C-STORE on the same association; handle via OnCStore.
matches, err := assoc.CGet(ctx, ae.GetRequest{
	QueryModel:     ae.PatientRootQueryRetrieveInformationModelGet,
	IdentifierData: query,
	OnCStore: func(_ context.Context, req ae.StoreRequest) uint16 {
		// persist req.Dataset
		return 0x0000
	},
})
```

Propose both the QR Get model and storage SOP Class presentation contexts for C-GET.

## C-STORE SCP

```go
ln, _ := net.Listen("tcp", ":11112")
_ = ae.Serve(ctx, ln, ae.ServerConfig{
	AETitle:                  "STORESCP",
	AcceptedAbstractSyntaxes: []string{"1.2.840.10008.5.1.4.1.1.7"},
	OnCStore: func(_ context.Context, req ae.StoreRequest) uint16 {
		// persist req.Dataset
		return 0x0000
	},
})
```

## Packages

| Package | Role |
|---------|------|
| `pdu` | A-ASSOCIATE / P-DATA-TF / A-RELEASE / A-ABORT + PDV fragmentation |
| `dimse` | C-ECHO / C-STORE / C-FIND / C-MOVE / C-GET command sets (Implicit VR LE) |
| `ae` | Association SCU (`CEcho`, `CStore`, `CFind`, `CMove`, `CGet`) + SCP (`Serve`) |

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
- [x] C-STORE SCU/SCP
- [x] godicom `EncodeDataset` / `DecodeDataset` integration
- [x] C-FIND SCU/SCP (Patient/Study root models)
- [x] C-MOVE / C-GET SCU/SCP (sub-op counts; C-GET interleaved C-STORE)

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
├── ae/                # Association SCU / SCP
├── dimse/             # DIMSE command sets
├── pdu/               # Upper Layer PDUs
├── pynetdicom/        # submodule → pydicom/pynetdicom
├── go.mod
└── README.md
```

## License

MIT — see [LICENSE](LICENSE).

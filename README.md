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

**Status:** `v0.4.0` — Phase 1–4 + Role Selection / C-CANCEL / Storage Commitment; depends on godicom `v0.23.0+`.

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
For real PACS, also propose SCP/SCU Role Selection so the SCU can receive C-STORE:

```go
cfg := ae.Config{
	AETitle: "GETSCU",
	PresentationContexts: []ae.PresentationContext{ /* Get model + CT Image Storage */ },
	RoleSelections: []pdu.RoleSelection{
		ae.BuildRole(string(uid.CTImageStorage), false, true), // requestor as SCP
	},
}
```

## Storage Commitment (N-ACTION / N-EVENT-REPORT)

```go
res, err := assoc.NAction(ctx, ae.ActionRequest{
	RequestedSOPClassUID:    ae.StorageCommitmentPushModelSOPClass,
	RequestedSOPInstanceUID: ae.StorageCommitmentPushModelSOPInstance,
	ActionTypeID:            dimse.StorageCommitmentActionTypeRequest,
	ActionInformationData:   info, // TransactionUID + ReferencedSOPSequence
	OnNEventReport: func(_ context.Context, req ae.EventReportRequest) uint16 {
		// handle commitment result (EventTypeID 1=success / 2=failures)
		return 0x0000
	},
})
```

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

## C-MOVE SCP (Move Destination C-STORE)

Configure `MoveDestinations` and return `MovePlan.Stores` from `OnCMove`. The SCP dials the destination AE and performs C-STORE sub-operations (pending/final C-MOVE-RSP derived from store outcomes):

```go
_ = ae.Serve(ctx, moveLn, ae.ServerConfig{
	AETitle: "MOVESCP",
	AcceptedAbstractSyntaxes: []string{
		ae.PatientRootQueryRetrieveInformationModelMove,
	},
	MoveDestinations: map[string]ae.MoveDestination{
		"STORESCP": {Addr: "127.0.0.1:11112"},
	},
	OnCMove: func(_ context.Context, req ae.MoveRequest) ae.MovePlan {
		return ae.MovePlan{
			Stores: []ae.StoreRequest{{
				AffectedSOPClassUID:    ctUID,
				AffectedSOPInstanceUID: sopUID,
				Data:                   ds,
			}},
		}
	},
})
```

Status-only handlers can still return `MovePlan{Responses: ...}` without `Stores`.

## DICOMweb (WADO / STOW / QIDO)

```go
client := &dicomweb.Client{BaseURL: "https://pacs.example/dicom-web"}

// STOW-RS
_, err := client.StoreFiles(ctx, "", []*godicom.FileDataset{fd})

// WADO-RS instance / series / study + metadata
raw, err := client.RetrieveInstance(ctx, studyUID, seriesUID, sopUID)
parts, err := client.RetrieveSeries(ctx, studyUID, seriesUID)
parts, err = client.RetrieveStudy(ctx, studyUID)
meta, err := client.RetrieveInstanceMetadata(ctx, studyUID, seriesUID, sopUID)

// WADO-RS rendered (JPEG/PNG) + Pixel Data bulkdata
mt, img, err := client.RetrieveRenderedInstance(ctx, studyUID, seriesUID, sopUID, dicomweb.RenderOptions{
	MediaType: dicomweb.MediaTypeJPEG,
	Quality:   90,
})
bulk, err := client.RetrieveBulkData(ctx, studyUID, seriesUID, sopUID)

// QIDO-RS studies / series / instances
matches, err := client.SearchStudies(ctx, url.Values{"PatientID": {"P001"}})
series, err := client.SearchSeries(ctx, studyUID, url.Values{"Modality": {"CT"}})
instances, err := client.SearchInstances(ctx, studyUID, seriesUID, nil)
```

Origin-server MVP for tests/demos:

```go
store := dicomweb.NewMemoryStore()
http.ListenAndServe(":8080", dicomweb.Handler(store, "/dicom-web"))
```

## TLS / timeouts / logging (Phase 4)

```go
// DIMSE over TLS
assoc, err := ae.Dial(ctx, ae.Config{
	AETitle:     "MYSCU",
	IdleTimeout: 30 * time.Second,
	TLS:         &tls.Config{ServerName: "pacs.example", MinVersion: tls.VersionTLS12},
	Logger:      slog.Default(),
}, "pacs.example:2762", "ANY-SCP")

// DICOMweb client with TLS + timeout
client, err := dicomweb.NewClient("https://pacs.example/dicom-web",
	dicomweb.WithTimeout(30*time.Second),
	dicomweb.WithTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12}),
	dicomweb.WithLogger(slog.Default()),
)
```

Optional real-PACS soak (skipped unless env is set):

```bash
GONETDICOM_PACS_ADDR=host:11112 GONETDICOM_PACS_AE=ANY-SCP \
  go test -tags=integration ./ae -run TestIntegrationCEchoPACS -v
```

## Packages

| Package | Role |
|---------|------|
| `pdu` | A-ASSOCIATE / P-DATA-TF / A-RELEASE / A-ABORT + PDV fragmentation |
| `dimse` | C-ECHO / C-STORE / C-FIND / C-MOVE / C-GET / C-CANCEL + N-ACTION / N-EVENT-REPORT |
| `ae` | Association SCU/SCP + TLS / idle timeout / optional slog / role selection |
| `dicomweb` | WADO-RS (incl. rendered/bulkdata) / STOW-RS / QIDO-RS client + origin-server MVP |

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
- [x] SCP/SCU Role Selection Negotiation (PDU `0x54`, `ae.BuildRole`)
- [x] C-CANCEL-RQ (`ae.CCancel`) for outstanding FIND/MOVE/GET
- [x] Storage Commitment Push Model (`ae.NAction` / `ae.NEventReport`)

### Phase 3 — DICOMweb MVP
- [x] WADO-RS Retrieve Instance (`application/dicom`) + Metadata (`dicom+json`)
- [x] WADO-RS Retrieve Study / Series (+ metadata)
- [x] WADO-RS Retrieve Rendered (instance JPEG/PNG) + Pixel Data bulkdata
- [x] STOW-RS Store
- [x] QIDO-RS Search for Studies / Series / Instances

### Phase 4 — Harden
- [x] Timeouts (`IdleTimeout` / HTTP client timeout)
- [x] TLS helpers (DIMSE `Config.TLS` / `ListenAndServeTLS`; DICOMweb `WithTLSConfig`)
- [x] Optional `slog` logging
- [x] Optional real-PACS C-ECHO soak (`-tags=integration`)
- [ ] Broader fixture / multi-PACS matrix in CI

## Layout

```
gonetdicom/
├── ae/                # Association SCU / SCP
├── dimse/             # DIMSE command sets
├── dicomweb/          # DICOMweb client + origin-server MVP
├── pdu/               # Upper Layer PDUs
├── pynetdicom/        # submodule → pydicom/pynetdicom
├── go.mod
└── README.md
```

## License

MIT — see [LICENSE](LICENSE).

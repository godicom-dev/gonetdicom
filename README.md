# gonetdicom

*gonetdicom* is a Go library for [DICOM](https://www.dicomstandard.org/) networking.
It provides DIMSE association services and a DICOMweb (WADO-RS / QIDO-RS / STOW-RS)
client and origin-server MVP. Dataset and pixel I/O come from
[godicom](https://github.com/godicom-dev/godicom).

[![Go Version](https://img.shields.io/badge/Go-%3E%3D%201.26-%23007d9c)](https://go.dev/)
[![GoDoc](https://pkg.go.dev/badge/github.com/godicom-dev/gonetdicom)](https://pkg.go.dev/github.com/godicom-dev/gonetdicom)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

```
gonetdicom
 └── github.com/godicom-dev/godicom
```

## Installation

```bash
go get github.com/godicom-dev/gonetdicom@latest
```

Clone with the optional reference submodule (DIMSE fixtures):

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

Propose a storage presentation context, then send a godicom Dataset (or
pre-encoded bytes):

```go
cfg := ae.Config{
	AETitle: "STORESCU",
	PresentationContexts: []ae.PresentationContext{{
		ID:             1,
		AbstractSyntax: "1.2.840.10008.5.1.4.1.1.7", // Secondary Capture
		TransferSyntaxes: []string{"1.2.840.10008.1.2"},
	}},
}
assoc, err := ae.Dial(ctx, cfg, "pacs.example:11112", "ANY-SCP")
// ...
res, err := assoc.CStore(ctx, ae.StoreRequest{
	AffectedSOPClassUID:    "1.2.840.10008.5.1.4.1.1.7",
	AffectedSOPInstanceUID: "1.2.3.4.5", // optional: omit to use Data.SOPInstanceUID or ae.NewInstanceUID()
	Data:                   ds,
})

```

## C-FIND / C-MOVE / C-GET

```go
matches, err := assoc.CFind(ctx, ae.FindRequest{
	QueryModel:     ae.PatientRootQueryRetrieveInformationModelFind,
	IdentifierData: query,
})

matches, err = assoc.CMove(ctx, ae.MoveRequest{
	QueryModel:      ae.PatientRootQueryRetrieveInformationModelMove,
	MoveDestination: "STORESCP",
	IdentifierData:  query,
})

matches, err = assoc.CGet(ctx, ae.GetRequest{
	QueryModel:     ae.PatientRootQueryRetrieveInformationModelGet,
	IdentifierData: query,
	OnCStore: func(_ context.Context, req ae.StoreRequest) uint16 {
		_ = req.Data // decoded Dataset
		return status.Success
	},
})
```

For C-GET against real PACS, also propose SCP/SCU Role Selection so the SCU can
receive C-STORE:

```go
cfg := ae.Config{
	AETitle: "GETSCU",
	PresentationContexts: []ae.PresentationContext{ /* Get model + storage SOP Class */ },
	RoleSelections: []pdu.RoleSelection{
		ae.BuildRole(string(uid.CTImageStorage), false, true), // requestor as SCP
	},
}
```

Cancel an outstanding FIND / MOVE / GET with `assoc.CCancel(ctx, msgID)`.

## C-STORE SCP

`Serve` blocks until `ctx` is cancelled. Do not reuse a short `WithTimeout` from
the C-ECHO snippet — that would shut the SCP down after a few seconds.

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
defer stop()

ln, err := net.Listen("tcp", ":11112")
if err != nil {
	log.Fatal(err)
}
err = ae.Serve(ctx, ln, ae.ServerConfig{
	AETitle:                  "STORESCP",
	AcceptedAbstractSyntaxes: ae.AllStorageSOPClasses,
	OnCStore: func(_ context.Context, req ae.StoreRequest) uint16 {
		if req.Data == nil || req.FileMeta == nil {
			return status.ProcessingFailure
		}
		fd := &godicom.FileDataset{Dataset: req.Data, FileMeta: req.FileMeta}
		if err := fd.SaveAs(req.AffectedSOPInstanceUID+".dcm", &godicom.WriteOptions{EnforceFileFormat: true}); err != nil {
			return status.ProcessingFailure
		}
		return status.Success
	},
})
```

`AcceptedAbstractSyntaxes` may include `"*"` to accept any peer-proposed abstract
syntax. Named DIMSE status constants live in package
[`status`](https://pkg.go.dev/github.com/godicom-dev/gonetdicom/status).

## C-MOVE SCP (Move Destination)

Configure `MoveDestinations` and return `MovePlan.Stores` from `OnCMove`. The SCP
dials the destination AE and runs C-STORE sub-operations (`MaxAssociations` > 1
fans out associations):

```go
_ = ae.Serve(ctx, moveLn, ae.ServerConfig{
	AETitle: "MOVESCP",
	AcceptedAbstractSyntaxes: []string{
		ae.PatientRootQueryRetrieveInformationModelMove,
	},
	MoveDestinations: map[string]ae.MoveDestination{
		"STORESCP": {Addr: "127.0.0.1:11112", MaxAssociations: 4},
	},
	OnCMove: func(_ context.Context, req ae.MoveRequest) ae.MovePlan {
		return ae.MovePlan{Stores: []ae.StoreRequest{{ /* ... */ }}}
	},
})
```

## Storage Commitment & DIMSE-N

```go
res, err := assoc.NAction(ctx, ae.ActionRequest{
	RequestedSOPClassUID:    ae.StorageCommitmentPushModelSOPClass,
	RequestedSOPInstanceUID: ae.StorageCommitmentPushModelSOPInstance,
	ActionTypeID:            dimse.StorageCommitmentActionTypeRequest,
	ActionInformationData:   info,
	OnNEventReport: func(_ context.Context, req ae.EventReportRequest) uint16 {
		return status.Success
	},
})

res, err = assoc.NGet(ctx, ae.NGetRequest{ /* ... */ })
res, err = assoc.NSet(ctx, ae.SetRequest{ /* ... */ })
res, err = assoc.NCreate(ctx, ae.CreateRequest{ /* ... */ })
res, err = assoc.NDelete(ctx, ae.DeleteRequest{ /* ... */ })
```

Async N-EVENT-REPORT on a new association is available via
`EventReportRequest.AsyncDestination`. SCP handlers: `OnNAction`,
`OnNEventReport`, `OnNGet`, `OnNSet`, `OnNCreate`, `OnNDelete`.

## User Identity Negotiation

```go
assoc, err := ae.Dial(ctx, ae.Config{
	AETitle:      "IDSCU",
	UserIdentity: ae.UsernamePasscodeIdentity("alice", "secret", false),
}, addr, "IDSCP")

_ = ae.Serve(ctx, ln, ae.ServerConfig{
	AETitle: "IDSCP",
	OnUserIdentity: func(req pdu.UserIdentityRQ) (bool, []byte) {
		return string(req.PrimaryField) == "alice", nil
	},
})
```

Nil `OnUserIdentity` accepts the association and omits any AC response item.

## DICOMweb (WADO / STOW / QIDO)

```go
client := &dicomweb.Client{BaseURL: "https://pacs.example/dicom-web"}

_, err := client.StoreFiles(ctx, "", []*godicom.FileDataset{fd})

raw, err := client.RetrieveInstance(ctx, studyUID, seriesUID, sopUID)
parts, err := client.RetrieveSeries(ctx, studyUID, seriesUID)
meta, err := client.RetrieveInstanceMetadata(ctx, studyUID, seriesUID, sopUID)

mt, img, err := client.RetrieveRenderedInstance(ctx, studyUID, seriesUID, sopUID, dicomweb.RenderOptions{
	MediaType: dicomweb.MediaTypeJPEG,
	Quality:   90,
})
bulk, err := client.RetrieveBulkData(ctx, studyUID, seriesUID, sopUID)

matches, err := client.SearchStudies(ctx, url.Values{"PatientID": {"P001"}})
```

Origin-server MVP for tests and demos:

```go
store := dicomweb.NewMemoryStore()
http.ListenAndServe(":8080", dicomweb.Handler(store, "/dicom-web"))
```

## TLS, timeouts, logging

```go
assoc, err := ae.Dial(ctx, ae.Config{
	AETitle:     "MYSCU",
	IdleTimeout: 30 * time.Second,
	TLS:         &tls.Config{ServerName: "pacs.example", MinVersion: tls.VersionTLS12},
	Logger:      slog.Default(),
}, "pacs.example:2762", "ANY-SCP")

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
| [`ae`](https://pkg.go.dev/github.com/godicom-dev/gonetdicom/ae) | Association SCU / SCP, TLS, roles, identity |
| [`dimse`](https://pkg.go.dev/github.com/godicom-dev/gonetdicom/dimse) | DIMSE command sets (C- and N- services) |
| [`pdu`](https://pkg.go.dev/github.com/godicom-dev/gonetdicom/pdu) | Upper-layer PDUs and PDV fragmentation |
| [`dicomweb`](https://pkg.go.dev/github.com/godicom-dev/gonetdicom/dicomweb) | WADO-RS / STOW-RS / QIDO-RS client + origin MVP |
| [`status`](https://pkg.go.dev/github.com/godicom-dev/gonetdicom/status) | Named DIMSE status constants |

## Documentation

- [pkg.go.dev API reference](https://pkg.go.dev/github.com/godicom-dev/gonetdicom)
- [CHANGELOG](CHANGELOG.md)
- [TODO](TODO.md) — deferred items and known gaps

## License

MIT — see [LICENSE](LICENSE).

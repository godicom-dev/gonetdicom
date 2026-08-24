[![CI](https://github.com/godicom-dev/gonetdicom/actions/workflows/ci.yml/badge.svg)](https://github.com/godicom-dev/gonetdicom/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/badge/Go-%3E%3D%201.26-%23007d9c)](https://go.dev/)
[![GoDoc](https://pkg.go.dev/badge/github.com/godicom-dev/gonetdicom)](https://pkg.go.dev/github.com/godicom-dev/gonetdicom)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

# *gonetdicom*

A Go implementation of the [DICOM](https://www.dicomstandard.org/) networking
protocol and DICOMweb (PS3.18) transactions.

## Description

[DICOM](https://www.dicomstandard.org/) is the international standard for medical
images and related information. It defines the formats and communication
protocols for media exchange in radiology, cardiology, radiotherapy and other
medical domains.

*gonetdicom* implements the DICOM networking protocol in Go. Working with
[godicom](https://github.com/godicom-dev/godicom), it allows the easy creation of
DICOM *Service Class Users* (SCUs) and *Service Class Providers* (SCPs), plus a
DICOMweb user agent and origin-server MVP.

*gonetdicom*'s main association helpers live in package
[`ae`](https://pkg.go.dev/github.com/godicom-dev/gonetdicom/ae):

- Start as an SCP with `ae.Serve` / `ae.ListenAndServeTLS` after configuring
  accepted presentation contexts and handlers (`OnCStore`, `OnCFind`, …)
- Act as an SCU with `ae.Dial`, which returns an `*ae.Association` you use to
  send DIMSE-C and DIMSE-N messages (`CEcho`, `CStore`, `CFind`, `CMove`,
  `CGet`, `NAction`, …)

Dataset and pixel I/O come from *godicom*; *gonetdicom* focuses on Upper Layer
PDUs, DIMSE command sets, association negotiation, and HTTP DICOMweb.

```
gonetdicom
 └── github.com/godicom-dev/godicom
```

Behaviour for DIMSE is primarily aligned with
[pynetdicom](https://github.com/pydicom/pynetdicom) (git submodule fixtures).
DICOMweb follows DICOM PS3.18.

## Documentation

- [pkg.go.dev API reference](https://pkg.go.dev/github.com/godicom-dev/gonetdicom)
- [CHANGELOG](CHANGELOG.md)
- [TODO](TODO.md) — deferred items and known gaps

## Installation

### Dependencies

[godicom](https://github.com/godicom-dev/godicom)

### Current release

```bash
go get github.com/godicom-dev/gonetdicom@latest
```

Clone with the optional reference submodule (DIMSE fixtures):

```bash
git clone --recurse-submodules https://github.com/godicom-dev/gonetdicom.git
```

## Supported DIMSE services

### SCU

Once associated, the following DIMSE-C and DIMSE-N services are available on
`*ae.Association`:

| Service | Method |
|---------|--------|
| C-ECHO | `CEcho` |
| C-STORE | `CStore` |
| C-FIND | `CFind` |
| C-MOVE | `CMove` |
| C-GET | `CGet` |
| C-CANCEL | `CCancel` |
| N-EVENT-REPORT | `NEventReport` |
| N-GET | `NGet` |
| N-SET | `NSet` |
| N-ACTION | `NAction` |
| N-CREATE | `NCreate` |
| N-DELETE | `NDelete` |

### SCP

`ae.ServerConfig` handlers: `OnCStore`, `OnCFind`, `OnCMove`, `OnCGet`,
`OnNAction`, `OnNEventReport`, `OnNGet`, `OnNSet`, `OnNCreate`, `OnNDelete`,
plus optional `OnUserIdentity`, `MoveDestinations`, role selection, and TLS.

## Examples

**Verification SCU (C-ECHO)**

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

**Storage SCU (C-STORE)**

Propose a storage presentation context, then send a *godicom* Dataset (or
pre-encoded bytes):

```go
cfg := ae.Config{
	AETitle: "STORESCU",
	PresentationContexts: []ae.PresentationContext{{
		ID:               1,
		AbstractSyntax:   "1.2.840.10008.5.1.4.1.1.7", // Secondary Capture
		TransferSyntaxes: []string{"1.2.840.10008.1.2"},
	}},
}
assoc, err := ae.Dial(ctx, cfg, "pacs.example:11112", "ANY-SCP")
// ...
res, err := assoc.CStore(ctx, ae.StoreRequest{
	AffectedSOPClassUID:    "1.2.840.10008.5.1.4.1.1.7",
	AffectedSOPInstanceUID: "1.2.3.4.5", // optional: Data.SOPInstanceUID or ae.NewInstanceUID()
	Data:                   ds,
})
```

`CStore` does not modify the `Data` you hand it. With no
`AffectedSOPInstanceUID` and no `SOPInstanceUID` in the dataset it generates a
UID and encodes a copy carrying it, so reusing one `Dataset` across a series
gives every instance its own identity; supplying the UID skips the copy.

Leaving `ID` unset lets `Dial` assign the free odd Presentation-context-IDs,
which is usually what you want; an explicit `ID` is kept as given. Since IDs are
odd values in 1..255, one association carries at most
`ae.MaxPresentationContexts` (128) of them — proposing more, an even ID, or the
same ID twice fails with `ae.ErrPresentationContexts` rather than putting an
ambiguous A-ASSOCIATE-RQ on the wire. To offer many storage SOP classes at once,
negotiate in batches or narrow the list.

**Query / Retrieve SCU (C-FIND / C-MOVE / C-GET)**

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

An `*ae.Association` carries one DIMSE operation at a time: each method sends its
request and then reads responses until the final one, so two running at once
interleave PDUs on the same connection. Dial one association per worker for
parallel work. `CCancel`, `Abort` and `Close` exist to reach an operation that is
already blocked, so those three are safe to call from another goroutine.

**Storage SCP (C-STORE)**

`Serve` blocks until `ctx` is cancelled. Do not reuse a short `WithTimeout`
from the C-ECHO snippet — that would shut the SCP down after a few seconds.

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

The Called AE Title is checked: a requestor asking for anything other than
`AETitle` (or an entry in `AlternativeAETitles`) gets an A-ASSOCIATE-RJ with
*called-AE-title-not-recognized*. Set `AllowAnyCalledAETitle: true` for an SCP
that deliberately answers to any name. A requestor that does not announce
protocol version 1 is likewise rejected.

Three `ServerConfig` fields bound what a peer can cost the SCP:
`HandshakeTimeout` (default 30s) limits how long a connection may sit without
completing negotiation, `IdleTimeout` ends an association whose peer goes silent
— it is refreshed per read and write, so it bounds silence rather than total
duration — and `MaxConcurrentAssociations` caps associations handled at once,
answering anything above the cap with a transient *local-limit-exceeded*
A-ASSOCIATE-RJ. `IdleTimeout` and `MaxConcurrentAssociations` are unlimited when
unset; pass a negative `HandshakeTimeout` to opt out of that one deliberately.

**Move Destination SCP (C-MOVE)**

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

**Storage Commitment & DIMSE-N**

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
```

Async N-EVENT-REPORT on a new association is available via
`EventReportRequest.AsyncDestination`.

**User Identity Negotiation**

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

## DICOMweb (WADO-RS / STOW-RS / QIDO-RS)

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

UIDs are checked before a request is built: a study, series, or instance UID
carrying a `/`, a `..`, or a `%` names a resource other than the one asked for, so
it fails with `dicomweb.ErrInvalidPath` instead of going out.

Both sides bound how much one body may buffer, since instances are held whole in
memory. `Client.MaxResponseBytes` (or `dicomweb.WithMaxResponseBytes`) defaults to
`dicomweb.DefaultMaxResponseBytes` (1 GiB) and fails with `dicomweb.ErrTooLarge`
rather than returning a truncated study; a negative value opts out.

Origin-server MVP for tests and demos:

```go
store := dicomweb.NewMemoryStore()
http.ListenAndServe(":8080", dicomweb.Handler(store, "/dicom-web",
	dicomweb.WithMaxRequestBytes(64<<20)))
```

A STOW-RS body is buffered whole before it reaches the `Store`, so `Handler`
bounds it at `dicomweb.DefaultMaxRequestBytes` (256 MiB) unless
`WithMaxRequestBytes` says otherwise, answering anything larger with 413. Error
responses carry only a status and a fixed reason — the cause comes from your
`Store`, from decoding stored bytes, or from the render path, and goes to the
request context's logger (`gonetdicom.WithLogger`) instead of to the requestor.

## Logging

*gonetdicom* uses Go's `log/slog`. By default it is silent (`DiscardHandler`),
similar in spirit to leaving pynetdicom's `debug_logger` unset.

```go
import (
	"context"
	"crypto/tls"
	"log/slog"
	"os"
	"time"

	"github.com/godicom-dev/gonetdicom"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dicomweb"
)

h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
logger := slog.New(h)

assoc, err := ae.Dial(ctx, ae.Config{
	AETitle:     "MYSCU",
	IdleTimeout: 30 * time.Second,
	TLS:         &tls.Config{ServerName: "pacs.example", MinVersion: tls.VersionTLS12},
	Logger:      logger, // Config / Client wins over context
}, "pacs.example:2762", "ANY-SCP")

// Or via context (shared with godicom.ReadFileContext etc.)
ctx = gonetdicom.WithLogger(ctx, logger)

client, err := dicomweb.NewClient("https://pacs.example/dicom-web",
	dicomweb.WithTimeout(30*time.Second),
	dicomweb.WithTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12}),
	dicomweb.WithLogger(logger),
)
```

Debug records use fixed attribute keys (`component`, `calling_ae`, `called_ae`,
`pdu_type_name`, `command_name`, `pc_id`, `message_id`, `status`, …). At Debug
level, AE logs PDU send/recv and DIMSE command summaries (pynetdicom-style).

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
| [`pdu`](https://pkg.go.dev/github.com/godicom-dev/gonetdicom/pdu) | Upper Layer PDUs and PDV fragmentation |
| [`dicomweb`](https://pkg.go.dev/github.com/godicom-dev/gonetdicom/dicomweb) | WADO-RS / STOW-RS / QIDO-RS client + origin MVP |
| [`status`](https://pkg.go.dev/github.com/godicom-dev/gonetdicom/status) | Named DIMSE status constants |

## Contributing

Bug reports, fixes, and documentation improvements are welcome. Please open an
issue or pull request on GitHub.

## License

MIT — see [LICENSE](LICENSE).

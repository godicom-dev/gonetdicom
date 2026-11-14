[![CI](https://github.com/godicom-dev/gonetdicom/actions/workflows/ci.yml/badge.svg)](https://github.com/godicom-dev/gonetdicom/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/godicom-dev/gonetdicom/graph/badge.svg)](https://codecov.io/gh/godicom-dev/gonetdicom)
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
- [PARITY](PARITY.md) — behaviour coverage against pynetdicom, gaps included
- [ADR 0001](docs/adr/0001-association-release-with-operations-in-flight.md) —
  what `Release` means while an operation is still running
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
		AbstractSyntax:   string(uid.SecondaryCaptureImageStorage),
		TransferSyntaxes: ae.UIDStrings(uid.ImplicitVRLittleEndian),
	}},
}
assoc, err := ae.Dial(ctx, cfg, "pacs.example:11112", "ANY-SCP")
// ...
res, err := assoc.CStore(ctx, ae.StoreRequest{
	AffectedSOPClassUID:    string(uid.SecondaryCaptureImageStorage),
	AffectedSOPInstanceUID: "1.2.3.4.5", // optional: Data.SOPInstanceUID or ae.NewInstanceUID()
	Data:                   ds,
})
```

SOP Classes and transfer syntaxes are named by *godicom*'s
[`uid`](https://pkg.go.dev/github.com/godicom-dev/godicom/uid) package, so
nothing here needs a UID pasted in; `ae.UIDStrings` converts a list of them to
the `[]string` these fields hold.

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
	OnCStore: func(_ context.Context, ev ae.StoreEvent) uint16 {
		if _, err := ev.Decode(); err != nil {
			return status.ProcessingFailure
		}
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

Cancel an outstanding FIND / MOVE / GET with `assoc.CCancel(ctx, msgID)`. The
request structs take a `MessageID` when another goroutine needs to name the
operation it is cancelling; leaving it zero allocates one. A cancel takes effect
at the next sub-operation boundary, which for C-MOVE means the instance already
on the wire to the Move Destination is finished and the ones after it are never
dialled for; the final response says how many were tried.

An `*ae.Association` carries one DIMSE operation at a time, and that is now
enforced rather than assumed: a second call returns `ae.ErrOperationInFlight`
instead of reading the first one's responses. `Release` returns it too, and for
the same reason plus one — PS3.8 7.2.2 allows a requestor nothing but A-ABORT
after it asks to release, so a release is a statement about the association and
not a way to stop a running retrieval. Stop the operation first (cancel its
context, or `CCancel`), then release. Dial one association per worker for
parallel work. `CCancel`, `Abort` and `Close` exist to reach an operation that is
already blocked, so those three are safe to call from another goroutine.
The reasoning is in
[`docs/adr/0001`](docs/adr/0001-association-release-with-operations-in-flight.md).

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
	OnCStore: func(_ context.Context, ev ae.StoreEvent) uint16 {
		ds, err := ev.Decode()
		if err != nil {
			return status.ProcessingFailure
		}
		fd := &godicom.FileDataset{Dataset: ds, FileMeta: ev.FileMeta}
		if err := fd.SaveAs(ev.AffectedSOPInstanceUID+".dcm", &godicom.WriteOptions{EnforceFileFormat: true}); err != nil {
			return status.ProcessingFailure
		}
		return status.Success
	},
})
```

`ev.Stream()` is the store's dataset as an `io.Reader`, and `ev.Bytes()` is the
same data drained into memory. The stream is fed straight from the association
reader, one P-DATA fragment at a time, so `io.Copy` to a file moves a series of
any size through constant memory: the copy buffer grows, never the dataset. A
500 MB enhanced CT used to cost 500 MB resident per association; it now costs
one fragment. dcm4che gets there with `PDVInputStream` and a caller-managed
buffer, pynetdicom by reading the whole thing into a `BytesIO`; here it is a
channel with a single slot, which is also the backpressure — a handler that
stops reading stops the reader.

The stream is single-use, and the library finishes reading it when the handler
returns even if the handler did not, so the next message always parses. Nothing
decodes the dataset unless you call `ev.Decode()` (which reads it whole first),
so a handler that only stores or forwards bytes pays no decode cost. `Decode`
reports an undecodable store as a normal error and the handler picks the
status — an SCP that cannot parse a store answers with a service failure
(pynetdicom uses 0xC211, *cannot understand*).

Every handler runs with the association context attached. `ae.AssocFromContext(ctx)`
returns the calling and called AE titles, the requestor's remote address and
implementation identification (Class UID and Version Name from its
A-ASSOCIATE-RQ), and — per message — the abstract syntax, presentation
context ID and negotiated transfer syntax of the context it arrived on, for
device checks, per-AE authorization and audit logging:

```go
OnCStore: func(ctx context.Context, ev ae.StoreEvent) uint16 {
	info, _ := ae.AssocFromContext(ctx)
	if !deviceRegistry.Allows(info.RemoteAddr, info.CallingAETitle) {
		return status.ProcessingFailure
	}
	// ...
},
```

`ae.AllStorageSOPClasses` is the whole Storage Service Class (pynetdicom's
`_STORAGE_CLASSES`), built from the *godicom* `uid` constants so each entry is
named in code rather than in a comment; a test checks the set against the
pynetdicom submodule. For a narrower SCP, name the classes you accept:
`ae.UIDStrings(uid.CTImageStorage, uid.MRImageStorage)`.

`AcceptedAbstractSyntaxes` may include `"*"` to accept any peer-proposed abstract
syntax. Named DIMSE status constants live in package
[`status`](https://pkg.go.dev/github.com/godicom-dev/gonetdicom/status).

Transfer syntaxes are negotiated separately. `AcceptedTransferSyntaxes` lists
what this SCP accepts, most preferred first; leaving it empty means
`ae.DefaultTransferSyntaxes` — implicit VR little endian, explicit VR little
endian, deflated explicit VR little endian, explicit VR big endian, the same set
pynetdicom offers and exactly what godicom can decode. A context whose syntaxes
do not intersect is answered with result 4 (*transfer syntaxes not supported*),
which tells the requestor to retry with another encoding; accepting a syntax we
cannot read would be a claim the first C-STORE disproves. An SCP that only files
bytes and never calls `Decode` can widen it with `"*"`, and then stores whatever
the peer sends.

Three optional user-information items are negotiated and exposed:

- **Asynchronous Operations Window** — `Config.AsyncOperationsWindow` proposes
  it, `ServerConfig.AsyncOperationsWindow` sets what an SCP advertises, and
  `assoc.AsyncOperationsWindow()` reads the answer. An SCP that receives a
  window without configuring one answers `1/1`, which is what this package
  actually does: the DIMSE loop takes one message at a time. The value is also
  on `AssocInfo`, so a handler can log or reject on it.
- **SOP Class Extended Negotiation** — `Config.ExtendedNegotiations` proposes
  per-class application information and `ServerConfig.ExtendedNegotiation`
  answers it. The information is opaque here; only the service class that
  defined it knows what it means. Declining a class omits the item, which is
  how PS3.8 says "no extended semantics here".
- **SOP Class Common Extended Negotiation** — `Config.CommonExtendedNegotiations`
  states, per SOP Class, which Service Class the class is used under and which
  general classes it may be matched to;
  `ServerConfig.CommonExtendedNegotiation` confirms or withholds that statement
  and `assoc.CommonExtendedNegotiation(uid)` reads the answer. This is the item
  that makes Storage Commitment work: the class a peer stores under and the
  Service Class that commits to it are different, and the requestor has to know
  the pairing before it sends anything. Withholding the item is not a refusal —
  the class stays usable the plain DIMSE way.

A Query/Retrieve identifier can be checked before the handler sees it:

```go
ServerConfig{
	IdentifierValidator: ae.QueryRetrieveLevelValidator("PATIENT", "STUDY"),
	// ...
}
```

A non-zero return answers the request with that status (0xA900 *identifier does
not match SOP class*) and skips the handler; the association stays up, because
the peer can retry with a corrected identifier.

`QueryModelValidator` is the same hook with the model's level table behind it:

```go
ServerConfig{
	IdentifierValidator: ae.QueryModelValidator(), // or ae.QueryModelValidator("PATIENT", "STUDY")
	// ...
}
```

It refuses a level the model does not answer (PATIENT on Study Root, SERIES on
the retired Patient/Study Only model) and a C-MOVE/C-GET that carries no unique
key for the level it retrieves. C-FIND is exempt from the key rule — a STUDY
level query by Patient Name is a legal query — and a model the package has no
table for is passed to the handler instead of refused on a guess.
`ae.QueryHierarchyFor` exposes the same table if an application wants to build
its own check.

The framework still does not carry a per-model attribute dictionary — which keys
a model must return, and under what conditions — because that is dataset
knowledge, and the hook is where it goes.

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

The SCP owns the sub-operation counts, not the handler: once the C-STORE
sub-operations are done it answers with what actually happened — `0x0000` when
nothing failed, `0xB000` when some failed or warned, `0xA702` when every one
failed — and a Warning or Failure carries the Failed SOP Instance UID List
`(0008,0058)`. C-GET reports the same way. `SubOperations` on a `RetrieveMatch`
the handler returns is replaced for the final response, because only the SCP
knows how the transfers went.

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
	OnUserIdentity: func(ctx context.Context, req pdu.UserIdentityRQ) (bool, []byte) {
		// The association identity is already on ctx here: the calling AE
		// title, the requestor's address and its implementation Class UID.
		info, _ := ae.AssocFromContext(ctx)
		if !trustedHosts[info.RemoteAddr.String()] {
			return false, nil
		}
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
frames, err := client.RetrieveFrames(ctx, studyUID, seriesUID, sopUID, []int{1})

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

## Concurrency and limits

An SCP is one goroutine per association. There is no worker pool and no job
queue: goroutines are cheap, the scheduler is already the pool, and parking
work behind a fixed number of workers adds latency and a place for deadlocks
without buying anything. The three things a thread pool is expected to provide
are provided separately, by the layer that should own each one.

| Requirement | Mechanism |
|---|---|
| A hard ceiling on concurrent associations | `ServerConfig.MaxConcurrentAssociations`. Over the cap the requestor gets A-ASSOCIATE-RJ (transient / local-limit-exceeded) — the same answer dcm4che's `Device.setLimitOpenAssociations` and pynetdicom's `AE.maximum_associations` send |
| Per-AE, per-tenant or per-licence policy | `ServerConfig.Admit`, one hook that sees `AssocInfo` and decides |
| Queue instead of reject | Block inside `Admit`. Its context dies at `HandshakeTimeout` or at shutdown, so a queue cannot outlive the handshake it is queueing for |
| A memory ceiling | `StoreEvent.Stream()`: peak per association is one P-DATA fragment, whatever the dataset size. No budget, no accounting, no tuning |
| A blast radius for one absurd object | `ServerConfig.MaxDatasetSize`, per message. With streaming it is no longer what protects memory; it is a blunt "this object is not real" cutoff that ends the association |
| Surviving a handler bug | Nothing to configure. A panic is caught once at the association boundary, logged with its stack, answered with A-ABORT; the process and every other association carry on |

```go
slots := make(chan struct{}, 8) // admission, not a work queue

cfg := ae.ServerConfig{
	AETitle:                  "MYSCP",
	MaxDatasetSize:           64 << 20,
	AcceptedAbstractSyntaxes: ae.AllStorageSOPClasses,
	Admit: func(ctx context.Context, info ae.AssocInfo) (func(), error) {
		select {
		case slots <- struct{}{}:
			// Runs once when the association ends, including after a panic.
			return func() { <-slots }, nil
		case <-ctx.Done():
			return nil, ctx.Err() // handshake deadline or shutdown
		}
	},
	OnCStore: func(ctx context.Context, ev ae.StoreEvent) uint16 {
		// Constant memory, whatever the dataset size: the fragments go
		// straight from the socket to the file.
		f, err := os.CreateTemp("", "store-*.dcm")
		if err != nil {
			return status.ProcessingFailure
		}
		defer f.Close()
		if _, err := io.Copy(f, ev.Stream()); err != nil {
			return status.ProcessingFailure
		}
		return status.Success
	},
}
```

`Admit` is a hook rather than a set of configuration fields because who gets in
is policy, and the application is the only layer that knows about tenants,
licences and maintenance windows. Returning a `*ae.RejectError` — usually
`ae.RejectLocalLimit(err)`, which says "busy, retry later" — rejects with the
exact PS3.8 result/source/reason; any other error rejects as
transient / no-reason-given. The same hook covers IP ranges, per-peer counts,
and a rate limiter, without the library growing a field per idea.

Policy that blocks is deliberate, not an oversight: the blocked party is the
association's own goroutine, so the accept loop and every other association
keep running. That is the whole difference from a thread pool — in Java or
Python the pool is what makes blocking affordable; in Go it never was the
expensive part.

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
issue or pull request on GitHub; [CONTRIBUTING.md](CONTRIBUTING.md) has the
norms — tests that fail without the change, wire formats pinned against
pydicom or dcm4che, one logical change per PR.

## License

MIT — see [LICENSE](LICENSE).

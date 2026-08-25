# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

Robustness audit of the wire decoders, the SCP association lifecycle and the
DICOMweb handlers ([#39](https://github.com/godicom-dev/gonetdicom/issues/39)).
No exported symbol was removed; three behaviour changes are listed under Changed.

### Security
- `pdu.Read` no longer sizes its buffer from the declared length, where six bytes
  from an unauthenticated peer bought a 4 GiB allocation: a 16 MiB
  `MaxPDUReadLength` backstop with `ErrPDUTooLarge`, `ReadLimit` for a tighter
  bound, and a chunked read that grows with bytes actually received
- SCP resource limits, none of which existed: `ServerConfig.HandshakeTimeout`
  (30s by default), `IdleTimeout` (refreshed per read and per write, so it
  measures silence rather than duration) and `MaxConcurrentAssociations` (over
  the cap the peer gets an A-ASSOCIATE-RJ, not a bare close). `Serve` also closes
  in-flight connections when its context is cancelled
- SCP validates the Called AE Title and the Protocol Version during negotiation
  instead of accepting whatever arrives, and rejects an empty Calling AE Title —
  which used to make the SCP fail while encoding its own A-ASSOCIATE-AC and drop
  the connection with no explanation
- WADO-RS multipart boundaries are per-response and random. The boundary was
  hard-coded, so an instance containing that delimiter line ended the body early:
  HTTP 200 with silently truncated data
- DICOMweb path segments are validated (`ErrInvalidPath`), request and response
  bodies are bounded (`DefaultMaxRequestBytes`, `DefaultMaxResponseBytes`,
  `WithMaxRequestBytes`, `WithMaxResponseBytes`, `ErrTooLarge`), and handler
  errors no longer echo store internals to the client
- 32-bit wire lengths are compared in `uint64` instead of being converted to
  `int`. Where `int` is 32 bits wide a DIMSE Value Length of `0xFFFFFFF8` came
  out negative, passed its bounds check and panicked the decoder from 8 bytes; a
  PDV item length of `0x7FFFFFFF` did the same from 20. Not reachable in a
  released build, since godicom does not compile for a 32-bit target either

### Fixed
- UID padding is trimmed when decoding an A-ASSOCIATE
  ([#42](https://github.com/godicom-dev/gonetdicom/issues/42)). PS3.5 9.1 pads an
  odd-length UID with a NUL and most UIDs traded during negotiation are odd
  (`1.2.840.10008.1.1` is 17 bytes), so a conforming peer sends that byte — and
  it stayed in the string, losing every comparison built on it: the SCP answered
  "abstract syntax not supported" for a SOP class it supports, a proposed Role
  Selection was dropped, and an accepted Transfer Syntax reached godicom naming
  nothing. Covers the Application Context, Abstract Syntax, Transfer Syntax,
  Implementation Class UID and Role Selection SOP Class items. Encoding is
  unchanged and still writes UIDs unpadded, which is what pynetdicom sends
- C-MOVE sub-operation counts saturate at 65535 instead of wrapping
  ([#41](https://github.com/godicom-dev/gonetdicom/issues/41)). The four counts
  are US on the wire, but the SCP also *counted* in `uint16`, so a `MovePlan` of
  65536 stores reported `Remaining: 0` in its first Pending response — an SCU
  reading that as "nothing outstanding" stopped there — and the next response
  computed `total-done` as `0-1` and said 65535 were outstanding, so the count
  moved backwards. On the failure path 65536 failures were reported as 0: total
  success under a failure status. Counting is now in `int` and narrowed once, at
  the wire, and the final status is decided on the real counts. C-MOVE itself has
  no 65535 limit, so an oversized plan is served and logged rather than rejected
- One reader per SCP association. Cancel detection read the connection directly
  under a 2 ms deadline, so a C-CANCEL-RQ split across TCP segments was consumed
  and discarded and the next read landed mid-PDU, after which the SCP could block
  forever on an open connection. Removing the per-response deadline also made
  600 C-FIND matches stream in ~20 ms instead of ~1.5 s
- The SCP dispatches on the Command Field rather than trying each `Decode*RQ` in
  turn: a command set carrying no (0000,0100) satisfied all of them and was
  handled as a C-ECHO. A C-CANCEL-RQ arriving after its operation finished is
  logged and ignored instead of aborting the association
- Presentation-context-IDs are assigned without collisions and validated before
  anything is sent (`ErrPresentationContexts`, `MaxPresentationContexts`). The
  old `id = 2*i+1` overflowed a byte past the 128th context and ignored IDs the
  caller had set, proposing one ID twice
- `CStore` no longer writes the SOP Instance UID it generates into the caller's
  `Dataset`, which made a reused Dataset send every instance under one identity
- Message IDs are atomic, and `Abort` no longer clears the connection out from
  under a blocked reader — aborting a blocked C-FIND panicked on a nil `net.Conn`
- `pdu` refuses to encode a value its length field cannot describe (`ErrTooLong`)
  rather than writing a truncated length

### Changed
- **A requested `Priority` of MEDIUM is sent as MEDIUM.** MEDIUM encodes as
  0x0000, and every SCU verb read that zero as "unset" and substituted LOW, so
  MEDIUM was the one priority the API could not express. An unset `Priority` now
  means MEDIUM; pass `dimse.PriorityLow` to deprioritise a request
- **An SCP answers only to its own AE title.** Set
  `ServerConfig.AllowAnyCalledAETitle`, or list the titles it should answer to in
  `AlternativeAETitles`, for the previous behaviour
- **`pdu.Read` rejects a PDU declaring more than `MaxPDUReadLength` (16 MiB).**
- `ae.AllStorageSOPClasses` is built from `godicom/uid` constants instead of UID
  strings with names in trailing comments, five of which disagreed with the UID
  beside them. The exported list is byte-identical: same 170 entries, same order
- Named constants for the A-ASSOCIATE-RJ result / source / reason bytes and the
  protocol version bit (`pdu.RejectResultPermanent`,
  `pdu.RejectSourceServiceUser`, `pdu.RejectReasonCalledAENotRecognized`,
  `pdu.ProtocolVersion1`, …)

### Added
- `ae.UIDStrings` converts `uid.UID` values to the `[]string` the abstract-syntax
  and transfer-syntax fields hold, so callers can name a SOP class instead of
  pasting its UID
- Fuzz targets over the unauthenticated wire path — `pdu.FuzzRead`,
  `pdu.FuzzDecodePDataTF`, `dimse.FuzzDecodeElements`,
  `dimse.FuzzCommandDecoders` — asserting encode/decode round-trip byte identity
  and full byte accounting, not just the absence of panics
- CI runs ubuntu/windows/macos against Go 1.26 and 1.27, tests `./pdu ./dimse
  ./status` on linux/386, and fuzzes each target for 60s per run

## [0.15.0] - 2026-08-13

### Added
- `log/slog` foundation aligned with godicom: `WithLogger` /
  `LoggerFromContext` / `SetDefaultLogger`, quiet `DiscardHandler` default,
  fixed attribute keys
- AE / SCP / DICOMweb resolve `Config.Logger` / `Client.Logger` over context
- Debug: PDU send/recv (`pdu_type_name`) and DIMSE command summaries
  (`command_name`, `pc_id`, `message_id`, `status`)

### Changed
- Depend on [godicom](https://github.com/godicom-dev/godicom) `v0.26.0`
  (slog logging, `PixelArray` / `DisplayFrame`, HTJ2K encode)
- Docs: README aligned with pynetdicom-style layout (description, DIMSE tables,
  examples)

## [0.14.0] - 2026-08-07

### Changed
- Depend on [godicom](https://github.com/godicom-dev/godicom) `v0.25.1` (JPEG/JPEG-LS encode, Implicit VR LE `PixelData` read fix)

## [0.13.0] - 2026-07-19

### Changed
- Depend on [godicom](https://github.com/godicom-dev/godicom) `v0.24.0`

### Added
- `ae.NewInstanceUID` — mint SOP Instance / Transaction UIDs via `uid.GenerateUID`
- C-STORE SCU: empty `AffectedSOPInstanceUID` is taken from `Data.SOPInstanceUID`, otherwise auto-minted
- N-CREATE SCP: success responses with no instance UID mint one (Part 7 requires the field)

## [0.12.0] - 2026-07-14

### Changed
- Align inbound C-STORE with pynetdicom `event.dataset` + `event.file_meta`: expose `StoreRequest.FileMeta`, remove convenience `SaveAs`/`File`

### Fixed
- Document Part 10 save path so compressed/multi-frame Pixel Data keep TransferSyntaxUID (avoid snow / “one frame”)

## [0.11.0] - 2026-07-14

### Added
- C-STORE SCP `OnCStore` now fills `StoreRequest.Data` (decoded godicom Dataset) and `TransferSyntax`, matching pynetdicom `event.dataset` / `save_as`

## [0.10.0] - 2026-07-14

### Added
- `ae.AllStorageSOPClasses`: pynetdicom `_STORAGE_CLASSES` catalog for Storage SCP abstract syntaxes
- `AcceptedAbstractSyntaxes` may include `"*"` to accept any peer-proposed Abstract Syntax (opt-in escape hatch)

## [0.9.0] - 2026-07-14

### Added
- WADO-RS metadata emits Pixel Data `BulkDataURI` (via godicom `dicomjson`) instead of omitting or inlining pixels
- `status` package: dcm4che-aligned DIMSE status constants (`status.Success`, `status.SOPClassNotSupported`, …); `dimse.Status*` aliases remain for compatibility

## [0.8.0] - 2026-07-13

### Added
- C-MOVE parallel destination stores via `MoveDestination.MaxAssociations` (fan-out across Storage associations)
- Storage Commitment async N-EVENT-REPORT on a new association via `EventReportRequest.AsyncDestination`

## [0.7.0] - 2026-07-13

### Added
- DIMSE-N N-GET / N-SET / N-CREATE / N-DELETE encode/decode + `ae.NGet` / `NSet` / `NCreate` / `NDelete` with SCP handlers `OnNGet` / `OnNSet` / `OnNCreate` / `OnNDelete` (pynetdicom golden fixtures)

## [0.6.0] - 2026-07-13

### Added
- User Identity Negotiation (PDU `0x58`/`0x59`): `pdu.UserIdentityRQ`/`UserIdentityAC`, `ae.Config.UserIdentity`, `ServerConfig.OnUserIdentity`, helpers `UsernameIdentity` / `UsernamePasscodeIdentity` (pynetdicom-aligned accept/reject + Kerberos/SAML/JWT AC response)

## [0.5.0] - 2026-07-13

### Added
- C-MOVE SCP performs real C-STORE sub-operations to Move Destination: `MovePlan`, `ServerConfig.MoveDestinations`, Move Originator AE/Message ID on outbound stores

### Changed
- **Breaking:** `OnCMove` now returns `MovePlan` (`Stores` + `Responses`) instead of `[]RetrieveMatch`

## [0.4.0] - 2026-07-13

### Added
- SCP/SCU Role Selection Negotiation (PDU `0x54`): `pdu.RoleSelection`, `ae.BuildRole`, `Config`/`ServerConfig.RoleSelections`, negotiated `AcceptedContext.AsSCU`/`AsSCP` (pynetdicom-aligned)
- C-CANCEL-RQ (`dimse.CCancelRQ`, `ae.Association.CCancel`); SCP peeks for cancel between C-FIND/C-MOVE/C-GET pending responses (status `0xFE00`)
- DIMSE-N Storage Commitment Push Model MVP: `N-ACTION` / `N-EVENT-REPORT` encode/decode + `ae.NAction` / `ae.NEventReport` with same-association event push (`OnNAction` / `OnNEventReport`)

## [0.3.0] - 2026-07-13

### Added
- WADO-RS Retrieve Rendered (instance-level JPEG/PNG via godicom pixel pipeline)
- WADO-RS Pixel Data bulkdata (`application/octet-stream`) client + origin-server routes
- PS3.18 HTTP error-path tests for rendered/bulkdata (404/406, missing PixelData, empty UIDs)

## [0.2.0] - 2026-07-13

### Added
- Phase 4 harden: DIMSE TLS (`Config.TLS` / `ListenAndServeTLS`), `IdleTimeout`, optional `slog` on AE + DICOMweb
- `dicomweb.NewClient` options (`WithTLSConfig`, `WithTimeout`, `WithLogger`, `WithHTTPClient`)
- Optional real-PACS soak: `go test -tags=integration ./ae -run TestIntegrationCEchoPACS`

## [0.1.0] - 2026-07-13

First tagged release — Phase 1–3 foundation.

### Added
- Phase 1 DIMSE: `pdu`, `dimse` C-ECHO, `ae` Association SCU + `CEcho`
- Phase 2 DIMSE: C-STORE / C-FIND / C-MOVE / C-GET SCU/SCP (godicom encode/decode)
- Phase 3 DICOMweb: `dicomweb` WADO-RS (study/series/instance + metadata), STOW-RS, QIDO-RS (studies/series/instances); `Handler` + `MemoryStore`
- Depend on godicom `v0.23.0`
- GitHub Actions CI (`go test -race`, `go vet`, `golangci-lint`)

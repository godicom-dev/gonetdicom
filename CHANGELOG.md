# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- `GetRequest.MessageID` and `MoveRequest.MessageID`, for the same reason
  `FindRequest.MessageID` already existed: C-CANCEL names the operation it
  cancels by Message ID, so a caller that wants to stop a retrieval from
  another goroutine has to be able to choose the id
- `ErrOperationInFlight`, returned by any operation started while another is
  still waiting for its answer, and by `Release` while any is running
- `docs/adr/0001-association-release-with-operations-in-flight.md` records the
  reasoning, quoting the PS3.8 7.2.2 requirements it is derived from
- SOP Class Common Extended Negotiation (PS3.8 D.3.3.6):
  `Config.CommonExtendedNegotiations` states per SOP Class which Service Class
  it is used under and which general classes it may be matched to,
  `ServerConfig.CommonExtendedNegotiation` confirms or withholds that statement,
  and `Association.CommonExtendedNegotiation` reads the answer. This is what
  Storage Commitment needs: the SOP Class a peer stores under and the Service
  Class that commits to it are different, and a requestor cannot commit to
  anything until it knows the pairing. Withholding the sub-item is not a
  refusal — the class stays usable the plain DIMSE way — so an SCP written
  before this item existed keeps negotiating exactly as it did
- `pdu.CommonExtendedNegotiation` carries the sub-item at the PDU layer, with
  each UID length-checked against the bytes actually present: every length in
  the item is a claim by the peer and is used as an offset
- `ae.QueryModelValidator` checks an identifier against the level hierarchy of
  the Query/Retrieve model it was written for, and `ae.QueryHierarchyFor`
  exposes that table (`QueryHierarchy`, `QueryLevel`, and the `LevelPatient` /
  `LevelStudy` / `LevelSeries` / `LevelImage` values). Three refusals a handler
  cannot recover from become 0xA900 without reaching it: a level the model does
  not answer (PATIENT on the Study Root model, SERIES on the retired
  Patient/Study Only model), a level outside the caller's allow-list, and a
  C-MOVE or C-GET that carries no unique key for the level it retrieves.
  C-FIND is exempt from the key rule — a STUDY level query by Patient Name is a
  legal query — and a model with no table is passed to the handler rather than
  refused on a guess, which keeps the long tail of specialised models usable.
  The table is structure, not a data dictionary: one attribute per level, the
  one that identifies the entity being retrieved (PS3.4 C.6.2.1.1)

### Changed
- `Release` no longer runs alongside an operation. Asking to release while a
  C-FIND/C-MOVE/C-GET is waiting for its answer returns `ErrOperationInFlight`:
  PS3.8 7.2.2 leaves a requestor nothing but A-ABORT after it asks, so a release
  cannot stop a running operation — cancel it, then release. Two operations
  called at once are refused for the same reason: one read side, one answer
  each, and the second caller used to steal the first one's responses
- `Release` skips inbound P-DATA while it waits for A-RELEASE-RP instead of
  failing. 7.2.2 says the requestor may receive P-DATA and may not answer it,
  so a peer that was mid-retrieve when the release was requested no longer
  turns an ordinary race into `unexpected PDU *pdu.PDataTF for release`. The
  four-iteration cap went with it; the loop is bounded by the caller's context
  and `IdleTimeout` like every other read
- `pdu.PDV.Value` decoded by `pdu.DecodePDataTF` is a subslice of the buffer
  that was passed in, not a copy. That buffer must outlive the PDVs and stay
  unmodified while they are in use — which is what `pdu.Read` already
  guarantees, since it allocates one buffer per PDU and never reuses it, so a
  caller that decodes from a reader notices nothing. A caller that decodes from
  its own buffer and then reuses it will now see that reuse through `Value`.
  The copy was the only reason a C-STORE needed the dataset twice in memory
  before the handler saw a byte

### Fixed
- A C-MOVE batch ran to the end after the requestor stopped it: the
  sub-operations run on the Move Destination's association, so the
  C-CANCEL-RQ or A-RELEASE-RQ waiting on the requestor's association was read
  only after the last instance had been stored, and the cancel was logged as
  late. The SCP now looks between two sub-operations — the one place it can —
  and either answers 0xFE00 with the untried instances as *remaining* rather
  than failed (PS3.4 Table C.4-3), or returns so the association loop can
  answer the release. The parallel path hands jobs over one at a time for the
  same reason: a pre-filled channel put the whole remainder beyond the reach of
  a stop that arrived after it was filled
- An A-RELEASE-RQ arriving between two C-GET sub-operations ended the
  association without the A-RELEASE-RP the peer was waiting for, so the peer
  could not tell a release from a network fault. The SCP now stops at the next
  sub-operation boundary and answers the release; a sub-operation that arrives
  after it is read and discarded, because the peer is entitled to ignore it
  (PS3.8 7.2.2) and a requestor that answers one commits a protocol violation of
  its own
- An association closed immediately after its A-RELEASE-RP could reach the peer
  as a reset rather than a release: closing a socket that still holds unread
  data makes the stack send RST, and a reset discards the peer's receive buffer
  — including the release response sitting in it. The SCP now drains whatever
  has already arrived before closing, with a deadline in the past so a peer that
  sent nothing costs two non-blocking reads and no latency. It cannot undo a
  requestor that keeps writing after A-RELEASE-RQ and races the close, but it
  removes the case where the late bytes were already there
- A C-CANCEL-RQ between two C-GET sub-operations was decoded as a C-STORE-RSP,
  which failed the association and left the SCU with neither a final response
  nor a usable connection. The store loop now recognises it, consumes the
  response it is still owed, and ends the operation at the next boundary
- A C-GET stopped by C-CANCEL answers 0xFE00 with the counts it reached and the
  untried sub-operations as *remaining*, not failed (PS3.4 Table C.4-3), and
  carries the Failed SOP Instance UID List when something failed

### Performance
- A P-DATA-TF decodes without moving its payload: a 16 KiB PDU takes 44 ns and
  56 B/op where it took 1966 ns and 16 KB, because decoding is now O(PDV count)
  rather than O(payload)
- `pdu.PDataTF.Encode` measures the body once and allocates the result once,
  where it used to build an item buffer per PDV, append them into a body, and
  copy the body behind a header. A 16 KiB PDU: 5713 ns / 51 KB / 3 allocs →
  2355 ns / 18 KB / 1 alloc
- C-STORE of 1 MiB end to end: 6.6 MB/op → 3.4 MB/op, 3.10 ms → 2.09 ms
  (338 → 503 MB/s). Three payload-sized buffers remain — the MCH prefix
  `FragmentMessage` prepends, the P-DATA buffer `Encode` writes, and the buffer
  `ReadLimit` fills — and none of them is written by the handler, which is why a
  500 MB store still costs one fragment of resident memory

## [0.20.0] - 2026-11-14

Concurrency control, done the way Go does it: admission and backpressure as
first-class mechanisms, rather than a thread pool borrowed from the JVM or a
worker queue borrowed from nowhere. dcm4che solves "how many at once" with
`Device.setExecutor` plus `setLimitOpenAssociations`, pynetdicom with one OS
thread per connection plus `AE.maximum_associations`; both are ways of making a
scarce resource survive. Goroutines are not that resource here, so the pool is
gone and what the pool was standing in for is explicit.

### Added
- Asynchronous Operations Window negotiation (PS3.8 D.3.3.3):
  `Config.AsyncOperationsWindow` proposes a window, `ServerConfig.AsyncOperationsWindow`
  sets what an SCP advertises, and `Association.AsyncOperationsWindow` reads the
  answer. An acceptor that receives a window but configures none answers 1/1 —
  the honest description of a dispatch loop that takes one message at a time —
  because PS3.8 requires an answer and silence leaves the requestor unable to
  tell whether pipelining is allowed. The negotiated value is also on `AssocInfo`
  for handlers
- SOP Class Extended Negotiation (PS3.8 D.3.3.5):
  `Config.ExtendedNegotiations` proposes per-class application information,
  `ServerConfig.ExtendedNegotiation` answers it per class (returning `ok=false`
  declines that class, which is how the protocol says "no extended semantics
  here"), and `Association.ExtendedNegotiation` reads the answer. The info is
  opaque to this package: only the service class that defines it can interpret
  it
- `ServerConfig.IdentifierValidator` and `ae.QueryRetrieveLevelValidator`: a
  C-FIND / C-MOVE / C-GET identifier is checked before the handler runs, and a
  non-zero status is answered as-is (0xA900 *identifier does not match SOP
  class*). PS3.4 puts that answer on the SCP, but what a valid identifier looks
  like is model-specific, so the rule is a hook and the ready-made check covers
  the level every model requires
- `ServerConfig.AcceptedTransferSyntaxes` and `ae.DefaultTransferSyntaxes`:
  transfer-syntax negotiation is now explicit. The default set is implicit VR
  little endian, explicit VR little endian, deflated explicit VR little endian
  and explicit VR big endian — pynetdicom's `DEFAULT_TRANSFER_SYNTAXES`, and
  what godicom can actually decode. `"*"` accepts whatever the requestor
  proposes, for a pass-through SCP that files bytes without decoding them
- `ServerConfig.Admit`: a per-association admission hook that receives
  `AssocInfo` and returns `(release func(), error)`. Returning a
  `*ae.RejectError` rejects with the exact A-ASSOCIATE-RJ result / source /
  reason; any other error rejects as transient / no-reason-given. The release
  func runs once when the association ends, including after a panic, so a quota
  taken at admission is given back. Blocking inside `Admit` is supported and is
  how a caller queues instead of rejecting: it runs on the association's own
  goroutine, and its context is cancelled at `HandshakeTimeout` or shutdown
- `ae.RejectError` and `ae.RejectLocalLimit`: the rejection as a value, with
  `Unwrap` for `errors.Is` / `errors.As`, instead of a call into the stack
- `ServerConfig.MaxDatasetSize` bounds the bytes one DIMSE message may carry,
  command set included. With `MaxConcurrentAssociations` it is an exact process
  memory bound (`associations × size`); there is deliberately no global byte
  budget, which would let one large sender stall every other association
- `StoreEvent.Stream`, and the reader behind it: an inbound dataset is no
  longer assembled into one buffer. Fragments travel from the socket to the
  handler over a channel with a single slot, so a 500 MB store costs one
  P-DATA fragment of resident memory per association, and a handler that stops
  reading stops the reader — backpressure with no queue and no watermark.
  dcm4che reaches the same place with `PDVInputStream` and a buffer the caller
  manages; pynetdicom reaches it not at all, reading the dataset into a
  `BytesIO`. `Bytes` and `Decode` drain the same stream for callers that want
  it whole. The SCU side streams too: C-GET hands each stored instance to
  `OnCStore` as it arrives

### Changed
- **Breaking**: `StoreEvent.Dataset []byte` is gone, replaced by `Stream()
  io.Reader`, `Bytes() ([]byte, error)` and `Decode() (*godicom.Dataset,
  error)`. Keeping a materialised buffer alongside a stream would mean paying
  the memory cost whether or not the handler reads it, which is the cost this
  change exists to remove. The body is single-use; the library finishes
  reading it when the handler returns, so a handler that ignores the dataset
  cannot desynchronise the association
- `MaxDatasetSize` is no longer what keeps memory bounded — `Stream` is. It
  remains as a cutoff for an object that is not plausibly real, and over the
  cap the association ends without a DIMSE response, because no status means
  "too big"
- A panic in a DIMSE handler now costs one association, not the process: it is
  recovered once at the association boundary, logged with its stack, and
  answered with A-ABORT. Handlers keep signalling expected failures with a
  DIMSE status; a panic means a bug, and the two are no longer the same thing.
  Precedent: `net/http`'s per-connection recovery in `conn.serve`
- `Serve` logs why an association ended at Debug, instead of discarding the
  error
- A presentation context is accepted only when one of its proposed transfer
  syntaxes is one we accept; previously the first proposal was taken whatever it
  was, so a peer could be told a JPEG 2000 context was usable and then have every
  store fail in the handler. Refused contexts carry result 4 (*transfer syntaxes
  not supported*), which is what pynetdicom returns and what tells a requestor to
  retry with another encoding rather than stop asking
- A command set that arrives intact but cannot be parsed now ends the
  association with A-ABORT. Closing the socket left the peer unable to tell a
  protocol error from a network fault, so it retried the same bytes
- A failed N-CREATE-RSP no longer has to name an instance: the UID is
  conditional (PS3.4 Table F.1-3), and inventing one for an object that does not
  exist is worse than leaving it out
- C-ECHO on a presentation context the acceptor never accepted now answers
  0x0122 (*SOP class not supported*) instead of success. The previous answer
  claimed an association the requestor cannot use was working, which is the one
  thing a verification request is asked to find out

## [0.19.0] - 2026-11-13

A dependency release ([godicom](https://github.com/godicom-dev/godicom)
`v0.34.0` → `v0.40.0`), the behavioural fixes from the commits since v0.18.0
that this file had not recorded, and two breaking API changes: the C-STORE
SCP that fixes issue #52, and the user identity callback.

### Added
- Association context for DIMSE handlers (issue #50): the SCP attaches an
  `AssocInfo` to the handler context — calling and called AE titles, remote
  address, the requestor's implementation Class UID and Version Name from its
  A-ASSOCIATE-RQ, and per message the abstract syntax, presentation context
  ID and negotiated transfer syntax — and the SCU-side C-GET / C-MOVE
  sub-operation store handlers attach it the same way, with the peer's
  address and implementation identification taken from the A-ASSOCIATE-AC.
  The shape follows
  pynetdicom's `Event.assoc` plus `Event.context` and dcm4che's
  `DimseRQHandler.onDimseRQ(Association, PresentationContext, ...)` pair.
  Handlers read it with `AssocFromContext` for device checks, per-AE
  authorization and audit logging
- A C-MOVE SCP handler can name the Move Destination itself: `MovePlan`
  carries a `Destination` (address and port), the first yield of pynetdicom's
  `EVT_C_MOVE` handler. Nil keeps the `ServerConfig.MoveDestinations` lookup;
  a non-nil `Destination` with an empty address makes the SCP answer
  0xA801, the same as yielding `(None, None)` there
- `StoreEvent.Decode` parses an inbound C-STORE's dataset bytes with the
  negotiated transfer syntax and reports failures as a normal error
- `ErrReleased` reports that the peer released the association while a request
  was outstanding. The release is honoured — an A-RELEASE-RP goes back — and
  the operation returns this instead of blocking for an answer that will never
  come

### Changed
- `OnUserIdentity` now receives the handler context:
  `func(ctx context.Context, req pdu.UserIdentityRQ) (bool, []byte)`. The
  association identity is attached before negotiation, so the callback can
  refuse an association by source address or peer implementation Class UID
  as well as by the identity item — pynetdicom's `EVT_USER_IDENTITY` event
  carries `event.assoc` and dcm4che hands its negotiator the `Association`
  for the same reason. Aligning the C-STORE SCP on `StoreEvent` below was
  the other half of the same release; both break compilation on purpose
- `OnCStore` (and the C-GET / C-MOVE sub-operation handler) now receives a
  `StoreEvent` instead of a `StoreRequest`. The old inbound path decoded the
  dataset eagerly and, on failure, handed the handler a silent nil `Data` —
  the worst combination, and one neither pynetdicom (lazy `event.dataset`,
  handler exceptions answered 0xC211) nor dcm4che (streamed
  `PDVInputStream`) shares. `StoreEvent.Dataset` carries the raw received
  bytes; decoding happens only when the handler calls `Decode`, so a
  handler that stores or forwards bytes pays no decode cost, and an
  undecodable store surfaces as an error the handler turns into a status
  (0xC211, *cannot understand*, mirrors pynetdicom). `StoreRequest` itself
  is now SCU-outbound only
- Depend on [godicom](https://github.com/godicom-dev/godicom) `v0.40.0`, up
  from `v0.34.0`. The releases in between add enhanced multi-frame
  functional group reading, atomic writes and write-side refusals (an
  integer its VR cannot hold, a Go type with no encoding for its VR), the
  File-set write side, the coded terminology (`sr`) and waveforms, a fix for
  reading an undefined-length pixel data value whose delimiter scan stopped
  three bytes short, the encapsulation of frames held in buffers
  (`EncapsulateBuffer` / `EncapsulatedBuffer`), the raw element hooks
  (`ReadOptions.Hooks`), and validation modes (`ReadOptions.ValueValidation`
  / `WriteOptions.ValueValidation`). This module calls none of the new API;
  the delimiter fix and the write-side refusals are what tighten what a
  C-STORE SCP accepts and writes

### Fixed
- A C-GET SCP answers with the outcome of its C-STORE sub-operations. The
  C-STORE-RSP status was decoded and dropped, so a retrieval whose stores all
  failed answered `0x0000` with whatever counts the handler had supplied. The
  final status is now `0x0000` / `0xB000` / `0xA702` by the rule in PS3.4
  C.4.3.1.3.2, the four Sub-Operation counts describe the transfers that
  happened, and a Warning or Failure carries the Failed SOP Instance UID List
  `(0008,0058)`
- A C-MOVE SCP counts a warning sub-operation as a warning rather than a
  failure. Collapsing them meant a Move Destination that stored every instance
  as a warning was reported as `0xA702`, *unable to perform sub-operations*,
  for a transfer that worked; the aggregate status and the Failed SOP Instance
  UID List follow the same rule as C-GET
- An SCU that receives an A-RELEASE-RQ while waiting for a response answers
  A-RELEASE-RP and returns `ErrReleased`. PS3.8 Table 9-10 makes the release
  valid in the P-DATA state; treating it as an unexpected PDU left the peer
  waiting for an RP that never came. `Release` also completes a release
  collision, where both sides send an A-RELEASE-RQ, instead of failing on the
  peer's RQ
- A C-STORE SCP matches the presentation context the way pynetdicom's
  `Association._get_valid_context` does for a store: an exact transfer
  syntax wins, and the uncompressed little-endian syntaxes (implicit VR,
  explicit VR, deflated) may substitute for one another, while a compressed
  syntax requires an exact match
- WADO-RS metadata writes the Transfer Syntax UID `(0002,0010)` into the
  file meta where it belongs rather than into the dataset

## [0.18.0] - 2026-10-23

### Added
- WADO-RS Retrieve Frames: `GET .../instances/{instance}/frames/{frames}` and
  `Client.RetrieveFrames`. Uncompressed frames are the stored pixel bytes.
  Compressed frames are the encapsulated bitstream in the instance's transfer
  syntax, not decoded pixels. The response is `multipart/related` of
  `application/octet-stream` and names that transfer syntax
- WADO-RS metadata includes Transfer Syntax UID `(0002,0010)` from the file
  meta, so a viewer can choose the decoder for those frames

### Changed
- Depend on [godicom](https://github.com/godicom-dev/godicom) `v0.34.0`. DICOM
  JSON now inlines a binary value when the BulkDataURI builder declines it, so
  metadata for an instance with private `UN` or `OB` elements succeeds while
  Pixel Data stays a BulkDataURI

## [0.17.1] - 2026-10-20

A dependency release: godicom `v0.30.0` → `v0.33.0`. Nothing in the exported
surface changed.

### Changed
- Depend on [godicom](https://github.com/godicom-dev/godicom) `v0.33.0`, up from
  `v0.30.0`. The three releases in between add `ReadOptions.OnElement`, palette
  color lookup (`ApplyColorLUT`, and `DisplayFrame` for `PALETTE COLOR`), a
  read-only DICOMDIR (`OpenFileSet`, `LoadFileSet`), and `Dataset.OverlayArray`.
  godicom `v0.31.0` also stopped `SpecificTags` from emptying the sequences it
  keeps: the filter applies to the top-level dataset only. This module does not
  call `SpecificTags`, so that change does not affect its behaviour

## [0.17.0] - 2026-09-04

A dependency release: godicom `v0.26.0` → `v0.30.0`. Four releases' worth of
fixes come with it, and it retyped the transfer-syntax parameters this package
feeds it, so the conversion is pinned at the `ae` boundary and the wire layers
stay godicom-free.

One exported symbol pair is removed, listed under Removed with the replacement.
Nothing else in the exported surface changed.

### Changed
- Depend on [godicom](https://github.com/godicom-dev/godicom) `v0.30.0`, up from
  `v0.26.0`. Four releases' worth of fixes arrive with it, and the 32-bit ones
  matter here: godicom's data dictionary answered the wrong VR for nearly every
  tag on a 32-bit platform, and `ParseTag` could not parse the item and delimiter
  tags from their hex-string form on any platform. Also read and write diagnostics
  (`ReadOptions.OnDiagnostic`, `WriteOptions.OnDiagnostic`), dictionary-VR
  setters, a data dictionary a caller can supply per read
  (`ReadOptions.Dictionary`, `NewPrivateDictionary`), and a fix for `AT` elements,
  which godicom wrote byte-swapped under little endian
- Transfer syntaxes are converted to `godicom.UID` where they cross into godicom
  (30 call sites in `ae`). godicom v0.27.0 retyped its transfer-syntax parameters
  from `string` to `uid.UID`; this package's `AbstractSyntax` and
  `TransferSyntax` fields stay `string`, which is what `pdu` puts on the wire and
  what `ae.UIDStrings` exists to feed them. `pdu`, `dimse` and `status` import no
  godicom at all — the wire layers are dependency-free on purpose — so the
  conversion belongs at the `ae` boundary and nowhere below it. No exported
  signature changes

### Removed
- **BREAKING: `ae.WaveformPresentationStateStorage` and
  `ae.WaveformAcquisitionPresentationStateStorage`.** They were spelled out here
  only because godicom's UID dictionary did not name them yet, and their doc
  comment said to drop them once it did — `TestStorageSOPClassesGodicomDoesNotName`
  watched for exactly that and fired on this bump. Use
  `uid.WaveformPresentationStateStorage` and
  `uid.WaveformAcquisitionPresentationStateStorage`; the UID strings are
  unchanged, and `AllStorageSOPClasses` still lists both SOP classes. The
  surviving test now treats an entry missing from godicom's dictionary as a
  failure rather than skipping it

## [0.16.0] - 2026-08-25

A robustness release. Most of it is the audit of the wire decoders, the SCP
association lifecycle and the DICOMweb handlers
([#39](https://github.com/godicom-dev/gonetdicom/issues/39)); the two
negotiation and C-MOVE fixes that head Fixed came out of following that work up
([#42](https://github.com/godicom-dev/gonetdicom/issues/42),
[#41](https://github.com/godicom-dev/gonetdicom/issues/41)).

No exported symbol was removed. Three behaviour changes are visible to callers
and are listed under Changed, each with the way to restore the old behaviour.

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

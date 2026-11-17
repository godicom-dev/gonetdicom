# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed
- Shutting a server down could close an association without the A-ABORT that
  was supposed to precede the close. The handshake watcher stopped by closing a
  channel that a goroutine `select`ed against `ctx.Done`, and a cancel that
  arrived before that goroutine was scheduled found both ready. `select` then
  closed the socket at random, the peer read EOF, and a shutdown looked like a
  network fault — the case the abort exists to prevent. The watcher now stops
  through `context.AfterFunc`, which either closes or steps aside as a whole
- A C-STORE whose pre-encoded bytes are not in the syntax of the context that
  carries them is refused with `ae.ErrTransferSyntaxMismatch`, where it used to
  go out mislabelled. The two little-endian syntaxes negotiate for each other —
  they differ only in the VR field — so a peer that accepted Explicit VR Little
  Endian alone was sent Implicit VR bytes and told they were Explicit VR. The
  receiver decodes under the context's syntax, so a strict peer fails an
  instance that was never actually wrong and a lenient decoder (including
  godicom's) hides the damage until the instance is read back. `StoreRequest.Data`
  is the conversion: a godicom Dataset is encoded into whatever the context
  declares, which is also why the check only covers bytes the caller encoded
- A C-GET sub-operation whose transfer syntax no accepted context carries is
  counted failed and reported in the failed count, instead of ending the
  association. PS3.4 Table C.4-3 asks the SCP to report the instances it could
  not deliver; one instance the peer may retry is a better answer than a
  retrieval that stops without one
- Study and series searches walk their instances in UID order. The response may
  now carry the whole stored dataset (`includefield=all`), and which instance's
  copy a study or series reported used to be whichever a Go map yielded first
- C-GET stores and N-EVENT-REPORT payloads pick their presentation context by
  context ID among the candidates. The choice used to be whichever entry a Go
  map yielded first, so an association proposing one SOP Class in two storage
  syntaxes sent the stores in a syntax that varied between runs of the same
  program

### Added
- `pdu.FuzzFragmentReader`, which asserts what a fragmenter must not get wrong —
  the payloads reassembled from the emitted PDVs are the input, every PDV fits
  the length the peer stated, and the two encoders produce the same bytes — over
  generated command, dataset and PDU lengths. It joins the four targets the fuzz
  job runs, on the encoder every store now goes through
- `TestRetrieveSoak`, three two-hundred-instance retrievals on one association,
  asserting the counts are identical every round and that the goroutines are back
  where they started. The failures this kind of loop has — a drifting count, a
  goroutine left behind per operation, a response one store late — are the ones a
  single pass cannot see. It is skipped under `-short`
- A `bench` job in CI: every benchmark runs once, and nothing is asserted about
  the numbers. PARITY quotes timings and allocations, and a benchmark that stops
  compiling is how those claims go stale without anyone noticing
- `examples/`, a runnable program per service: `echo`, `store`, `find`, `get`,
  `move` and `dicomweb`, each starting its own SCP on a loopback port so that
  `go run ./examples/find` shows both ends of the association with nothing to
  configure. This is the answer to pynetdicom's eight command-line tools that
  PARITY used to name as the largest absence: a CLI would have to decide an
  argument syntax, a file layout and a configuration format on the user's behalf,
  while an example shows the API doing the work
- Retrieve Rendered at study and series level: `GET .../studies/{study}/rendered`
  and `.../studies/{study}/series/{series}/rendered`, with
  `Client.RetrieveRenderedStudy` and `RetrieveRenderedSeries` to ask for them.
  The answer is `multipart/related` of one image per instance, and an instance
  with nothing to render — an SR, a PR, an image whose Pixel Data was never sent
  — is left out rather than failing the response, because a study is a mixed bag
  and a 406 for the whole of it because one series holds a report would make the
  transaction useless on a real archive. A resource where *nothing* renders is
  406, the same answer an unrenderable single instance gets
- The Search parameters that are about the answer rather than the match, in
  `dicomweb.MemoryStore`: `limit`, `offset`, `fuzzymatching` and `includefield`
  (`all`, a keyword, a tag, or a comma-separated list of either, repeated across
  parameters as the standard allows). `limit` and `offset` trim an ordered result
  set; `fuzzymatching` makes the free-text VRs match on a case-insensitive
  substring while dates, numbers and UIDs stay exact, because `2026` is not a
  fuzzy `20260101`; `includefield` adds attributes to the level's projection, and
  `all` returns the stored dataset as it is. The reference store now keeps the
  whole dataset for that last one, where it used to index ten attributes and
  could not answer any other name
- `dicomweb.ErrBadQuery`, which a `Store` returns when a Search parameter cannot
  be honoured — `limit=ten`, a negative `offset`, an `includefield` that names no
  attribute, a `fuzzymatching` that is not a boolean — and which `Handler`
  answers with 400 instead of 500. A store that returned nothing for a
  parameter it did not understand answered with a different result set than the
  one asked for, and a requestor cannot tell that from a smaller archive
- `ServerConfig.RequiredCallingAETitles`, the calling titles an SCP serves: any
  other requestor is rejected with A-ASSOCIATE-RJ (calling-AE-title-not-
  recognized). Empty is the default and serves any peer that names itself, which
  is pynetdicom's `require_calling_aet` unset. It is configuration rather than an
  `Admit` closure because it is the protocol's own refusal — nothing runs,
  nothing is counted, and the peer is told why in the PDU that ends the
  negotiation — while `Admit` stays for decisions that need more than a name
- `ae.EventReportRequest.TransferSyntax`, which states what
  `EventInformation`'s bytes are, so the same rule applies to an event report as
  to a store: bytes must match the context that carries them, and a godicom
  Dataset is encoded into it
- `gonetdicom.AttrSOPInstance` and `gonetdicom.AttrTransferSyntax`, the log
  attributes the refusal above and the counted sub-operation failure carry

## [0.22.0] - 2026-11-15

A retrieval no longer exists as a batch. The three handlers that answer with a
set — C-FIND's matches, C-GET's and C-MOVE's sub-operations — return iterators
now, so a match is encoded and written before the next one is produced, and a
sub-operation is stored before the handler resumes with the peer's answer in
hand. What that buys is not a smaller number: a five-hundred-instance retrieval
holds one instance instead of five hundred, a C-CANCEL is noticed between two
items because the handler's own loop is what notices it, and a handler that
counts successful stores counts the ones that happened rather than the ones it
offered. `iter.Seq2` is the Go spelling of the generator pynetdicom's C-FIND SCP
has always been — yielding is ranging over a function, a yielded error ends the
operation the way a raised exception does, and a yield that returns false is
GeneratorExit — and `docs/adr/0002` is the reasoning, including why the C-MOVE
fan-out went with it: `op.Outcome` is written after the store and before the
yield returns, so a handler cannot resume with a store still in flight. The
sending side got the same treatment: `StoreRequest.Source` reads the dataset one
fragment at a time as the C-STORE-RQ goes out, so an instance that is already a
file on disk never becomes a `[]byte` at all, and the two encoders are pinned
against each other so the wire format did not change. The rest of the release is
the server describing itself: `AcceptedContexts` states what a SOP Class is
served in and what it is passed through in, `RejectedContexts` and
`NoContextError` say why a context was refused instead of leaving the requestor
to guess, and `NewServer` keeps the registry that makes a shutdown mean every
association rather than the listener.

### Added
- `ae.NewServer`, an SCP that keeps a registry of the associations it is serving:
  `Associations()` returns them, `ActiveAssociation.Abort()` ends one with
  A-ABORT, `AbortAll()` ends every one, and `Info()` reports the AE titles and
  peer identification the association negotiated (with `ok=false` while it is
  still negotiating). `Serve(ctx, ln, cfg)` is unchanged and runs the same code
  with the registry off. pynetdicom exposes the same three things as
  `AE.active_associations`, `Association.abort` and `AE.shutdown`
- `ServerConfig.AcceptedContexts`, which states the accepted presentation
  contexts a SOP Class at a time with its own transfer-syntax preference
  (pynetdicom's `add_supported_context(uid, transfer_syntaxes)`). When set it
  replaces `AcceptedAbstractSyntaxes` and `AcceptedTransferSyntaxes`, because a
  class-at-a-time list is the more specific statement. This is what lets an SCP
  decode some classes and pass the rest through: implicit VR for the ones it
  parses, `"*"` for the ones it files untouched. An unlisted class is answered
  with result 3, an unlisted syntax with result 4, and a `"*"` entry covers the
  classes the list does not name
- `Association.RejectedContexts()`, the contexts the acceptor refused with the
  result it gave, so a requestor can tell "this peer does not serve that class"
  (3) from "it does, but not in a syntax either of us offered" (4). The two used
  to be indistinguishable: both arrived as a missing context
- `ae.NoContextError`, returned by `Dial` when the acceptor accepted nothing. It
  names each refused class and the reason, and unwraps to `ErrNoContext`, so
  `errors.Is` still works for callers that only checked that
- `ae.StoreRequest.Source`, which sends a dataset that never has to be resident:
  `Open()` returns a reader and its length, and the bytes are read one fragment
  at a time as the C-STORE-RQ goes out, so a 500 MB instance costs one fragment
  of memory on the sending side. `ae.FileSource` is the archive case — a path, an
  offset and a length, which is a Part 10 file minus its preamble and file meta
  group — and `ae.SourceFunc` adapts a function, as `http.HandlerFunc` adapts a
  handler. `Data` and `Dataset` still take precedence, and the wire format is
  unchanged: the store goes out through the new `pdu.FragmentReader`, which emits
  what `pdu.FragmentMessage` builds, one PDU at a time instead of all of them
  first. A source that ends before its declared length is `io.ErrUnexpectedEOF`
  rather than a short instance the peer would store, and an `Open` that fails
  leaves nothing on the wire, so the association is still usable. This is the
  sending half of #51; see `docs/adr/0002-handler-contract-iterators.md` §5

### Changed
- **Breaking.** `ServerConfig.OnCGet` returns `iter.Seq2[*ae.RetrieveOp, error]`
  for the same reason `OnCFind` does, and with more to gain: the handler states
  the sub-operation count (`ae.RetrievePlan{Total}`), then offers one `Store` at
  a time, and the framework performs the store on the association and writes the
  peer's C-STORE-RSP back into `op.Outcome` before the yield returns. A
  five-hundred-instance retrieval therefore produces one instance at a time
  instead of materialising five hundred `StoreRequest`s, and the handler learns
  what actually happened to each of them — which pynetdicom's C-GET cannot
  report, because there the handler calls `send_c_store` itself and the
  framework only tallies what it is told. `ae.PlannedGet(plan)` and
  `ae.PlannedOps(plan)` adapt the old return value in one line. The counts and
  the aggregate status of every response are now the measured ones
  (`subOperations.reconcileMatch`), a store offered before the count is
  `0xC413`, more than 65535 sub-operations is `0xC416`, and a handler error is
  `0xC411`
- **Breaking.** `ServerConfig.OnCMove` returns `iter.Seq2[*ae.RetrieveOp, error]`,
  the C-GET contract plus the two things only C-MOVE needs. Its sub-operations
  run on a second association, so `ae.RetrievePlan` now carries the
  `Destination` and the `Contexts` the stores will be sent in. Both used to be
  derived from the batch, which an iterator does not have, and an
  A-ASSOCIATE-RQ has to name every presentation context before the first
  C-STORE can be proposed — so the handler declares them, which is the same
  information pynetdicom takes from `ApplicationEntity.requested_contexts`
  (`ae.py:associate`, which raises when that list is empty). A plan that names
  stores without contexts is answered with `0xC513`. Everything else follows
  C-GET: the store goes on the wire before the handler resumes, `op.Outcome`
  carries the destination's answer, and a handler error is `0xC511`.
  `ae.PlannedMove(plan)` and `ae.PlannedMoveOps(plan)` adapt the old return
  value, deriving the contexts from the plan's stores as before
- **Breaking.** `ae.MoveDestination.MaxAssociations` is gone. C-MOVE
  sub-operations run one at a time on one association, which is what pynetdicom
  does (`service_class.py:2144` opens one and `:2354` stores through it). The
  fan-out and the write-back are alternatives rather than complements: a handler
  that resumes with `op.Outcome` filled in cannot have a store still in flight.
  Fanning out also required the whole batch in memory — `make([]StoreRequest,
  len(stores))` — which is exactly what #51 is about. Restoring the parallelism
  needs a batched operation (yield a slice, read back a slice of outcomes) and
  its own ADR
- Every completed sub-operation is answered with a Pending response carrying the
  measured counts, for C-GET and C-MOVE alike, as pynetdicom does
  (`service_class.py:1983-1989`, `:2378-2384`). A C-MOVE plan also gets an
  opening Pending that carries the whole of `Total`, which is what this package
  sent before the batch started. A handler that wants a progress response of its
  own still yields a Pending `Result`
- C-MOVE counts past 65535 saturate rather than being refused with `0xC516` the
  way pynetdicom does (`:2132`). The ceiling is real — the four counts are US —
  but refusing a 65536-instance move is a worse answer than a `Remaining` that
  stops moving, and this package chose saturation before the iterator contract.
  The saturation is now logged with the count that was requested
- **Breaking.** `ServerConfig.OnCFind` returns `iter.Seq2[ae.FindMatch, error]`
  rather than `[]ae.FindMatch`: the handler is the Go spelling of pynetdicom's
  C-FIND generator, so a match is encoded and written before the next one is
  produced and a result set is never resident as a whole — the streaming half of
  #51, with the reasoning in `docs/adr/0002-handler-contract-iterators.md`. A
  handler that already holds its matches returns `ae.FindSeq(matches)`, or the
  whole handler becomes `ae.FindMatches(matches)`; one that produces them
  incrementally ranges over its own source and yields, and stops when the yield
  reports that nothing is listening. Three behaviours came with it: a handler
  error is answered with `0xC311` and an Identifier that will not encode with
  `0xC312` instead of ending the association, an operation whose handler yielded
  no terminal response ends with Success as the reference does
  (`service_class.py:398-402`) instead of being left open, and the handler's
  context is cancelled the moment the peer cancels, so `context.Cause` reports
  `ae.ErrPeerCancelled` or `ae.ErrPeerReleased` to a call that is blocked rather
  than a flag to poll
- `Serve` returns after the last association has ended rather than when the
  listener closes. Cancelling the context already closed every association;
  returning earlier left handlers running behind a function that had returned,
  which made "shutdown" mean "stop accepting" and nothing more. A listener that
  fails for another reason still returns immediately, because associations
  already running own their connections
- A cancelled server ends an established association with A-ABORT instead of
  closing its socket. A close is indistinguishable from a pulled cable, so a
  peer that sees one retries into a server that is not there; A-ABORT is the
  protocol's own way of saying the association is over, and it is what
  pynetdicom's `AE.shutdown` does. A connection that has not sent an
  A-ASSOCIATE-RQ yet is still just closed — there is no association to abort

## [0.21.0] - 2026-11-15

Failure, said precisely. Three different things used to be reported as one: a
peer that sent bytes that do not decode was answered by closing the socket,
which says the network broke — so the peer retried the same bytes against a
server that would refuse them identically; a release asked for while an
operation was mid-answer raced that operation for the read side; and a deadline
that expired inside a PDU ate the rest of the PDU, leaving a length prefix in
the payload and nothing to do but abort. Now an undecodable PDU is A-ABORT, a
release is `ErrOperationInFlight` until the operation is answered or cancelled,
and the SCU reads one PDU at a time on its own goroutine, so a caller that stops
waiting leaves the stream framed and can still release. The same precision
reached the tables: `ae/serviceclass.go` states which presentation contexts
serve a SOP Class and which statuses it may answer with — the two questions a
peer actually asks — rather than each application keeping its own literal list,
and a response whose status the class does not define is logged and sent
unchanged instead of being silently improved. DICOMweb gained the Non-Patient
Instance Service and the two top-level Search targets, so the archive can be
read at the level above a study as well as below one.

### Added
- The DIMSE service classes as a table: `ae.ServiceClassOf` names the class a SOP
  Class UID belongs to, a class returns its SOP Classes
  (`ae.ServiceClassPrintManagement.SOPClasses()`), and `ae.ServiceClasses` lists
  all 23. This is pynetdicom's `uid_to_service_class` and `_*_CLASSES`, and it
  answers the question a presentation context list poses — which UIDs make up
  MPPS, or print management, or storage commitment — without a literal list in
  every application. A UID no class claims is `ae.ServiceClassUnknown`, the same
  answer pynetdicom gives by returning its base `ServiceClass`. The two Service
  Class UIDs are answered but appear in no list, because they are not abstract
  syntaxes. Storage names the existing `AllStorageSOPClasses` rather than a
  second copy of the same UIDs
- `ae.ServiceClass.HasStatus`, the other half of that table: which statuses a
  service may answer with. It carries the general list of PS3.7 Annex C and the
  twelve class annexes of PS3.4 — Storage (B.2.3), C-FIND / C-GET / C-MOVE (C.4),
  worklist (K), relevant patient (Q), substance administration (V), non-patient
  storage (GG), procedure step, print job management (H), application event
  logging (I), UPS (CC) and RT machine verification (DD). The operation is part
  of the question for Query/Retrieve, where 0xB001 is a C-FIND warning and
  0xA801 a C-MOVE failure; an empty `ae.RetrieveOperation` answers the union. An
  unknown SOP Class answers the general list, as pynetdicom's base `ServiceClass`
  does. The annexes' ranges are kept as ranges rather than expanded into four
  thousand entries, and `TestStatusSetsMatchPynetdicom` compares the result with
  `status.py` code for code. Three tables in that file are defined and attached
  to nothing upstream — the Annex K worklist set, because
  `BasicWorklistManagementServiceClass` answers the C-FIND set, and the Media
  Creation and Storage Management sets, because neither class sets `statuses` —
  so they are not copied here
- `dimse.PeekResponseInfo` reads the Command Field, the Status and the Affected
  SOP Class UID of a response in one pass. C-ECHO-RSP names the Verification SOP
  Class, so the SOP Class is there to ask about even for a command a peer never
  sends without a context
- `gonetdicom.AttrSOPClass`, the log attribute the warning below carries
- `pdu.ErrUndecodable`, which marks a PDU that arrived in full but does not
  parse — an unknown type, or a body its type does not explain — and nothing
  else: a read that ends early stays an I/O error, because a peer that
  disconnected has not sent garbage and should not be told that it did. It exists
  so the association layer can tell the two apart and answer the first with
  A-ABORT
- The two top-level Search targets of the Studies Service, "All Series"
  (`GET /series`) and "All Instances" (`GET /instances`): `Handler` answers both,
  and `Client` gained `SearchAllSeries` and `SearchAllInstances`. They are
  separate methods rather than `SearchSeries` with an empty UID, because a study
  UID that arrives empty from a variable is a bug and turning it into "return the
  archive" would answer instead of refusing. On the server side the empty UID is
  now a stated contract: it is the absent level of the URI, so
  `Store.SearchSeries("")` must return every series the store holds — a store that
  returns nothing there makes the server answer `204` to a request it could have
  answered, which reads as an empty archive
- A study or series above the searched level is now a query key as well as a
  level: `/series?StudyInstanceUID=` and `/instances?SeriesInstanceUID=` narrow to
  one parent, which is the only way to ask for it when the URI names none. Those
  keys were silently ignored before, so a filtered request returned a superset the
  requestor had no way to notice
- The Non-Patient Instance Service (PS3.18 section 12): `Handler` serves Retrieve,
  Store and Search on the six resource names — colour palettes, hanging
  protocols, implant templates, defined procedure protocols, protocol approvals,
  inventories — and `Client` gained `RetrieveNonPatientInstance`,
  `RetrieveNonPatientInstanceFile`, `SearchNonPatientInstances`,
  `StoreNonPatientInstances`, `StoreNonPatientInstance` and
  `StoreNonPatientFiles`. The backing store is a second pair of interfaces rather
  than three more methods on `Store`: `NonPatientRetriever` is what a read-only
  archive implements, `NonPatientStore` adds the Store Transaction that PS3.18
  12.1 makes optional, and a `Store` implementing neither serves no non-patient
  resources at all — the paths answer 404, like any path the server does not have.
  A read-only implementation answers POST with 405 and `Allow: GET` instead, so a
  user agent can tell "no such resource" from "not accepted here". The instance
  segment of the path is checked against UID syntax before the store is called,
  because joining category and UID onto a directory is the ordinary way to write a
  store and `..` would then name the parent directory
- `dicomweb.NonPatientCategories()` returns the six resource names of that
  service, in standard order, as a copy: they decide which paths the server
  answers, so a caller that truncated the slice would remove a resource
- `GetRequest.MessageID` and `MoveRequest.MessageID`, for the same reason
  `FindRequest.MessageID` already existed: C-CANCEL names the operation it
  cancels by Message ID, so a caller that wants to stop a retrieval from
  another goroutine has to be able to choose the id
- `ErrOperationInFlight`, returned by any operation started while another is
  still waiting for its answer, and by `Release` while any is running
- `ErrIdleTimeout`, returned when no PDU arrives within `Config.IdleTimeout`.
  It bounds the wait rather than the association: the read side keeps whatever
  the peer eventually sends, so the next call — or `Release` — can still use
  the connection
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
- A response whose status the SOP Class does not define is logged at warn level
  and sent unchanged, so a handler that answers a C-FIND with a C-MOVE status
  leaves that status on the wire and the warning in the log. pynetdicom does the
  same (`service_class.py`, "Unknown status value returned by callback"); a
  package that substituted a status of its own would report the handler's bug to
  the peer as a protocol violation the peer did not commit. The check is in
  `writeMessage`, so it covers every response this package sends — including the
  C-STORE-RSPs a C-GET SCP answers, whose SOP Class is the stored one, not the
  one the request named
- A PDU that arrives whole but does not decode — an unknown type, or a body its
  type does not explain — is answered with A-ABORT (source 0x02, reason 0x01
  *unrecognized PDU*) instead of a closed socket. Both sides do it, and the SCP
  does it before an association exists too. Closing the connection said the
  network broke, so the peer retried the same bytes against a server that would
  refuse them identically; `Release` now returns `ErrAborted` afterwards rather
  than sending an A-RELEASE-RQ on an association the peer was told to forget.
  This is the rule pynetdicom's DUL applies to any exception while receiving
  (`dul.py`, `test_dul.py::test_unknown_pdu_aborts`)
- The SCU read side runs on its own goroutine, one PDU at a time, where it used
  to read in the caller's goroutine under a deadline taken from the caller's
  context. A deadline that expired in the middle of a PDU therefore consumed
  part of it and lost the rest: the next read took a length prefix out of
  payload, everything after it was garbage, and `Abort` was the only way out.
  Now the goroutine owns the PDU it started and finishes it, so the caller only
  stops waiting — a deadline reports `context.DeadlineExceeded`, the stream
  stays framed, and `Release` still works (it skips the P-DATA and reaches the
  A-RELEASE-RP). What is unchanged is that an abandoned operation leaves its
  remaining responses in the stream: stop the operation, then release
- `IdleTimeout` bounds the wait for the next PDU instead of the socket read, for
  the same reason: a deadline on the socket can stop a read halfway through a
  PDU. The wait now fails with `ErrIdleTimeout` and the association is
  untouched — a peer that was merely slow can still be read, or released
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

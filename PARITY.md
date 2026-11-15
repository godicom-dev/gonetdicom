# Parity with pynetdicom

This is the working ledger behind "how much of pynetdicom is covered here".
It exists because counting test functions is misleading: pynetdicom has 2078
`def test_*` functions and a large share of them assert the shape of the Python
implementation rather than anything a peer can see — 273 in `test_fsm.py` drive
a state table that a Go stack does not have, and `test_dimse_provider.py`,
`test_events.py`, `test_primitives.py`, `test_timer.py` and `test_cli.py` test an
object model, an event bus, a thread and a command line.

The useful unit is a **behaviour**: a rule about the wire, an error a peer can
observe, or a limit an operator can set. Status below is per behaviour group,
not per upstream test, so "out of scope" always names the upstream tests and
says what they assert instead.

Legend: **covered** — a test would fail if the behaviour regressed;
**partial** — the main path is tested, listed gaps are not;
**out of scope** — the upstream tests assert the shape of pynetdicom's own
objects, and the behaviours underneath them are either covered elsewhere in this
table or not behaviours at all.

## Where the numbers are

| Package | Statements | Notes |
|---|---|---|
| `status` | 100.0% | pure constants and predicates |
| root | 95.8% | logger precedence, nil tolerance |
| `dimse` | 90.4% | command encode/decode, status helpers |
| `pdu` | 85.9% | every PDU and user-information item, incl. malformed frames |
| `ae` | 85.3% | association, DIMSE dispatch, Q/R, N-services, the service class and status tables, the server registry |
| `dicomweb` | 81.3% | no pynetdicom counterpart (PS3.18 is not in it); Studies and Non-Patient Instance services |

`go test ./... -cover` reproduces the table. Cross-package use does not count
toward the callee's number, so `pdu` is understated: `FragmentMessage` and the
control-PDU decoders are also driven through `ae`.

## Module-by-module

| pynetdicom module (tests) | Our counterpart | Status |
|---|---|---|
| `test_pdu.py` (114), `test_pdu_items.py` (191) | `pdu/pdu_test.go`, `pdu/control_test.go`, `pdu/userinfo_test.go`, `pdu/read_test.go`, `pdu/toolong_test.go`, `pdu/lengths_test.go` | covered |
| `test_dimse_c.py` (47), `test_dimse_msg.py` (25), `test_dimse_n.py` (48) | `dimse/*_test.go` (12 files) | covered |
| `test_status.py` (22) | `status` (100%), `dimse/status_test.go` | covered |
| `test_service_verification.py` (15) | `ae/handshake_test.go`, `ae/assoc_test.go` | covered |
| `test_service_storage.py` (24) | `ae/store_*_test.go`, `ae/stream_test.go`, `ae/subops*_test.go` | covered |
| `test_service_n.py` (51), `test_assoc_n.py` (84) | `ae/n_status_test.go`, `ae/n_test.go`, `ae/action_test.go`, `ae/nevent_test.go` | covered |
| `test_service_qr.py` (149) | `ae/find_test.go`, `ae/retrieve_test.go`, `ae/move_*_test.go`, `ae/subops_e2e_test.go`, `ae/validate_test.go`, `ae/qrmodel_test.go`, `ae/model_passthrough_test.go` | covered — including the per-model level tables (see gap 1) |
| `test_presentation.py` (99) | `ae/contexts_test.go`, `ae/pc_negotiation_e2e_test.go`, `ae/role_*_test.go`, `ae/server_test.go` | partial — the negotiation half is covered; `build_context` and the 22 per-service context lists have no counterpart (see "What is still missing") |
| `test_acse.py` (85) | `ae/handshake_test.go`, `ae/acse_robustness_test.go`, `ae/admit_test.go`, `ae/identity_test.go`, `ae/negotiation_test.go` | partial — see below |
| `test_ae.py` (102) | `ae/scp.go`/`Config`/`ServerConfig` + the tests above | partial — see below |
| `test_assoc.py` (213) | `ae/assoc_test.go`, `ae/release_test.go`, `ae/negotiation_test.go` | partial — see below |
| `test_assoc_user.py` (111) | `ae/identity_test.go`, `ae/negotiation_test.go` | partial — see below |
| `test_transport.py` (75) | `ae/tls_test.go`, `ae/limits_test.go`, `ae/pdulength_test.go` | partial — see below |
| `test_logging.py` (35) | `logger_test.go`, `logger_nil_test.go`, `ae/log_test.go` | covered |
| `test_dul.py` (10), `test_fsm.py` (273) | `ae/acse_robustness_test.go`, `ae/malformed_test.go`, `ae/undecodable_test.go` | partial — see below |
| `test_primitives.py` (50), `test_events.py` (23), `test_dimse_provider.py` (22) | none | out of scope — these assert that Python objects hold the values they were given and that `evt.trigger` calls the bound handler; the corresponding behaviour here is a function call |
| `test_timer.py` (7), `test_cli.py` (2), `test_environment.py` (2) | none | out of scope — a thread-based TIMER, a command line, and version/env lookup; `HandshakeTimeout` and `IdleTimeout` are the timeouts that reach the wire |
| `test_sop.py` (67), `test_service_class.py` (13) | `ae/serviceclass_test.go`, `ae/serviceclass_status_test.go`, `ae/scp_cancel_move_test.go` | partial — the `SOPClass` type and the dataset half of `validate_status`; see gap 7 |
| `test_dsutils.py` (56) | godicom | out of scope — dataset work lives in the dataset library |
| `test_service_relevant_patient.py` (22), `test_service_non_patient.py` (12), `test_service_substance_admin.py` (8) | `ae/model_passthrough_test.go` | covered as pass-through — named models exported, no per-model validation |
| `test_utils.py` (18), `test_validators.py` (3) | none | partial — see "Validation" below |
| `apps/tests/` (229) | none | out of scope — the eight command-line tools; see "What is still missing" |

Tally over the 34 upstream test modules: 12 covered, 3 covered as pass-through,
12 partial, 7 out of scope. The out-of-scope set is 162 tests, and every one of
them asserts something about pynetdicom's own objects: the event bus, the
dataset helpers, the CLI framework, the ARTIM timer. Two blocks that used to be
listed there — the state machine (`test_fsm.py`, `test_dul.py`) and the SOP Class
registry (`test_sop.py`, `test_service_class.py`) — are now partial, because part
of what they assert does reach the wire; see gaps 6 and 7. `test_presentation.py`
moved the other way: its negotiation half is covered, but its per-service context
lists are not there at all, and calling that "covered" hid the absence.

`pynetdicom/apps/` is outside those 34 modules and outside the library: 4322
lines of CLI over 6939 lines of end-to-end tests. It is the largest single thing
this repository does not have, and it is not a library feature — see below.

## The gaps that remain, in order of what a peer would notice

### 1. Query/Retrieve identifier validation (covered by hook)

`ServerConfig.IdentifierValidator` runs before the handler for C-FIND / C-MOVE /
C-GET and answers whatever status it returns; `ae.QueryRetrieveLevelValidator`
is the ready-made check (level present, optional allow-list) that replies 0xA900
(*identifier does not match SOP class*). A refused identifier skips the handler
and leaves the association usable.

`ae.QueryModelValidator` adds the part that needs a table: each model's level
hierarchy and the attribute that identifies an entity at each level (PS3.4
C.6.2.1.1, exposed as `QueryHierarchyFor`). Three refusals follow, all of them
mistakes a handler could not recover from: a level the model does not answer
(PATIENT on Study Root, SERIES on the retired Patient/Study Only model), a level
excluded by the caller's allow-list, and a C-MOVE or C-GET that carries no
unique key for the level it retrieves. C-FIND is exempt from the key rule —
a STUDY level query by Patient Name is legal — and a model with no table (the
Relevant Patient Information models, the specialised Query/Retrieve models) is
passed to the handler rather than refused on a guess.

The table is structure, not a data dictionary: one attribute per level, chosen
because it is the one that identifies the entity being retrieved. What
pynetdicom's dataset validation also does — the required and optional key sets
per model and level, including the "required if" conditions, and the VR/value
multiplicity checks — is a data dictionary, and is still the application's
business. `QueryModelValidator` is the seam for it: an application that wants
the full table writes the validator, calls `QueryHierarchyFor` for the shape,
and answers 0xA900 for the rest.

### 2. Association lifecycle corners (partial)

Covered: accept/reject, release, release collision on the SCU, peer release
while a request is outstanding, A-ABORT both directions, abort as first PDU,
truncated RQ, repeated rejections, association with no contexts, release while
P-DATA is still arriving at the requestor, release requested mid-C-GET, release
and C-CANCEL mid-C-MOVE, and C-CANCEL at a sub-operation boundary in both orders
(`ae/release_inflight_test.go`, `ae/scp_stop_midretrieve_test.go`,
`ae/scp_cancel_move_test.go`). The rules behind the last four are in
`docs/adr/0001`.

Also covered: a PDU that arrives whole but does not decode — an unknown type, or
a body its type does not explain — is answered with A-ABORT (source 0x02, reason
0x01 *unrecognized PDU*), on both sides, including before an association exists
(`ae/undecodable_test.go`). The distinction that makes this possible is
`pdu.ErrUndecodable`: a read that ends early is a connection that ended and is
not marked, so the association layer closes in that case and aborts in this one.
pynetdicom reaches the same place from the other direction — its DUL reactor
catches every exception while receiving and sends A-ABORT with reason 0x00
(`dul.py`), and `test_dul.py::test_unknown_pdu_aborts` /
`::test_recv_bad_pdu_aborts` assert the association ends up aborted.

Shutdown is the same statement in the other direction: cancelling the server's
context ends every established association with A-ABORT
(`TestServeCancellationEndsEstablishedAssociation`), closes connections that
have not associated yet (`TestServeCancellationEndsConnectingClient`), and
`Serve` returns only once the last handler has finished. pynetdicom's
`AE.shutdown` aborts each active association before shutting its servers down,
for the same reason: a closed socket is what a peer cannot tell apart from a
network fault, and therefore the case it retries.

Not covered: `network timeout` on a half-open socket (needs a controllable
clock) and ARTIM timer expiry measured in seconds (we use `HandshakeTimeout`
deadlines instead, which is the same guarantee by a different mechanism).

### 3. Transport (partial)

Covered: TLS accept and dial, Maximum Length Received advertised and honoured,
tolerance for PDUs larger than we advertised, IPv4, IPv6, idle and handshake
timeouts.

IPv6 is covered as a property rather than a feature: `Serve` takes a
`net.Listener` and keeps whatever `net.Addr` it gets, so the family is the
caller's choice. `TestServeOnIPv6Listener` binds `[::1]` and runs a C-ECHO and
a C-STORE through it, then checks that a handler sees a bracketed peer address
that still splits and parses — the failure mode being pinned is a `host:port`
guess somewhere in the path, which works on IPv4 and misreads `[::1]:port`. The
tests skip, not fail, where the platform has no IPv6: a build host with it
disabled cannot say anything about this code.

Not covered, because pynetdicom does not do them either: Unix sockets (`Address`
resolves a host and a port, and nothing else) and connection reuse across
associations (each association gets its own socket in both implementations).
`test_transport.py` is mostly about the `Address` object — parsing `ipv4`,
`ipv6`, `host:port`, bracketed literals, hostname resolution, a bad address
failing before anything is sent — and about `AssociationSocket` init and close;
the parts of it that can reach a wire are the address families, covered above.

### 4. User identity (partial)

Covered: accept, reject with a server response, requestor reading the response,
and the five identity types at the PDU layer (they share one wire shape).

Not covered: a full Kerberos/SAML/JWT flow with a real verifier. Neither
implementation has one — pynetdicom hands the decoded items to `EVT_USER_ID` and
takes back a response, and here the hook gets the bytes and decides — so a real
verifier is application code in both, and there is no framework behaviour left to
compare.

### 5. `test_ae.py`: the AE object's lifecycle (partial)

pynetdicom's `AE` owns a thread, a listening socket, a set of supported contexts
and a start/stop lifecycle, and `test_ae.py` exercises that object. Here, `Serve`
owns the socket and `ServerConfig` owns the policy, so "start the AE, add a
context, stop the AE" is "build a config, call `Serve`, cancel the context" —
covered by every `ae` test that starts a server, not by a separate lifecycle API.
What the upstream module asserts about what goes on the wire — which contexts are
accepted, how a rejection is worded, what happens when the same context is added
twice — is covered by `ae/negotiation_test.go` and `ae/contexts_test.go`.

The lifecycle half that `AE` also provides — enumerate the live associations, end
one, wait for the last to finish — is `ae.NewServer`: `Associations`,
`ActiveAssociation.Abort`, `AbortAll`, and a `Serve` that returns only after the
last association goroutine has ended. `Serve(ctx, ln, cfg)` remains the shorthand
for a server nobody needs a handle on, and both run the same code.

Genuine gap inside this group: none left at the negotiation layer. Both 0x56
(`SOP Class Extended Negotiation`) and 0x57 (`SOP Class Common Extended
Negotiation`) are decoded, answered through a `ServerConfig` hook, proposed
through `Config`, and readable from the association. The difference from
pynetdicom is where the *policy* lives, not what is on the wire: pynetdicom
gives its `EVT_SOP_COMMON` handler a dict of primitives and takes back whatever
it returns, while `ServerConfig.CommonExtendedNegotiation` answers yes or no
per proposed class and the confirmed statement is echoed verbatim. A counter-
offer is not something PS3.8 D.3.3.6 describes, so the narrower signature is
the honest one.

Still missing from this group: pynetdicom's `AE`-level defaults for these items
(`AE.add_requested_common_extended_negotiation` and friends) land here as struct
fields, so there is no per-item registration API to test. That is a difference in
where the policy is written, not in what an association negotiates.

### 6. The DUL state machine (partial)

pynetdicom implements the association as a 13-state table (`fsm.py`) driven by
DUL events, and `test_fsm.py` asserts the table: which state each event moves to,
which action runs, what the event queue holds afterwards. Those assertions are
about that object. A stack that runs the states directly has no table to inspect,
and nothing on the wire changes because of it.

What does reach the wire, and is covered here:

| `test_dul.py` / `test_fsm.py` behaviour | Here |
|---|---|
| unknown PDU type aborts the association | `TestSCPAbortsOnUnknownPDUType`, `TestSCUAbortsOnUnknownPDUType` |
| undecodable PDU body aborts | `TestSCPAbortsOnUndecodableBody` |
| bad PDU before the association exists | `TestSCPAbortsOnUndecodableFirstPDU` |
| garbage instead of a PDU, abort as first PDU, truncated RQ | `TestSCPSurvivesGarbageBeforeAssociation`, `TestSCPSurvivesAbortAsFirstPDU`, `TestSCPSurvivesTruncatedAssociateRQ` |
| A-ASSOCIATE-RJ, A-ASSOCIATE-AC then abort, abort in each direction | `ae/handshake_test.go`, `ae/release_test.go`, `ae/acse_robustness_test.go` |
| one abort per fault, not one per read | `TestSCUSendsOneAbortPerBadPDU` |

What is not reproduced, and why it cannot be a behaviour test: the ARTIM timer's
state transitions (we bound the same wait with `HandshakeTimeout` deadlines, gap
2), and the event queue's ordering. `test_fsm.py` also asserts a number of
*recovery* transitions that our code has no counterpart for because it never
enters the bad state — the abort paths above end the association instead.

### 7. The SOP Class and Service Class objects (partial)

`test_sop.py` and `test_service_class.py` cover pynetdicom's registry: 67 tests
that walk UID tables and 13 that exercise the two behaviours built on them.

The table is now here, as `ae/serviceclass.go`: 23 service classes,
`ServiceClassOf` and `ServiceClass.SOPClasses`, with
`ae/serviceclass_test.go::TestServiceClassesMatchPynetdicom` parsing
`pynetdicom/pynetdicom/sop_class.py` and requiring the two to agree UID for UID
where the submodule is checked out. That covers the 26
`test_sop.py::TestUIDToServiceClass` cases one for one, including the two that
are easy to get wrong: `1.2.840.10008.4.2` is a Service Class UID rather than a
SOP Class and is answered without being offered as an abstract syntax, and
`MediaStorageDirectoryStorage` has no service class in pynetdicom either.

Where it lives moved since this section was first written. It said the map
belonged in `godicom/uid`, next to the UIDs; that was this repository's layering
talking. pynetdicom keeps it in the network stack, pydicom — the library godicom
ports — has no notion of a service class, and the mapping is stated in PS3.4
rather than in the UID registry of PS3.6. A DIMSE stack is the only place that
needs to know it.

What remains of `test_sop.py` is the `SOPClass` type: 35 tests that assert a
named constant equals a UID and carries a `service_class` attribute. The
constant-equals-UID half is godicom's (`uid.CTImageStorage`), and the attribute
half is the table above; the type itself has no Go counterpart, because a UID
here is a string.

The behaviours, which do reach the wire:

- `ServiceClass.is_cancelled` — whether a C-CANCEL arrived for a given Message
  ID, consumed as it is read. Covered: `ae/scp_cancel_move_test.go` and
  `ae/scp_stop_midretrieve_test.go` cancel a running retrieval at a
  sub-operation boundary.
- `ServiceClass.validate_status` / `is_valid_status` — a handler's status is
  merged into the response, and a status outside the service class's set is
  logged and **still sent**; nothing is refused at the wire. Both halves are here
  now, in the places a Go handler can reach them: the sets are
  `ae/serviceclass_status.go` and `ServiceClass.HasStatus`, and the log is
  `warnUnexpectedStatus`, called from `writeMessage` so that every response this
  package sends is checked in one place. The half that has no counterpart is
  `validate_status`'s input: pynetdicom accepts a `Dataset` from a callback, so it
  has to answer 0xC001 when it holds no Status element and 0xC002 when the
  callback returned neither a dataset nor an int. A Go handler returns a
  `uint16`; there is nothing to repair and no wrong type to refuse.

That leaves the status sets as data rather than as a gap:

`ae/serviceclass_status.go` carries the general list and the twelve class annexes
of `status.py` -- Storage (B.2.3), C-FIND / C-GET / C-MOVE (C.4), worklist (K),
relevant patient (Q), substance administration (V), non-patient storage (GG),
procedure step, print job management (H), application event logging (I), UPS
(CC) and RT machine verification (DD). `ServiceClass.HasStatus` answers them, and
the `RetrieveOperation` parameter is what keeps C-FIND and C-MOVE apart: 0xB001 is
a C-FIND warning, 0xA801 a C-MOVE failure, and 0xA702 belongs to the two
operations that perform sub-operations. pynetdicom makes the same distinction by
picking `QR_*_SERVICE_CLASS_STATUS` in `QueryRetrieveServiceClass.__init__`; here
the operation is the key, which is also what the wire carries.

Annexes give ranges (`0xC000-0xCFFF: unable to process`) and the table keeps them
as ranges. pynetdicom materialises four thousand dict entries instead; both are
the same set, and `TestStatusSetsMatchPynetdicom` compares them by sweeping all
65536 codes, so a mistake in a range bound fails the same way a wrong code does.

Three tables in `status.py` are defined and attached to nothing, and are
therefore not copied: the Annex K worklist set (because
`BasicWorklistManagementServiceClass` answers `QR_FIND_SERVICE_CLASS_STATUS`),
and the Media Creation and Storage Management sets (because neither class sets
`statuses`). Following the table instead of the behaviour would have blessed a
status pynetdicom warns about. The test asserts the codes that tell the two
apart, so an upstream change that attaches one of them arrives as a failure
rather than as silence.

What is left of `test_service_class.py` is `validate_status`'s dataset half (four
cases) and its wrong-type case, plus `test_scp_raises`, which asserts that the
base `ServiceClass.SCP` raises `NotImplementedError`. All six are about the type a
Python callback returns or about an exception Python raises; the Go answers are
the type system and 0x0122.

## What is still missing

The list below is what pynetdicom has and this package does not, named as
features rather than as test counts. Three items that used to be spread across
gaps 2–5 are closed and are not on it: per-SOP-Class transfer syntaxes at the
acceptor (`ServerConfig.AcceptedContexts`), the requestor's view of the contexts
an acceptor refused (`Association.RejectedContexts`, and `NoContextError` when
none were accepted), and a registry of live associations (`ae.NewServer` with
`Associations` and `AbortAll`).

| Missing | pynetdicom | Here | Note |
|---|---|---|---|
| The eight command-line tools | `apps/{echoscu,echoscp,storescu,storescp,findscu,getscu,movescu,qrscp}` — 4322 lines of app over 6939 lines of end-to-end tests, including a Q/R SCP with its own handler database | `examples/` — one runnable program per service, each starting its own SCP on a loopback port | The largest absence, and the one least worth closing. These are the reference implementation's own test harnesses: `storescu` exists so `storescp` can be tested, and both exist so the library can be. What an operator wants from them is a way to see the API work, which `go run ./examples/find` answers without eight more binaries to maintain — a real CLI would need argument parsing, a file layout and a configuration format, and each of those is a decision this library should not make for its users |
| Per-service presentation context lists | 22 constants — `QueryRetrievePresentationContexts`, `PrintManagementPresentationContexts`, `ModalityPerformedPresentationContexts`, `UnifiedProcedurePresentationContexts`, … — plus `build_context(uid)` | `ServiceClass.SOPClasses()` and `DefaultTransferSyntaxes`, composed by the caller | 22 of the 99 `test_presentation.py` cases. They assert that a list holds the right UIDs and the default syntaxes; the UIDs come from the service class table, so the missing part is the composition, not the data |
| `register_uid` | a private SOP Class is registered into the service-class table at run time | the table is static; a private UID answers `ServiceClassUnknown` and gets the general status set | Deliberate — a package-level mutable table is global state every other association has to agree with — but it is an absence, not a covered behaviour |
| An association handle inside an SCP handler | `event.assoc`, so a handler can `send_c_store`, `send_n_event_report` or `send_c_find` on the connection it is serving | handlers receive `(ctx, req)`; the two protocol uses are purpose-built: C-GET and C-MOVE sub-operations store over the association, and an N-ACTION handler returns an `*EventReportRequest` to push the Storage Commitment report back | The gap shows up in a third-party service class that wants to initiate on an accepted association, not in DIMSE itself |

`require_calling_aet` left this table with `ServerConfig.RequiredCallingAETitles`: it is
`ServerConfig.RequiredCallingAETitles`, and it sits beside `require_called_aet`
(`AETitle` / `AlternativeAETitles` / `AllowAnyCalledAETitle`) rather than being
expressed through `Admit`, because the refusal belongs to the negotiation.

Two more things are worth writing down as absences rather than gaps, because
they would change behaviour if they were filled:

- **Async operations.** pynetdicom does not implement them either — `acse.py`
  answers `(1, 1)` to any proposed window — so implementing them would put this
  package ahead of the reference, not level with it. See "If you want to move
  the number".
- **Retrieval Capabilities (PS3.18 8.9).** pynetdicom has no HTTP surface, so it
  cannot be a parity item at all; it belongs to `dicomweb` and is still
  unimplemented for the reason recorded there.

## Validation

pynetdicom validates inbound datasets (VR, value multiplicity, required
attributes) and answers 0xA900. We validate the *envelope* (PDU framing, DIMSE
command sets, presentation contexts, statuses) and leave dataset semantics to
godicom and the handler. This is the same split dcm4che makes: the network stack
is not the place for a data dictionary.

The one table we do keep is the Query/Retrieve level hierarchy — which levels a
model answers and which attribute identifies an entity at each. It is protocol
structure (PS3.4 C.6.2.1.1), nine UIDs against three level lists, and it buys
three refusals a handler cannot recover from. Everything that needs the key
attributes themselves stays on the application side, behind the same hook.

## If you want to move the number

Two items used to sit here with reasons about this package — "a generator to be
built deliberately", "the design question ADR 0001 declined on purpose". Those
are not parity arguments. Judged against pynetdicom itself, neither one is a gap:

1. **Asynchronous operations: pynetdicom does not support them either.** Its
   ACSE reads the Asynchronous Operations Window item and answers `(1, 1)`
   whatever was proposed — `acse.py::_check_async_ops`, where the response is
   built after the requestor's numbers are deliberately discarded ("Response is
   always ignored as async ops is not supported"), and `docs/changelog/v1.0.0.rst`
   says the same in words. The upstream test is
   `test_acse.py::test_req_response_async`, which proposes a window and asserts
   `assoc.acceptor.asynchronous_operations == (1, 1)`; the counterpart here is
   `TestAsyncOperationsWindowAnsweredSynchronousByDefault`, which proposes 4/4
   and requires 1/1 back. So a peer that proposes 8/8 gets 1/1 from pynetdicom
   and, here, a refused second operation: the same behaviour, reported as an
   error instead of enforced by silence. `ErrOperationInFlight` is that refusal,
   and it is also why there is no pending-request map — with a maximum of one
   operation performed, a demultiplexer has nothing to demultiplex. Implementing
   async operations would put this package *ahead* of the reference
   implementation, not level with it.

   One place this package can say something pynetdicom cannot:
   `ServerConfig.AsyncOperationsWindow` lets a server advertise more than 1/1 for
   handlers it knows tolerate overlap. The default is nil, which answers 1/1, so
   the aligned behaviour is what you get by not asking.
2. **Retrieve Capabilities is not a pynetdicom question.** pynetdicom implements
   PS3.7 and PS3.8 and has no HTTP surface at all, so PS3.18 8.9 cannot move a
   number in this file. It stays unimplemented for the reason it always had — a
   wrong Capabilities document is worse than an absent one, because a user agent
   that trusts it stops probing for what is not listed — but that is a DICOMweb
   argument and belongs to `dicomweb`, not here.

What is left, measured against upstream test counts:

| Upstream block | Tests | Why it is not covered |
|---|---|---|
| `test_fsm.py` | 273 | asserts the state table, not the wire; the wire half is gap 6 |
| `test_sop.py`, `test_service_class.py` | 80 | 26 of them are the UID → Service Class table, and that table is `ae/serviceclass.go` now; the 13 `test_service_class.py` cases are down to the dataset half of `validate_status` and `test_scp_raises`. What is left is the `SOPClass` type (35), the Python-typed status cases (6) and the UID-dictionary membership cases (6), all described in gap 7 |
| `test_primitives.py`, `test_events.py`, `test_dimse_provider.py` | 95 | the Python object model |
| `test_timer.py`, `test_cli.py`, `test_environment.py` | 11 | a thread, a command line, version lookup |
| `test_dsutils.py` | 56 | godicom's |

The remainder of the 2078 — the `test_service_*`, `test_assoc*`, `test_acse`,
`test_pdu*`, `test_dimse*`, `test_transport`, `test_presentation`, `test_status`,
`test_logging` modules — is covered, including the parts that are only reachable
through a hook (gaps 1–5) and the abort-on-a-bad-PDU rule added in gap 2.

Two transactions an earlier version of this list named as gaps do not exist. The
Studies Service defines Retrieve, Store and Search and nothing else (PS3.18
10.3), and the 2019a edition that Annex C ("Retired") points at for removed
content has no such chapters either. Non-patient instances were the third item
listed here and are now covered: see `dicomweb/nonpatient.go`.

The first item used to be the two top-level Search targets, All Series
(`GET /series`) and All Instances (`GET /instances`), followed by a note that
retrieving `/instances/{instance}` was their harder half. Both are now covered,
and the note was wrong: 10.1 makes the three top-level resources — `/studies`,
`/series`, `/instances` — collections that reference every Study, Series and
Instance in the service, and a single Instance is addressed as
`/studies/{study}/series/{series}/instances/{instance}`. There was never a
retrieve under `/instances` to implement, so the handler does not invent one.

## Performance

pynetdicom ships an asv suite under `pynetdicom/benchmarks/`; dcm4che ships
only the `dcmbenchmark` tool, which parses a file repeatedly rather than
driving the network. So the counterpart to match is pynetdicom's, and it maps
one-to-one onto `go test -bench`:

| pynetdicom | Here |
|---|---|
| `bench_pdu.py` | `pdu/bench_test.go` — A-ASSOCIATE-RQ and P-DATA, plus `FragmentMessage` |
| `bench_dimse_message.py` | `dimse/bench_test.go` — C-ECHO, C-FIND, C-STORE command sets, `CommandHasDataset` |
| `bench_c_send.py` | `ae/assoc_bench_test.go` — associate, C-ECHO, C-STORE at two sizes |
| `bench_presentation.py` | `ae/assoc_bench_test.go` (`BenchmarkAssociate` covers the handshake as a whole; per-context negotiation is exercised by `ae/negotiation_test.go`) |
| — no counterpart | `ae/msgbody_bench_test.go` — `Stream` against `Bytes` against pull |

The last row is the one with no upstream equivalent, and the one worth reading.
On a 16 MiB store, `Stream` costs 601 B/op and 6 allocs/op regardless of
payload; `Bytes` costs 36 MB/op because a `bytes.Buffer` doubling to hold it
contiguous is the price of not streaming. pynetdicom cannot be compared on this
axis at all — `Event.dataset` is always a `BytesIO` — and dcm4che reaches the
same place only by handing `PDVInputStream` a buffer the caller had to allocate.

The per-PDU numbers are the other half of the same story, because streaming
only decides what the *handler* holds. What the stack allocates per PDU is
fixed by the encoder and decoder:

| Operation | Before | Now |
|---|---|---|
| `DecodePDataTF`, 16 KiB PDU | 1966 ns, 16.4 KB/op, 3 allocs | 44 ns, 56 B/op, 2 allocs |
| `EncodePDataTF`, 16 KiB PDU | 5713 ns, 51.2 KB/op, 3 allocs | 2355 ns, 18.4 KB/op, 1 alloc |
| C-STORE 1 MiB, end to end | 6.6 MB/op, 3.10 ms | 3.4 MB/op, 2.09 ms |

Decoding is now O(PDV count) rather than O(payload), which is why the 45x on the
first row is real but the throughput figure it produces is an artefact: there is
no longer data to move. The 3.4 MB that remain for a 1 MiB store are three
payload-sized buffers — the MCH prefix `FragmentMessage` prepends, the buffer
`Encode` writes, and the buffer `ReadLimit` fills — and they are sized by a PDU,
not by the dataset, so a store of 1 MiB sums to 3 MiB only because the whole
instance fits one PDU at that size.

What `StoreRequest.Source` changes is not those three but how many of them exist
at once. `pdu.FragmentReader` reads a fragment, encodes it, writes it, and only
then reads the next, so a 500 MB instance costs three fragments of memory
whatever its size; `FragmentMessage` materialises every P-DATA-TF of the message
before the first one is written, which is the whole dataset plus the dataset.
`TestFragmentReaderDoesNotReadAhead` is that claim rather than a benchmark's
allowance. The two remaining copies per fragment are structural — `PDV.Value`
carries the message control header, and `Encode` writes a fresh buffer — so
removing them means changing what a PDV is and what the decoder produces, not the
encoder's shape.

Run them with a fixed time so the numbers are comparable between runs:

```sh
go test -bench . -benchtime=1s -run XXX ./pdu ./dimse ./ae
```

They are not wired into CI. A hosted runner's benchmark numbers vary by more
than the changes worth catching, and a red build caused by a noisy neighbour is
worse than no signal.

## Maintaining this file

When a gap above is closed, move the line rather than adding a paragraph, and
re-run `go test ./... -cover` to update the table. If a new pynetdicom module
appears (`git -C pynetdicom pull`), add a row even if the answer is "out of
scope" — the value of this file is that the absences are written down.

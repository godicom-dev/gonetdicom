# Parity with pynetdicom

This is the working ledger behind "how much of pynetdicom is covered here".
It exists because counting test functions is misleading: pynetdicom has 2078
`def test_*` functions and a large share of them exercise a table-driven ACSE
state machine (`test_fsm.py`, 273) or Python-only layers (DIMSE primitives,
event objects, CLI, timers) that have no counterpart in a synchronous Go API.

The useful unit is a **behaviour**: a rule about the wire, an error a peer can
observe, or a limit an operator can set. Status below is per behaviour group,
not per upstream test.

Legend: **covered** — a test would fail if the behaviour regressed;
**partial** — the main path is tested, listed gaps are not;
**out of scope** — the Go design makes the question meaningless.

## Where the numbers are

| Package | Statements | Notes |
|---|---|---|
| `status` | 100.0% | pure constants and predicates |
| root | 95.8% | logger precedence, nil tolerance |
| `dimse` | 90.4% | command encode/decode, status helpers |
| `pdu` | 85.7% | every PDU and user-information item, incl. malformed frames |
| `ae` | 84.9% | association, DIMSE dispatch, Q/R, N-services |
| `dicomweb` | 78.7% | no pynetdicom counterpart (PS3.18 is not in it) |

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
| `test_presentation.py` (99) | `ae/contexts_test.go`, `ae/pc_negotiation_e2e_test.go`, `ae/role_*_test.go` | covered |
| `test_acse.py` (85) | `ae/handshake_test.go`, `ae/acse_robustness_test.go`, `ae/admit_test.go`, `ae/identity_test.go` | partial — see below |
| `test_ae.py` (102) | `ae/scp.go`/`Config`/`ServerConfig` + the tests above | partial — see below |
| `test_assoc.py` (213) | `ae/assoc_test.go`, `ae/release_test.go`, `ae/negotiation_test.go` | partial — see below |
| `test_assoc_user.py` (111) | `ae/identity_test.go`, `ae/negotiation_test.go` | partial — see below |
| `test_transport.py` (75) | `ae/tls_test.go`, `ae/limits_test.go`, `ae/pdulength_test.go` | partial — see below |
| `test_logging.py` (35) | `logger_test.go`, `logger_nil_test.go`, `ae/log_test.go` | covered |
| `test_dul.py` (10), `test_fsm.py` (273) | none | out of scope — we implement the states directly, not as a table |
| `test_primitives.py` (50), `test_events.py` (23), `test_dimse_provider.py` (22) | none | out of scope — Python object model |
| `test_timer.py` (7), `test_cli.py` (2), `test_environment.py` (2) | none | out of scope |
| `test_sop.py` (67), `test_service_class.py` (13) | none of our own | out of scope — we have no SOP Class registry by design; handlers receive the UID |
| `test_dsutils.py` (56) | godicom | out of scope — dataset work lives in the dataset library |
| `test_service_relevant_patient.py` (22), `test_service_non_patient.py` (12), `test_service_substance_admin.py` (8) | `ae/model_passthrough_test.go` | covered as pass-through — named models exported, no per-model validation |
| `test_utils.py` (18), `test_validators.py` (3) | none | partial — see "Validation" below |

Tally over the 34 upstream test modules: 13 covered, 3 covered as pass-through,
7 partial, 11 out of scope. The out-of-scope set accounts for about 400 upstream
tests that ask questions a Go DIMSE stack does not have (state-machine tables,
Python primitives, CLI, timers, a SOP Class registry).

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

Not covered: Unix sockets, and connection reuse across associations (we
deliberately open a connection per association).

### 4. User identity (partial)

Covered: accept, reject with a server response, requestor reading the response,
and the five identity types at the PDU layer (they share one wire shape).

Not covered: a full Kerberos/SAML/JWT flow with a real verifier — the hook gets
the bytes and decides, so there is nothing framework-side left to test.

### 5. `test_ae.py` behaviours with no Go equivalent (partial)

pynetdicom's `AE` object owns a thread, a listening socket, a set of supported
contexts and a lifecycle. Here, `Serve` owns the socket and `ServerConfig` owns
the policy, so "start the AE, add a context, stop the AE" is "build a config,
call Serve, cancel the context" — tested through every `ae` test, not as a
separate lifecycle API.

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

Still missing from this group, and out of scope by intent: pynetdicom's
`AE`-level defaults for these items (`AE.add_requested_common_extended_negotiation`
and friends) land here as struct fields, which is why there is no per-item
registration API to test.

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

In order of value per line of code:

1. DICOMweb beyond the MVP: PS3.18 Delete, Upsert, Capabilities and non-patient
   instances are the largest remaining surface, and they are outside
   pynetdicom's scope so they never appear in the comparison above.
2. Asynchronous operations: the window is negotiated and readable, but DIMSE in
   this package is synchronous, so more than one operation in flight is refused
   (`ErrOperationInFlight`) rather than demultiplexed. Implementing it means a
   pending-request map and a policy for responses nobody is waiting for — the
   design question ADR 0001 declined on purpose.

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
`Encode` writes, and the buffer `ReadLimit` fills. Removing any of them is
structural: the first needs `PDV` to stop carrying its message control header
inside `Value`, the third needs a streaming decoder that reads PDV headers as
they arrive instead of buffering the PDU first.

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

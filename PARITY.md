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
| `pdu` | 84.0% | every PDU and user-information item, incl. malformed frames |
| `ae` | 82.9% | association, DIMSE dispatch, Q/R, N-services |
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
| `test_service_qr.py` (149) | `ae/find_test.go`, `ae/retrieve_test.go`, `ae/move_*_test.go`, `ae/subops_e2e_test.go`, `ae/validate_test.go`, `ae/model_passthrough_test.go` | covered, except per-model attribute tables (see below) |
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

What is still missing is the part pynetdicom generates from its SOP Class
definitions: a per-model table of required and optional keys. Populating that
would mean porting a data dictionary into the network stack, which is the split
this package avoids — the hook is one function call away for an application that
wants it.

### 2. Association lifecycle corners (partial)

Covered: accept/reject, release, release collision on the SCU, peer release
while a request is outstanding, A-ABORT both directions, abort as first PDU,
truncated RQ, repeated rejections, association with no contexts.

Not covered: `network timeout` on a half-open socket (needs a controllable
clock), ARTIM timer expiry measured in seconds (we use `HandshakeTimeout`
deadlines instead, which is the same guarantee by a different mechanism), and
an acceptor that releases while a peer is mid-P-DATA.

### 3. Transport (partial)

Covered: TLS accept and dial, Maximum Length Received advertised and honoured,
tolerance for PDUs larger than we advertised, IPv4, idle and handshake
timeouts.

Not covered: IPv6 (no test; nothing in the code is address-family specific),
Unix sockets, and connection reuse across associations (we deliberately open a
connection per association).

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

Genuine gap inside this group: **SOP Class Extended Negotiation is answered but
SOP Class Common Extended Negotiation (0x57) is not implemented**. A peer that
proposes it is ignored, which is legal (unrecognised user items are skipped) but
means Storage Commitment service-class mapping via 0x57 does not work.

## Validation

pynetdicom validates inbound datasets (VR, value multiplicity, required
attributes) and answers 0xA900. We validate the *envelope* (PDU framing, DIMSE
command sets, presentation contexts, statuses) and leave dataset semantics to
godicom and the handler. This is the same split dcm4che makes: the network stack
is not the place for a data dictionary.

## If you want to move the number

In order of value per line of code:

1. SOP Class Common Extended Negotiation (0x57) encode/decode and a
   `ServerConfig` hook. Small, completes the user-information set.
2. IPv6 listener test, once a CI image that supports it is pinned.
3. `Association.Release` in the presence of pending C-GET sub-operations —
   needs a decision about what to do with in-flight stores, not just a test.
4. A per-model key table behind `IdentifierValidator`, if an application asks
   for one; today the hook is where that knowledge lives.

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

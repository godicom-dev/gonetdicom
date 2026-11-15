# 2. The handler contract: pynetdicom's generator, in Go

- Status: accepted
- Date: 2026-11-15
- Implemented: decisions 1–3 and 6 for all three services — `ae.FindHandler` /
  `ae.FindSeq` / `ae.FindMatches`, `ae.GetHandler` / `ae.PlannedGet` /
  `ae.PlannedOps`, `ae.MoveHandler` / `ae.PlannedMove` / `ae.PlannedMoveOps`,
  `ae.RetrieveOp`, `ae.RetrievePlan`, `ae.StoreContext`, `ae.StoreOutcome`, and
  `ae.ErrPeerCancelled` / `ae.ErrPeerReleased` for all of them. Decision 5
  (`StoreRequest.Source`, `pdu.FragmentReader`) is still proposed: a store
  carries its dataset in memory on both sides of the association.

## The question

Every SCP handler in this package returns a slice. `FindHandler` returns
`[]FindMatch`, `GetHandler` returns a `GetPlan`, `MoveHandler` returns a
`MovePlan`. A retrieval of a 500-instance study therefore materialises 500
`StoreRequest`s — and, because `StoreRequest` carries the dataset as `[]byte`,
500 decoded instances — before the first C-MOVE-RSP leaves the process.

[pynetdicom#51](https://github.com/godicom-dev/gonetdicom/issues/51) asks for
streaming, and it is the one remaining item in `PARITY.md`'s "What is still
missing" list that is a design question rather than a missing binary. The
question is not only "stream or not"; it is what the handler contract should be
when the reference implementation's contract is a Python generator bundled with
a live association handle.

## What the reference does

`service_class.py` drives three handlers, and the shape is the same in all
three: the handler is a generator, the SCP iterates it, and each item becomes a
response on the wire as it arrives.

| Service | First yield | Then | Handler exception |
|---|---|---|---|
| C-FIND (`:296-390`) | `(status, identifier)` | one per match | `0xC311`; an identifier that will not encode is `0xC312` |
| C-GET (`:1690-1810`) | the **number of sub-operations** (`0xC413` if not an int, `0xC416` if > 65535) | `(status, dataset)` pairs | `0xC411` |
| C-MOVE (`:2060-2160`) | the **destination** `(addr, port)` — `0xC514` if the yield is malformed, `0xA801` if the destination is unknown | the count (`0xC513`/`0xC516`), then pairs | `0xC511` |

Two details in that table are load-bearing and are easy to miss:

- **The count is not a convenience.** A C-MOVE-RSP / C-GET-RSP Pending response
  carries Number of Remaining Sub-operations, and the final response carries
  all four counts with a sum the standard requires to be consistent
  (PS3.4 C.4.3.1.3, C.4.3.2.3). Neither can be written truthfully without
  knowing how many sub-operations there are going to be.
- **The stores are the handler's, not the framework's.** In pynetdicom a C-GET
  handler calls `event.assoc.send_c_store(...)` inside the generator, and the
  framework only tallies what the handler says it did — `store_results` is
  incremented from the yielded status (`:1801`, `:1890`, `:1974`). That is why
  the association handle has to be in the event, and it is why the counts are
  a claim rather than a measurement.

Cancellation is `event.is_cancelled()`, a callable the handler is expected to
poll (`:283`).

The three things travel together — generator, association handle, self-reported
counts — because that is the only shape Python offers for "the handler needs to
do I/O and tell me what happened".

## The constraint on our side

Two of the three have already been decided differently, on purpose, and those
decisions are what make this translation possible:

1. **The framework performs the stores.** `GetPlan.Stores` and `MovePlan.Stores`
   are executed by `scpHandleGet` / `scpPerformMoveStores`, and `subOperations`
   records the real C-STORE-RSP statuses; `reconcile` then overwrites whatever
   the handler claimed. That was PR #59's subject — "Report sub-operation
   outcomes honestly". A count this package has measured is not something a
   handler should be asked to invent.
2. **There is no association handle in a handler, and there will not be.**
   `PARITY.md` lists it as a deliberate absence. The two protocol uses are
   purpose-built: sub-operations are performed here, and an N-ACTION handler
   returns an `*EventReportRequest` to push the Storage Commitment report back
   on the association it is serving.

So the translation target is: **keep the generator, drop the handle, and let
the framework's tally replace the handler's claim.** Everything else in
pynetdicom's contract — statuses, ordering, the first-yield discipline, the
cancel semantics, the final-response arithmetic — is kept one for one, and the
Go version is allowed to be *more* informative only where it is measuring
instead of guessing.

## Decision

### 1. A handler returns an iterator, not a slice

```go
type FindHandler func(ctx context.Context, req FindRequest) iter.Seq2[FindMatch, error]
```

`iter.Seq2[V, E]` is `func(yield func(V, E) bool)`, which is the Go spelling of
a generator: the handler body runs on the framework's goroutine while it
ranges, one item at a time, and `yield` returning false is the handler's cue to
return — the same contract as Python's `GeneratorExit`, except the handler
cannot swallow it because `for ... range` makes it the loop condition.

```go
OnCFind: func(ctx context.Context, req ae.FindRequest) iter.Seq2[ae.FindMatch, error] {
    return func(yield func(ae.FindMatch, error) bool) {
        for _, hit := range archive.Query(ctx, req.IdentifierData) {
            if !yield(ae.FindMatch{Status: status.Pending, Identifier: hit}, nil) {
                return // the peer cancelled, or the association ended
            }
        }
    }
}
```

Three properties fall out and none of them had to be built:

- **Back-pressure.** The framework encodes and writes before it asks for the
  next item, so the handler cannot outrun the socket. A channel would need a
  buffer, and the buffer size would become a tuning knob and a memory leak
  vector.
- **Cancellation.** The framework stops ranging when `pollStop` reports a
  cancel or a release, and the handler returns at its next `yield`. There is no
  `is_cancelled()` for the handler to forget to poll.
- **No goroutine.** The iterator body is called by the consumer, so a handler
  that is never ranged never runs, and nothing is left running when the
  association ends. A channel-based contract would need one goroutine per
  operation *and* a rule for who closes it.

Errors are per item: `yield(zero, err)` is the handler reporting failure, which
the framework answers with `0xC311` / `0xC411` / `0xC511` — pynetdicom's
`ctx.error_status` for the same three services.

### 2. A retrieve yields operations, and gets the outcome back

```go
type RetrieveHandler func(ctx context.Context, req RetrieveRequest) iter.Seq2[*RetrieveOp, error]

type RetrieveOp struct {
    // Exactly one of these is set, and the order is the protocol's:
    //   Plan   — first, always. Total is the sub-operation count;
    //            a C-MOVE Plan also carries the Destination.
    //   Store  — perform this C-STORE now, then send its Pending response.
    //   Result — send this response and perform no store.
    Plan   *RetrievePlan
    Store  *StoreRequest
    Result *RetrieveMatch

    // Outcome is written by the framework before yield returns. For a Store
    // it is the C-STORE-RSP this package actually received.
    Outcome StoreOutcome
}
```

The write-back is the part that replaces `event.assoc`. Because the iterator is
paused at `yield`, and the framework resumes it only after the store has been
answered, the handler reads `op.Outcome` on the same goroutine with no
synchronisation and no future to await:

```go
func(ctx context.Context, req ae.GetRequest) iter.Seq2[*ae.RetrieveOp, error] {
    return func(yield func(*ae.RetrieveOp, error) bool) {
        instances := archive.Find(ctx, req.IdentifierData)
        if !yield(&ae.RetrieveOp{Plan: &ae.RetrievePlan{Total: len(instances)}}, nil) {
            return
        }
        for _, inst := range instances {
            op := &ae.RetrieveOp{Store: &ae.StoreRequest{
                AffectedSOPClassUID:    inst.SOPClassUID,
                AffectedSOPInstanceUID: inst.SOPInstanceUID,
                Source:                 inst, // see 5
            }}
            if !yield(op, nil) {
                return
            }
            if op.Outcome.Status != status.Success {
                // this package counted it; the handler decides what to do next
            }
        }
    }
}
```

`Plan` is required as the first item, because the counts have to be truthful
(above). A C-MOVE handler's first `Plan` carries the destination, and the
framework dials it before it asks for the count — matching pynetdicom's order,
where the association to the Move Destination is opened between the first and
second yield. `Total == 0` means "nothing to do" and produces a Success with
zero counts, as upstream does.

A C-MOVE `Plan` also carries `Contexts []StoreContext`, which C-GET's does not.
That asymmetry is the protocol's: the stores go out on a second association, and
an A-ASSOCIATE-RQ has to name every presentation context before the first
C-STORE can be proposed, so the list cannot be discovered by running the
iterator. It used to be derived from the batch — `movePresentationContexts([]StoreRequest)` —
which is the one thing about this service that a slice could do and an iterator
cannot. pynetdicom resolves the same requirement with
`ApplicationEntity.requested_contexts` and raises if it is empty
(`ae.py:associate`); declaring it per operation is narrower, because a handler
that knows which series it is about to move knows which SOP Classes are in it.

`Total > 65535` differs by service, deliberately. C-GET answers `0xC416`
(pynetdicom, `:1739`). C-MOVE saturates the counts and logs the request instead
of answering `0xC516` (`:2132`): the ceiling is real, but a PACS moving a
65536-instance series needs the transfer to happen, and a `Remaining` that stops
moving is something an SCU can act on while a refusal is not. That was this
package's decision before the iterator contract, and translating a handler
signature is not the place to reverse it.

### 2a. Three details the code settled

Writing the retrieval half changed three things in the decision above. Two are
corrections to what this document said first, and one is a correction to the
first implementation of C-GET:

- **A completed `Store` is answered with a Pending, and the counts are the
  measured ones.** The first C-GET implementation left the response to the
  handler, on the argument that pynetdicom's contract is "one response per
  yield". That was wrong about the reference: pynetdicom sends a Pending after
  every store in both services (`service_class.py:1983-1989` for C-GET,
  `:2378-2384` for C-MOVE), and the counts in it are the four it just updated.
  Without it, a 500-instance retrieval produces no progress at all until the
  final response, and an SCU with an idle timer declares the peer dead. So the
  `Store` op's comment above — "perform this C-STORE now, then send its Pending
  response" — is what the code does, and the handler's `Result` is for anything
  beyond it. A C-MOVE plan additionally gets one opening Pending carrying the
  whole of `Total`, because the destination association takes a round trip
  before the first instance can go out.
- **Reconciliation is per response, not per batch.** `reconcile` rewrote a slice
  after the fact; `reconcileMatch` fills the same numbers into one response as it
  is written, because a response that waits for the end of the operation is a
  response the point of this ADR does not get to send. With `Total == 0` there
  is nothing to measure and the response passes through untouched, so a handler
  that reports its own progress for work this package is not doing keeps it.
- **C-MOVE is the same contract, not a special case.** It was held back because
  of decision 6 rather than because of anything about C-MOVE itself: the
  destination fan-out and the write-back are alternatives. Removing the fan-out
  turned out to cost less than the ADR first assumed — it was never a capability
  the reference had — so the service now shares `RetrieveOp`, the write-back and
  the reconcile with C-GET, and `MoveHandler` differs only in what its `Plan`
  has to carry.

### 3. The statuses are pynetdicom's, and so is the arithmetic

| Condition | pynetdicom | Here |
|---|---|---|
| handler raised / returned an error | `0xC311` `0xC411` `0xC511` | same |
| identifier will not encode | `0xC312` | same |
| first C-GET yield is not a count | `0xC413` | same |
| first C-MOVE yield is not a destination | `0xC514` | same |
| destination unknown | `0xA801` | same |
| count is not a count | `0xC513` | same |
| more than 65535 sub-operations | `0xC416` `0xC516` | same |
| handler yielded Success but a store failed | `0xB000` + Failed SOP Instance UID List | same, from the measured tally |
| no matches at all | final Success | same |

The last two are the ones worth stating: they are the two places where the
framework has a measurement the reference does not, and both resolve in the
direction of the wire format rather than away from it.

### 4. Cancellation is a context cause, not a callable

pynetdicom hands the handler a function to poll. Go already handed every
handler a `context.Context`, so the framework cancels it — with
`context.WithCancelCause` — at the moment `pollStop` sees a C-CANCEL for this
operation's Message ID:

| What happened | `context.Cause(ctx)` |
|---|---|
| the peer sent C-CANCEL | `ae.ErrPeerCancelled` |
| the peer released the association | `ae.ErrPeerReleased` |
| the server is shutting down | `context.Canceled` |
| a deadline expired | `context.DeadlineExceeded` |

That is strictly more than `is_cancelled()` can express: the handler can tell
"stop, the viewer pressed cancel" from "stop, we are shutting down" — two things
that want different cleanup and that used to arrive as one boolean
(ADR 0001 §6 made the same distinction one layer down, between cancel and
release).

The limit of it is worth stating plainly, because it is where the first
implementation stopped short of the claim. The framework only runs between two
items of the iterator, so a stop is observed — and the cause set — at that
boundary: a C-CANCEL that arrives while the handler is producing item *n* takes
effect when item *n* is offered. A handler that produces results in a loop is
therefore interrupted exactly where it should be, and one blocked inside a
single call is not interrupted by the cancel at all; the association's own
context still cancels on shutdown, which is a different cause. Closing that gap
means watching the association's inbound queue while the handler runs — a
reader-layer change (the queue would need an owner that operations can observe
without consuming), and its own ADR. Until then the honest statement is: the
cause is delivered at the item boundary, and `is_cancelled()`'s polling is
replaced by something that cannot be forgotten, not by something that preempts.

The wire behaviour does not change: a peer cancel still ends with `0xFE00` and
the untried sub-operations still count as Remaining (PS3.4 Table C.4-3).

### 5. `StoreRequest.Source`, because a store should not have to be in memory

```go
// StoreSource supplies a dataset without holding it.
//
// Open returns the bytes and their length: a PDV's length is explicit on the
// wire, so a stream that cannot say how long it is cannot be sent.
type StoreSource interface {
    Open() (r io.ReadCloser, size int64, err error)
}
```

`StoreRequest` fills `Data` > `Dataset` > `Source` in that order. Two
properties make it honest:

- `Open` may be called more than once, so a retry is a retry.
- There is no range-read method. DIMSE P-DATA is a one-way ordered stream; the
  retry granularity is the whole instance or the whole association, and an
  interface that offers to resume mid-instance is asking every implementation
  to answer a question the protocol never asks.

`Source` alone is not enough. `pdu.FragmentMessage` materialises every
`*PDataTF` for the message before the first one is written
(`pdu/pdata.go:144`), so a 500 MB dataset is copied once more on the way out;
`FragmentReader` and a reader-taking `writeMessage` are part of this decision,
not a follow-up. `PARITY.md`'s performance section already names this as the
third buffer that only a streaming encoder can remove.

Note the current limit of the win: `godicom` encodes a `Dataset` to `[]byte`
(`Dataset.Encode`), so `Source` serves the case where the bytes on disk are
already in the negotiated transfer syntax — which is the archive case, since a
Part 10 file minus its 132-byte preamble and file meta group *is* the dataset.
A `godicom.Dataset.EncodeTo(io.Writer, UID)` would extend it to re-encoding,
and that is a `godicom` decision, not this one.

### 6. Sequential, and the fan-out goes

An iterator cannot be fanned out without breaking the write-back: the handler
resumes as soon as the framework asks for the next item, so a store dispatched
to another goroutine has not finished by the time the handler reads
`op.Outcome`. `MoveDestination.MaxAssociations` therefore has no meaning on this
path, and rather than leave a field that silently does nothing it is removed:
C-MOVE sub-operations run one at a time on one destination association.

That is not a capability regressing but a capability this package invented being
retired. pynetdicom opens one association to the Move Destination
(`service_class.py:2144`) and stores through it one at a time (`:2354`). The
field's own documentation called itself "a Go concurrency advantage over typical
single-threaded DIMSE stacks", which is an admission that it was never part of
the port. It also cost the thing #51 is about: fanning out required the whole
batch to exist — `make([]StoreRequest, len(stores))` — before the first store
could be dispatched, which is the memory profile the issue was opened over.

What it bought was throughput against a peer that can absorb parallel
associations. The measurements in `PARITY.md` put a 1 MiB store at ~2 ms end to
end, so a 500-instance study is about a second of transfer, against 500 MB of
memory not held. If throughput turns out to matter more, the honest way to
restore it is a batched operation — `Store []StoreRequest` yielding
`[]StoreOutcome`, ordered by index — which keeps the write-back, defines an
ordering rule for the responses in the same breath, and can keep the fan-out.
That is a new capability rather than a translation, so it should be argued on
its own terms in its own ADR.

## The rule behind the six decisions

The decisions above are instances of one rule, which is what makes them
reviewable: **the wire is pynetdicom's, the scheduling is Go's.** Where the
reference spends a resource Python does not have (a thread, a buffered
`BytesIO`, a pollable callable, an object that stands in for a value), this
package answers with the Go mechanism for the same guarantee — and the
guarantee, not the mechanism, is what the tests pin.

| Reference mechanism | Go mechanism | What it buys | What must not move |
|---|---|---|---|
| one thread per connection, pooled by `AE.maximum_associations` | one goroutine per association, admission by `ServerConfig.Admit` (PR #61) | admission becomes policy instead of a scarce resource | A-ASSOCIATE-AC/RJ and the result/source/reason it carries |
| `event.dataset` as `BytesIO` | `StoreEvent.Stream()` over the fragment channel | constant memory for any instance size | PDV fragmentation, Maximum Length Received |
| handler generator + `event.assoc.send_c_store` | `iter.Seq2` + framework-performed stores | the counts are measured, not claimed | response order, one response per yield |
| `event.is_cancelled()` polled by the handler | `context.Cause(ctx)` with exported sentinels | cancellation reaches a blocked call; peer-cancel ≠ shutdown | `0xFE00`, Remaining vs Failed (PS3.4 Table C.4-3) |
| exceptions and `attempt()` | typed errors, `errors.Is` | refusal is programmatic at the call site | the status code each refusal becomes |
| `threading.Timer` / ARTIM | `HandshakeTimeout`, `IdleTimeout` | no timer thread, no state machine | the seconds each bound represents |
| a `Dataset` that is always whole | `StoreSource` + `FragmentReader` | the dataset need not be resident to be sent | PDV length is explicit: a source that cannot state its size cannot be sent |

Two things are deliberately *not* translated, because the reference's shape is
an artefact of Python rather than a behaviour: the `SOPClass` object (a UID is
a string, and the registry is a table — `ae/serviceclass.go`), and the
per-service presentation-context lists (a composition of `ServiceClass.SOPClasses`
and `DefaultTransferSyntaxes`, to be written by the caller — `PARITY.md` names
the 22 constants as data that is already present).

And one thing is translated *further* than the reference, which needs saying
out loud: `testing/synctest` gives a controllable clock, so the half-open
socket and ARTIM-expiry behaviours that `PARITY.md` gap 2 records as "needs a
controllable clock" are testable here without a real five-second wait. Closing
that gap moves a `partial` to `covered` without changing a byte on the wire.

## Consequences

- **Breaking.** Three handler signatures change, and one field goes.
  Migration is one line each via adapters that keep the old shapes as values:
  `ae.FindMatches([]FindMatch) FindHandler`, `ae.PlannedGet(GetPlan) GetHandler`,
  `ae.PlannedMove(MovePlan) MoveHandler`, with `ae.FindSeq` / `ae.PlannedOps` /
  `ae.PlannedMoveOps` for a handler that keeps its own body. They yield the
  materialised slice in order, which is exactly what the old code did, so a
  caller that does not want streaming does not have to think about iterators.
  `MoveDestination.MaxAssociations` is deleted (decision 6) and cannot be
  migrated: the behaviour it asked for is the one the write-back rules out.
- **`subOperations` changes meaning at the source, not at the end.** Today the
  total is `len(plan.Stores)` and the counts are folded in afterwards by
  `reconcile`. Under the iterator the total arrives first and the counts
  accumulate as the stores happen, so `cancelMatch` no longer has to reason
  about "untried" as a subtraction from a plan — it is the tail of a loop that
  has not run. The PS3.4 rule it encodes (untried is Remaining, not Failed) is
  unchanged.
- **Every service gains a real Pending per sub-operation.** Today a C-GET handler
  supplies Pending responses and `reconcile` may rewrite the final one, while a
  C-MOVE handler gets them from the batch path. Under the iterator each `Store`
  op sends its own Pending with counts this package measured, which is what
  pynetdicom's one-yield-per-sub-operation produces too, minus the claim. A
  handler that also yields a Pending `Result` gets a second response, which is
  allowed and means what it says.
- **`writeMessage` grows a reader variant.** Two call sites (`ae/assoc.go:642`,
  `ae/scp.go:1535`); `FragmentMessage` stays for the command set, which is
  small and already in memory.
- **`PARITY.md` changes in two places.** The "association handle inside an SCP
  handler" row becomes an explanation rather than a gap — the third use it
  names (a third-party service class initiating on an accepted association) is
  still absent, and the C-GET/C-MOVE uses are covered by measurement instead.
  Gap 2's "network timeout on a half-open socket (needs a controllable clock)"
  is also closable now: Go 1.25's `testing/synctest` is that clock, and the two
  timeout behaviours this package substitutes for the ARTIM timer become
  testable without a five-second sleep.
- **Handlers get a goroutine-free streaming shape**, which means an application
  that wants to prefetch (a DB cursor open on a second goroutine feeding a
  channel) still can, inside its own iterator. The contract does not forbid
  concurrency; it only declines to schedule it.

## Alternatives considered

**Channels.** `chan FindMatch` plus an error channel is the other Go spelling of
a generator, and it buys concurrent production for free. It costs a goroutine
per operation, a close protocol (who closes on cancel — sender, receiver, or a
third party?), and the error has to travel out of band. `iter.Pull` gives the
same concurrency for a handler that wants it, without making every handler pay
for it. A channel would also have to answer what happens when the consumer
stops early, which `yield` returning false answers by construction.

**A callback pair: `emit func(T) error` plus a second `result func() Outcome`.**
This is what "yield plus `event.assoc`" looks like when it is translated
literally, and it is worse than either neighbour: two functions where the
protocol has one step, and an ordering rule that is documented rather than
enforced.

**Keeping the slices and adding a `Stream bool`.** It would not be breaking,
and it would double every code path — including the status arithmetic, which is
the part most worth having exactly one copy of.

**Handing the handler the association, as pynetdicom does.** It is the smallest
translation and the wrong one: it moves the store into application code, which
is where the counts become claims again, and it undoes PR #59. The one thing it
buys — a third-party service class initiating operations on the association it
serves — is a real gap in `PARITY.md` and needs its own design, not this one.

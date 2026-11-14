# 1. Association release when an operation is still running

- Status: accepted
- Date: 2026-11-14

## The question

A C-GET is bidirectional: while the SCU waits for C-GET-RSPs it also receives
C-STORE-RQs, several hundred of them for a series. What does `Release` mean in
the middle of that, and what happens to the sub-operations that are already on
the wire? The question is not academic — a viewer with a progress bar and a
"cancel" button hits it on every aborted retrieval.

It came up as two concrete defects:

- `Release` called while `CGet` was blocked failed with `ae: unexpected PDU
  *pdu.PDataTF for release` as soon as the peer sent anything, so an ordinary
  race between "peer keeps storing" and "we ask to release" looked like a
  protocol error.
- An A-RELEASE-RQ that arrived between two C-STORE sub-operations was reported
  by the SCP as an unexpected PDU, which ended the association without the
  A-RELEASE-RP the peer was waiting for. The peer saw a dropped connection and
  could not tell that from a network fault.

## What the standard says

PS3.8 7.2.2 settles most of it, and the sentences are worth quoting because
three of the four decisions below follow from them:

> The requestor shall not issue any further primitives other than an A-ABORT
> request primitive until it receives an A-RELEASE confirmation primitive.
>
> **Note:** Even though the requestor of the A-RELEASE service shall not issue
> any further primitive other than A-ABORT, it may receive P-DATA Indication
> primitives.
>
> The acceptor then shall not issue any UL primitives other than an A-RELEASE
> response primitive, an A-ABORT request primitive, or P-DATA Request primitive.
>
> An accepting DICOM Application Entity shall always issue an A-RELEASE
> response primitive with an "affirmative" result parameter (i.e., accept the
> release).

So: a release cannot be refused, a requestor may not answer anything it receives
after asking, and an acceptor may keep sending P-DATA while it finishes. There
is no "hold on, I'm busy" on the wire.

## The constraint on our side

`Association` has one read side and no message demultiplexer. Every operation
reads the responses it asked for, in the caller's goroutine, and recognises them
by Message ID. That is what makes the API a set of ordinary blocking calls
instead of a session object with callbacks, and it is why two operations cannot
share an association: the second reader takes whichever PDU arrives first, so
one operation receives the other's answer and the message ids stop lining up.
The README has said "one operation at a time" all along; until now nothing
checked it.

On the accepting side the shape is different — one goroutine per association,
one reader goroutine feeding it whole messages — so a release arriving
mid-operation reaches a handler that is between messages, and the handler is
what decides whether to keep going.

## Decision

1. **`Release` is an association-level request and cannot be used to stop an
   operation.** While another operation is waiting for its answer, `Release`
   returns `ErrOperationInFlight` before it writes anything. PS3.8 7.2.2 leaves
   a requestor nothing but A-ABORT once it has asked, so "release and abandon
   the operation" is not a thing the protocol can express; pretending otherwise
   would mean either answering messages after the release (forbidden) or
   leaving the operation's caller waiting for an answer that will never come.

2. **The same check refuses a second operation**, with the same error. This is
   a behaviour change for anyone who was calling two methods concurrently and
   getting away with it: they now get a failure at the call they made, instead
   of a message-id mismatch somewhere else later.

3. **`Release` ignores inbound P-DATA while it waits for A-RELEASE-RP.** The
   Note in 7.2.2 says the requestor may still receive them; 7.2.2 says it may
   not answer them. So the messages are dropped, and the loop continues until
   the RP arrives. Each pass consumes one PDU, so the loop cannot spin without
   the peer sending something, and a peer that goes quiet is bounded by the
   caller's context and `IdleTimeout` — the same bound every other read in this
   package has. The previous four-iteration cap is gone: it turned a long
   in-flight queue into "release did not complete", which is not an error the
   caller could act on.

4. **To stop a retrieval, cancel the operation, then release.** `CCancel` takes
   the Message ID, so `GetRequest.MessageID` and `MoveRequest.MessageID` now
   exist for the same reason `FindRequest.MessageID` already did: a caller that
   wants to cancel from another goroutine has to know the id it is cancelling.
   Cancelling the operation's context is the other route, and it stays the
   caller's job to decide when the association is quiet enough to release
   (see the limitation below).

5. **An A-RELEASE-RQ that arrives mid-operation ends the operation.** The SCP
   stops at the next sub-operation boundary, does not send a final C-GET-RSP —
   the peer is waiting for an RP and must ignore P-DATA — and lets the
   association loop answer the release. A sub-operation already on the wire is
   still answered before stopping: its response is owed, and skipping it would
   leave the read side one message behind.

6. **The same boundary distinguishes cancel from release.** A C-CANCEL means
   "stop this operation, keep the association"; release means "stop
   everything". Both used to be reported as one boolean, and the two need
   different answers, so `pollStop` now returns which of the two it saw. A
   cancel yields 0xFE00 with the counts reached so far, and the untried
   sub-operations are reported as remaining rather than failed (PS3.4
   Table C.4-3, C.4.3.1.5): they were never attempted, and calling them failures
   would tell the SCU that instances it still wants could not be stored.

## Consequences

- A deferred `Release` after a failed operation keeps working when the stream is
  at a message boundary, which is the normal case: the peer sent a complete
  message and is now waiting.
- When the operation died from a context deadline that expired *inside* a PDU,
  the SCU-side stream is desynchronised and no DIMSE exchange can repair it —
  `Abort` is the only exit, and `Release` will fail. This is a real limitation,
  not an oversight: `pdu.Read` cannot resume a partially read PDU, and the
  protocol has no primitive for "forget what I sent". Fixing it means moving the
  SCU read side onto the same reader goroutine the SCP uses, so that
  cancellation is noticed between whole PDUs instead of inside one. That is a
  larger change than this one and has its own migration cost: every SCU method
  would read from a channel instead of the connection.
- C-MOVE sub-operations run on a *different* association (the Move Destination),
  so a release on the requestor's association does not stop them here: the batch
  completes and the final response goes nowhere. Cutting it short is a separate
  question — what to report for instances that were never tried is exactly the
  remaining-vs-failed distinction above — and it is tracked in `PARITY.md`
  rather than half-answered here.
- A C-GET that has finished its sub-operations writes the rest of its responses
  back to back. A cancel arriving in that window is not noticed, because the
  responses are already queued in memory and there is no message boundary at
  which the request could be read. That is why `writeRetrieveResponses` has no
  poll of its own: C-FIND polls between matches and C-GET between
  sub-operations, since those are the points where the operation waits for the
  peer. The function had a poll before this change and no caller could ever
  trigger it, which is worth stating because an unreachable branch that looks
  like a safety net is worse than no branch at all.

## Alternatives considered

**Make `Release` cancel outstanding operations first.** It would be convenient
and it is what a UI button wants, but the requestor is not allowed to send
anything after A-RELEASE-RQ, so the cancel would have to precede the release by
a full round trip and wait for the final response — at which point `Release` is
doing four things and hiding a timeout policy inside itself. Callers that want
that can write it in six lines; the repository's `Cancel` path is already
tested on its own.

**Allow concurrent operations and demultiplex by Message ID.** That is what
dcm4che and pynetdicom do, and both need a background thread plus a pending
map plus a policy for what happens when a response arrives for an operation
nobody is waiting for. It trades a documented restriction for a hidden
scheduler. With asynchronous operations windows negotiated but not implemented
(see `PARITY.md`), there would be nothing to gain: DIMSE in this library is
synchronous by design, and a second operation would only interleave its PDUs
with the first one's.

**Answer the peer's P-DATA during release.** It feels polite, and the acceptor
is allowed to send P-DATA. The requestor is not: 7.2.2 allows it A-ABORT only.
Sending a C-STORE-RSP during our own release would be a protocol violation that
the peer is entitled to abort over.

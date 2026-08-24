package dimse

import "testing"

// decodeElements is the floor of every DIMSE path: each Decode* function and each
// Peek* helper begins by calling it on bytes that arrived inside a P-DATA-TF, so
// it decides what the rest of the package ever sees. Its element lengths are
// 32-bit and unvalidated by anything upstream.
func FuzzDecodeElements(f *testing.F) {
	for _, seed := range commandSeeds() {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, in []byte) {
		els, err := decodeElements(in)
		if err != nil {
			if els != nil {
				t.Fatalf("returned %d elements alongside an error: %v", len(els), err)
			}
			return
		}
		// The decode is total — it rejects trailing bytes — so a success accounts
		// for every byte of the input. Any length field read differently from the
		// way it was written stops the arithmetic adding up here.
		total := 0
		for _, e := range els {
			total += 8 + len(e.value)
		}
		if total != len(in) {
			t.Fatalf("%d elements account for %d of %d bytes", len(els), total, len(in))
		}
	})
}

// An SCP dispatches on Command Field and then hands the same bytes to whichever
// decoder that names, so any of these can be reached with any command set a peer
// cares to send — including one whose Command Field says C-STORE and whose body
// does not.
func FuzzCommandDecoders(f *testing.F) {
	for _, seed := range commandSeeds() {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, in []byte) {
		// The values are not checked. These are peer bytes: an error is a fine
		// answer, and so is a message with zero-valued fields. A panic is not.
		_, _ = PeekCommandField(in)
		PeekMessageID(in)
		PeekStatus(in)
		_, _ = CommandHasDataset(in)

		_, _ = DecodeCEchoRQ(in)
		_, _ = DecodeCEchoRSP(in)
		_, _ = DecodeCStoreRQ(in)
		_, _ = DecodeCStoreRSP(in)
		_, _ = DecodeCFindRQ(in)
		_, _ = DecodeCFindRSP(in)
		_, _ = DecodeCMoveRQ(in)
		_, _ = DecodeCMoveRSP(in)
		_, _ = DecodeCGetRQ(in)
		_, _ = DecodeCGetRSP(in)
		_, _ = DecodeCCancelRQ(in)

		_, _ = DecodeNEventReportRQ(in)
		_, _ = DecodeNEventReportRSP(in)
		_, _ = DecodeNGetRQ(in)
		_, _ = DecodeNGetRSP(in)
		_, _ = DecodeNSetRQ(in)
		_, _ = DecodeNSetRSP(in)
		_, _ = DecodeNActionRQ(in)
		_, _ = DecodeNActionRSP(in)
		_, _ = DecodeNCreateRQ(in)
		_, _ = DecodeNCreateRSP(in)
		_, _ = DecodeNDeleteRQ(in)
		_, _ = DecodeNDeleteRSP(in)
	})
}

// commandSeeds returns the pynetdicom golden PDVs with the Message Control
// Header stripped, which is what a decoder is handed, plus an N-* command set the
// goldens do not cover.
func commandSeeds() [][]byte {
	pdvs := [][]byte{
		goldenCEchoRQPDV,
		goldenCEchoRSPPDV,
		goldenCStoreRQPDV,
		goldenCStoreDSPDV,
		goldenCStoreRSPPDV,
		goldenCFindRQPDV,
		goldenCFindRQDSPDV,
		goldenCFindRSPPDV,
		goldenCFindRSPDSPDV,
		goldenCMoveRQPDV,
		goldenCMoveRQDSPDV,
		goldenCMoveRSPPDV,
		goldenCGetRQPDV,
		goldenCGetRQDSPDV,
		goldenCGetRSPPDV,
	}
	out := make([][]byte, 0, len(pdvs)+1)
	for _, pdv := range pdvs {
		out = append(out, pdv[1:])
	}

	n := &NActionRQ{
		MessageID:               7,
		ActionTypeID:            StorageCommitmentActionTypeRequest,
		RequestedSOPClassUID:    StorageCommitmentPushModelSOPClass,
		RequestedSOPInstanceUID: StorageCommitmentPushModelSOPInstance,
		HasDataset:              true,
	}
	if b, err := n.Encode(); err == nil {
		out = append(out, b)
	}
	return out
}

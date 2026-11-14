package ae_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// dialNegotiated starts an SCP, associates with cfg, and returns the
// association.
func dialNegotiated(t *testing.T, cfg ae.ServerConfig, scu ae.Config) *ae.Association {
	t.Helper()

	cfg.AETitle = "NEGSCP"
	if cfg.AcceptedAbstractSyntaxes == nil {
		cfg.AcceptedAbstractSyntaxes = []string{pdu.VerificationSOPClass}
	}
	addr := serveSCP(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	scu.AETitle = "NEGSCU"
	if len(scu.PresentationContexts) == 0 {
		scu.PresentationContexts = []ae.PresentationContext{
			{ID: 1, AbstractSyntax: pdu.VerificationSOPClass, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
		}
	}
	assoc, err := ae.Dial(ctx, scu, addr, "NEGSCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = assoc.Release(context.Background()) })
	return assoc
}

// PS3.8 9.3.3.3.2: an acceptor that received a window must answer with its own.
// For a dispatch loop that takes one message at a time the honest answer is
// 1/1, and that is what a requestor has to see — not silence, which would leave
// it unable to tell whether pipelining is allowed.
func TestAsyncOperationsWindowAnsweredSynchronousByDefault(t *testing.T) {
	t.Parallel()

	assoc := dialNegotiated(t, ae.ServerConfig{}, ae.Config{
		AsyncOperationsWindow: &pdu.AsyncOperationsWindow{MaxInvoked: 4, MaxPerformed: 4},
	})

	got, ok := assoc.AsyncOperationsWindow()
	if !ok {
		t.Fatal("acceptor sent no window in answer to a proposed one")
	}
	if got != (pdu.AsyncOperationsWindow{MaxInvoked: 1, MaxPerformed: 1}) {
		t.Fatalf("window = %+v, want 1/1", got)
	}
}

// A server that configures a window gets that answer echoed, and the requestor
// reads it back unchanged.
func TestAsyncOperationsWindowEchoesConfiguredValue(t *testing.T) {
	t.Parallel()

	assoc := dialNegotiated(t, ae.ServerConfig{
		AsyncOperationsWindow: &pdu.AsyncOperationsWindow{MaxInvoked: 3, MaxPerformed: 2},
	}, ae.Config{
		AsyncOperationsWindow: &pdu.AsyncOperationsWindow{MaxInvoked: 3, MaxPerformed: 2},
	})

	got, ok := assoc.AsyncOperationsWindow()
	if !ok {
		t.Fatal("acceptor sent no window")
	}
	if got != (pdu.AsyncOperationsWindow{MaxInvoked: 3, MaxPerformed: 2}) {
		t.Fatalf("window = %+v, want 3/2", got)
	}
}

// Nothing proposed means nothing answered: an AC that volunteers a window the
// requestor never asked for would be answering a question nobody asked.
func TestAsyncOperationsWindowAbsentWhenNotProposed(t *testing.T) {
	t.Parallel()

	assoc := dialNegotiated(t, ae.ServerConfig{}, ae.Config{})
	if got, ok := assoc.AsyncOperationsWindow(); ok {
		t.Fatalf("window = %+v, want none", got)
	}
}

// The SCP side sees the same value in its handler context, so a handler can log
// or reject on it without reaching for the association object.
func TestAsyncOperationsWindowVisibleToHandler(t *testing.T) {
	t.Parallel()

	var (
		mu   sync.Mutex
		seen *pdu.AsyncOperationsWindow
	)
	assoc := dialNegotiated(t, ae.ServerConfig{
		OnCFind: func(ctx context.Context, _ ae.FindRequest) []ae.FindMatch {
			info, _ := ae.AssocFromContext(ctx)
			mu.Lock()
			seen = info.AsyncOperationsWindow
			mu.Unlock()
			return []ae.FindMatch{{Status: 0x0000}}
		},
	}, ae.Config{
		AsyncOperationsWindow: &pdu.AsyncOperationsWindow{MaxInvoked: 2, MaxPerformed: 2},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := assoc.CFind(ctx, ae.FindRequest{
		QueryModel:     pdu.VerificationSOPClass,
		IdentifierData: findIdentifier(),
	}); err != nil {
		t.Fatalf("CFind: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if seen == nil {
		t.Fatal("handler saw no window")
	}
	if *seen != (pdu.AsyncOperationsWindow{MaxInvoked: 1, MaxPerformed: 1}) {
		t.Fatalf("handler window = %+v, want 1/1", *seen)
	}
}

// Extended negotiation is answered per SOP Class: a hook that declines a class
// must produce no item for it, because absence is how the protocol says "no
// extended semantics here".
func TestExtendedNegotiationAnsweredPerClass(t *testing.T) {
	t.Parallel()

	const (
		accepted = "1.2.840.10008.5.1.4.1.1.2"
		declined = "1.2.840.10008.5.1.4.1.1.4"
	)
	var calls int
	assoc := dialNegotiated(t, ae.ServerConfig{
		AcceptedAbstractSyntaxes: []string{accepted, declined},
		ExtendedNegotiation: func(sopClassUID string, info []byte) ([]byte, bool) {
			calls++
			if sopClassUID != accepted {
				return nil, false
			}
			// Answer is the reverse of what was asked, so a passthrough bug
			// would be visible.
			out := append([]byte(nil), info...)
			for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
				out[i], out[j] = out[j], out[i]
			}
			return out, true
		},
	}, ae.Config{
		PresentationContexts: []ae.PresentationContext{
			{ID: 1, AbstractSyntax: accepted, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
			{ID: 3, AbstractSyntax: declined, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
		},
		ExtendedNegotiations: []pdu.ExtendedNegotiation{
			{SOPClassUID: accepted, Info: []byte{0x01, 0x02}},
			{SOPClassUID: declined, Info: []byte{0x03}},
		},
	})

	if calls != 2 {
		t.Fatalf("hook called %d times, want once per proposed class", calls)
	}
	info, ok := assoc.ExtendedNegotiation(accepted)
	if !ok {
		t.Fatal("accepted class got no answer")
	}
	if len(info) != 2 || info[0] != 0x02 || info[1] != 0x01 {
		t.Fatalf("answer = % x, want the hook's 02 01", info)
	}
	if info, ok := assoc.ExtendedNegotiation(declined); ok {
		t.Fatalf("declined class got % x", info)
	}
}

// Without a hook the SCP declines every class, and the association still
// stands: extended negotiation is optional, not a precondition.
func TestExtendedNegotiationAbsentWithoutHook(t *testing.T) {
	t.Parallel()

	const sop = "1.2.840.10008.5.1.4.1.1.2"
	assoc := dialNegotiated(t, ae.ServerConfig{
		AcceptedAbstractSyntaxes: []string{sop},
	}, ae.Config{
		PresentationContexts: []ae.PresentationContext{
			{ID: 1, AbstractSyntax: sop, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
		},
		ExtendedNegotiations: []pdu.ExtendedNegotiation{{SOPClassUID: sop, Info: []byte{0x01}}},
	})

	if info, ok := assoc.ExtendedNegotiation(sop); ok {
		t.Fatalf("unexpected answer % x", info)
	}
	if len(assoc.Contexts()) != 1 {
		t.Fatalf("association has %d contexts, want 1", len(assoc.Contexts()))
	}
}

// findIdentifier is the smallest identifier a C-FIND-RQ can carry.
func findIdentifier() *godicom.Dataset {
	ds := godicom.NewDataset()
	ds.Set(godicom.NewDataElement(godicom.MustTag("QueryRetrieveLevel"), godicom.VRCS, "PATIENT"))
	return ds
}

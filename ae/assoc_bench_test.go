package ae_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

const benchCTImageStorage = "1.2.840.10008.5.1.4.1.1.2"

// End-to-end numbers over a real socket, in the shape pynetdicom measures in
// benchmarks/bench_c_send.py. These are the only benchmarks here that include
// the kernel: everything below them is a function call, this is a conversation.

func startBenchServer(b *testing.B, cfg ae.ServerConfig) (string, func()) {
	b.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = ae.Serve(ctx, ln, cfg)
	}()
	return ln.Addr().String(), func() {
		cancel()
		_ = ln.Close()
		<-done
	}
}

func benchDial(b *testing.B, addr, abstractSyntax string) *ae.Association {
	b.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	assoc, err := ae.Dial(ctx, ae.Config{
		AETitle: "BENCHSCU",
		PresentationContexts: []ae.PresentationContext{{
			ID:               1,
			AbstractSyntax:   abstractSyntax,
			TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
		}},
	}, addr, "BENCHSCP")
	if err != nil {
		b.Fatal(err)
	}
	return assoc
}

func benchOpCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// The fixed cost every operation pays once: TCP, the A-ASSOCIATE handshake, the
// negotiation, the release. A C-MOVE of a single instance is mostly this, which
// is why it is the number to look at before optimising anything per-message.
func BenchmarkAssociate(b *testing.B) {
	addr, stop := startBenchServer(b, ae.ServerConfig{
		AETitle:                  "BENCHSCP",
		AcceptedAbstractSyntaxes: []string{pdu.VerificationSOPClass},
	})
	defer stop()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		assoc := benchDial(b, addr, pdu.VerificationSOPClass)
		ctx, cancel := benchOpCtx()
		if err := assoc.Release(ctx); err != nil {
			cancel()
			b.Fatal(err)
		}
		cancel()
	}
}

// One C-ECHO inside an established association: the per-message cost with the
// handshake factored out. Compare against BenchmarkAssociate to see how much of
// a small operation is connection setup.
func BenchmarkCEcho(b *testing.B) {
	addr, stop := startBenchServer(b, ae.ServerConfig{
		AETitle:                  "BENCHSCP",
		AcceptedAbstractSyntaxes: []string{pdu.VerificationSOPClass},
	})
	defer stop()

	assoc := benchDial(b, addr, pdu.VerificationSOPClass)
	b.Cleanup(func() {
		ctx, cancel := benchOpCtx()
		defer cancel()
		_ = assoc.Release(ctx)
	})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, cancel := benchOpCtx()
		err := assoc.CEcho(ctx)
		cancel()
		if err != nil {
			b.Fatal(err)
		}
	}
}

// C-STORE at two sizes. The small one is dominated by the round trip and the
// command set; the 1 MiB one is the streaming path carrying a real image, and
// its B/op is the point of the exercise - it should not track the payload.
func BenchmarkCStore(b *testing.B) {
	for _, size := range []int{4 << 10, 1 << 20} {
		b.Run(fmt.Sprintf("%dKiB", size>>10), func(b *testing.B) {
			payload := bytes.Repeat([]byte{0x5a}, size)

			addr, stop := startBenchServer(b, ae.ServerConfig{
				AETitle:                  "BENCHSCP",
				AcceptedAbstractSyntaxes: []string{benchCTImageStorage},
				OnCStore: func(_ context.Context, ev ae.StoreEvent) uint16 {
					if _, err := io.Copy(io.Discard, ev.Stream()); err != nil {
						return 0xC211 // cannot understand
					}
					return dimse.StatusSuccess
				},
			})
			defer stop()

			assoc := benchDial(b, addr, benchCTImageStorage)
			b.Cleanup(func() {
				ctx, cancel := benchOpCtx()
				defer cancel()
				_ = assoc.Release(ctx)
			})

			ctx, cancel := benchOpCtx()
			defer cancel()
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := assoc.CStore(ctx, ae.StoreRequest{
					Dataset:                payload,
					AffectedSOPClassUID:    benchCTImageStorage,
					AffectedSOPInstanceUID: "1.2.826.0.1.3680043.10.1075.1.1.1.1",
				}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

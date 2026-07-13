package ae_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/pdu"
)

func TestUserIdentityUsernameRoundtrip(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- ae.Serve(ctx, ln, ae.ServerConfig{
			AETitle: "IDSCP",
			OnUserIdentity: func(req pdu.UserIdentityRQ) (bool, []byte) {
				if req.Type != pdu.UserIdentityUsername {
					t.Errorf("type=%d", req.Type)
					return false, nil
				}
				if string(req.PrimaryField) != "alice" {
					t.Errorf("primary=%q", req.PrimaryField)
					return false, nil
				}
				return true, nil
			},
		})
	}()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()

	assoc, err := ae.Dial(dialCtx, ae.Config{
		AETitle:      "IDSCU",
		UserIdentity: ae.UsernameIdentity("alice", true),
	}, ln.Addr().String(), "IDSCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if assoc.UserIdentityResponse() != nil {
		t.Fatalf("unexpected AC response for username type: %q", assoc.UserIdentityResponse())
	}
	if err := assoc.CEcho(dialCtx); err != nil {
		t.Fatalf("C-ECHO: %v", err)
	}
	if err := assoc.Release(dialCtx); err != nil {
		t.Fatalf("release: %v", err)
	}
	cancel()
	<-errCh
}

func TestUserIdentityReject(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- ae.Serve(ctx, ln, ae.ServerConfig{
			AETitle: "IDSCP",
			OnUserIdentity: func(req pdu.UserIdentityRQ) (bool, []byte) {
				return string(req.PrimaryField) == "ok", nil
			},
		})
	}()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()

	_, err = ae.Dial(dialCtx, ae.Config{
		AETitle:      "IDSCU",
		UserIdentity: ae.UsernamePasscodeIdentity("bad", "x", false),
	}, ln.Addr().String(), "IDSCP")
	if !errors.Is(err, ae.ErrRejected) {
		t.Fatalf("want ErrRejected, got %v", err)
	}
	cancel()
	<-errCh
}

func TestUserIdentityJWTServerResponse(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wantResp := []byte("server-token")
	errCh := make(chan error, 1)
	go func() {
		errCh <- ae.Serve(ctx, ln, ae.ServerConfig{
			AETitle: "IDSCP",
			OnUserIdentity: func(req pdu.UserIdentityRQ) (bool, []byte) {
				if req.Type != pdu.UserIdentityJWT {
					return false, nil
				}
				return true, wantResp
			},
		})
	}()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()

	assoc, err := ae.Dial(dialCtx, ae.Config{
		AETitle: "IDSCU",
		UserIdentity: &pdu.UserIdentityRQ{
			Type:                      pdu.UserIdentityJWT,
			PositiveResponseRequested: true,
			PrimaryField:              []byte("client-jwt"),
		},
	}, ln.Addr().String(), "IDSCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if string(assoc.UserIdentityResponse()) != string(wantResp) {
		t.Fatalf("response=%q", assoc.UserIdentityResponse())
	}
	if err := assoc.Release(dialCtx); err != nil {
		t.Fatalf("release: %v", err)
	}
	cancel()
	<-errCh
}

func TestUserIdentityIgnoredWithoutHandler(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- ae.Serve(ctx, ln, ae.ServerConfig{AETitle: "IDSCP"})
	}()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()

	assoc, err := ae.Dial(dialCtx, ae.Config{
		AETitle:      "IDSCU",
		UserIdentity: ae.UsernameIdentity("anyone", true),
	}, ln.Addr().String(), "IDSCP")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := assoc.Release(dialCtx); err != nil {
		t.Fatalf("release: %v", err)
	}
	cancel()
	<-errCh
}

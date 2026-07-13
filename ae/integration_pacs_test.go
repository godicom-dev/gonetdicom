//go:build integration

package ae_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/godicom-dev/gonetdicom/ae"
)

// Soak against a real DICOM peer.
//
//	GONETDICOM_PACS_ADDR=host:11112 GONETDICOM_PACS_AE=ANY-SCP \
//	  go test -tags=integration ./ae -run TestIntegrationCEchoPACS -count=1 -v
func TestIntegrationCEchoPACS(t *testing.T) {
	addr := os.Getenv("GONETDICOM_PACS_ADDR")
	called := os.Getenv("GONETDICOM_PACS_AE")
	if addr == "" || called == "" {
		t.Skip("set GONETDICOM_PACS_ADDR and GONETDICOM_PACS_AE to run")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	assoc, err := ae.Dial(ctx, ae.Config{
		AETitle:     envOr("GONETDICOM_CALLING_AE", "GONETSCU"),
		IdleTimeout: 10 * time.Second,
	}, addr, called)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer assoc.Abort()

	if err := assoc.CEcho(ctx); err != nil {
		t.Fatalf("C-ECHO: %v", err)
	}
	if err := assoc.Release(ctx); err != nil {
		t.Fatalf("release: %v", err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

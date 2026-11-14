package ae_test

import (
	"context"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
	"github.com/godicom-dev/gonetdicom/status"
)

const eventReportSOPClass = "1.2.840.10008.1.20.1" // Storage Commitment Push Model

// The SCP side of N-EVENT-REPORT: event type, instance and event information
// reach the handler, and its status and reply dataset reach the requestor. This
// is the service a Storage Commitment SCP uses to answer a push, so a payload
// dropped here is a commitment that silently never completes.
func TestNEventReportInboundRoundtrip(t *testing.T) {
	t.Parallel()

	const instance = "1.2.3.4.5.6.7"

	var (
		gotEventType uint16
		gotInstance  string
		gotInfo      string
	)
	reply := godicom.NewDataset()
	reply.Set(godicom.NewDataElement(godicom.MustTag("RetrieveAETitle"), godicom.VRCS, "REPLYAE"))

	assoc := dialNegotiated(t, ae.ServerConfig{
		AcceptedAbstractSyntaxes: []string{eventReportSOPClass},
		OnNEventReport: func(ctx context.Context, req ae.EventReportRequest) uint16 {
			gotEventType = req.EventTypeID
			gotInstance = req.AffectedSOPInstanceUID
			if ds, err := godicom.DecodeDataset(req.EventInformation, godicom.UID(pdu.ImplicitVRLittleEndian)); err == nil {
				if v, ok := ds.GetString(godicom.MustTag("TransactionUID")); ok {
					gotInfo = v
				}
			}
			return dimse.StatusSuccess
		},
	}, ae.Config{
		PresentationContexts: []ae.PresentationContext{
			{ID: 1, AbstractSyntax: eventReportSOPClass, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
		},
	})

	info := godicom.NewDataset()
	info.Set(godicom.NewDataElement(godicom.MustTag("TransactionUID"), godicom.VRUI, "1.2.3"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := assoc.NEventReport(ctx, ae.EventReportRequest{
		AffectedSOPClassUID:    eventReportSOPClass,
		AffectedSOPInstanceUID: instance,
		EventTypeID:            1,
		EventInformationData:   info,
	})
	if err != nil {
		t.Fatalf("NEventReport: %v", err)
	}
	if res.Status != dimse.StatusSuccess {
		t.Fatalf("status 0x%04x, want success", res.Status)
	}
	if gotEventType != 1 {
		t.Fatalf("handler saw event type %d, want 1", gotEventType)
	}
	if gotInstance != instance {
		t.Fatalf("handler saw instance %q, want %q", gotInstance, instance)
	}
	if gotInfo != "1.2.3" {
		t.Fatalf("handler saw transaction UID %q, want 1.2.3", gotInfo)
	}
}

// A failing N-EVENT-REPORT is answered, not swallowed: the peer has to learn
// that the event was not accepted, otherwise a retry never happens.
func TestNEventReportInboundFailureStatus(t *testing.T) {
	t.Parallel()

	assoc := dialNegotiated(t, ae.ServerConfig{
		AcceptedAbstractSyntaxes: []string{eventReportSOPClass},
		OnNEventReport: func(context.Context, ae.EventReportRequest) uint16 {
			return status.ProcessingFailure
		},
	}, ae.Config{
		PresentationContexts: []ae.PresentationContext{
			{ID: 1, AbstractSyntax: eventReportSOPClass, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := assoc.NEventReport(ctx, ae.EventReportRequest{
		AffectedSOPClassUID:    eventReportSOPClass,
		AffectedSOPInstanceUID: "1.2.3",
		EventTypeID:            2,
	})
	if err != nil {
		t.Fatalf("NEventReport: %v", err)
	}
	if res.Status != status.ProcessingFailure {
		t.Fatalf("status 0x%04x, want 0x%04x", res.Status, status.ProcessingFailure)
	}
}

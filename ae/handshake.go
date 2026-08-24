package ae

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/godicom-dev/gonetdicom"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// checkProtocolVersion rejects a requestor that does not speak DICOM UL
// protocol version 1.
//
// Protocol-version is a bit field (PS3.8 9.3.2), so only bit 0 is tested: bits
// for versions that do not exist yet are the sender's business, not ours.
func checkProtocolVersion(ctx context.Context, conn net.Conn, cfg ServerConfig, rq *pdu.AAssociateRQ) error {
	if rq.ProtocolVersion&pdu.ProtocolVersion1 != 0 {
		return nil
	}
	return reject(ctx, conn, cfg, rq,
		pdu.RejectSourceServiceProviderACSE, pdu.RejectReasonProtocolVersionNotSupported,
		fmt.Sprintf("protocol version 0x%04x does not include version 1", rq.ProtocolVersion))
}

// checkAETitles rejects a requestor that asked for an AE title this SCP does not
// answer to, or that did not identify itself.
//
// Accepting any Called AE Title is not harmless: a peer pointed at the wrong
// host, or at the wrong service on the right host, has no way left to notice.
// It also removes the only handle an SCP has for behaving differently per
// published name.
func checkAETitles(ctx context.Context, conn net.Conn, cfg ServerConfig, rq *pdu.AAssociateRQ) error {
	if normalizeAETitle(rq.CallingAETitle) == "" {
		// PS3.8 requires a real AE title here, and an all-padding one cannot be
		// echoed back in the A-ASSOCIATE-AC (see pdu.PadAETitle), so this has to
		// be refused rather than tolerated.
		return reject(ctx, conn, cfg, rq,
			pdu.RejectSourceServiceUser, pdu.RejectReasonCallingAENotRecognized,
			"empty Calling AE Title")
	}
	if !cfg.acceptsCalledAETitle(rq.CalledAETitle) {
		return reject(ctx, conn, cfg, rq,
			pdu.RejectSourceServiceUser, pdu.RejectReasonCalledAENotRecognized,
			fmt.Sprintf("Called AE Title %q is not %q", rq.CalledAETitle, cfg.AETitle))
	}
	return nil
}

// acceptsCalledAETitle reports whether called names this SCP.
func (c ServerConfig) acceptsCalledAETitle(called string) bool {
	if c.AllowAnyCalledAETitle {
		return true
	}
	called = normalizeAETitle(called)
	if called == "" {
		return false
	}
	if called == normalizeAETitle(c.AETitle) {
		return true
	}
	for _, alt := range c.AlternativeAETitles {
		if called == normalizeAETitle(alt) {
			return true
		}
	}
	return false
}

// normalizeAETitle strips the padding a peer may have used. AE titles are
// 16 bytes padded with spaces; NUL padding is not conformant but common enough
// that comparing without trimming it would reject working peers. AE titles are
// otherwise case sensitive, so nothing else is folded away.
func normalizeAETitle(s string) string {
	return strings.Trim(s, " \x00")
}

// reject answers an A-ASSOCIATE-RQ with a permanent A-ASSOCIATE-RJ and returns
// the matching error.
//
// The rejection is logged here because Serve discards what handleAssociation
// returns: without a log line, a peer being turned away leaves no trace on this
// side at all.
func reject(ctx context.Context, conn net.Conn, cfg ServerConfig, rq *pdu.AAssociateRQ, source, reason byte, why string) error {
	cfg.logger(ctx).Warn("scp rejected association",
		gonetdicom.AttrRemote, conn.RemoteAddr().String(),
		gonetdicom.AttrCalledAE, rq.CalledAETitle,
		gonetdicom.AttrCallingAE, rq.CallingAETitle,
		"reason", why,
	)
	_ = writePDUConn(ctx, conn, &pdu.AAssociateRJ{
		Result:           pdu.RejectResultPermanent,
		Source:           source,
		ReasonDiagnostic: reason,
	})
	return fmt.Errorf("%w: %s", ErrRejected, why)
}

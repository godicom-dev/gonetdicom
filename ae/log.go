package ae

import (
	"context"
	"log/slog"
	"net"

	"github.com/godicom-dev/gonetdicom"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
)

func resolveAELogger(ctx context.Context, opt *slog.Logger) *slog.Logger {
	return gonetdicom.ResolveLogger(ctx, opt)
}

func logPDU(ctx context.Context, direction string, p pdu.PDU) {
	l := gonetdicom.LoggerFromContext(ctx)
	if !l.Enabled(ctx, slog.LevelDebug) {
		return
	}
	l.DebugContext(ctx, "pdu",
		gonetdicom.AttrDirection, direction,
		gonetdicom.AttrPDUType, p.Type(),
		gonetdicom.AttrPDUTypeName, pdu.TypeName(p.Type()),
	)
}

func logDIMSE(ctx context.Context, direction string, pcid byte, command []byte) {
	l := gonetdicom.LoggerFromContext(ctx)
	if !l.Enabled(ctx, slog.LevelDebug) {
		return
	}
	args := []any{
		gonetdicom.AttrDirection, direction,
		gonetdicom.AttrPCID, pcid,
	}
	if field, err := dimse.PeekCommandField(command); err == nil {
		args = append(args,
			gonetdicom.AttrCommandField, field,
			gonetdicom.AttrCommandName, dimse.CommandName(field),
		)
	}
	if msgID, ok := dimse.PeekMessageID(command); ok {
		args = append(args, gonetdicom.AttrMessageID, msgID)
	}
	if status, ok := dimse.PeekStatus(command); ok {
		args = append(args, gonetdicom.AttrStatus, dimse.FormatStatus(status))
	}
	l.DebugContext(ctx, "dimse", args...)
}

func readPDUConn(ctx context.Context, conn net.Conn) (pdu.PDU, error) {
	p, err := pdu.Read(conn)
	if err != nil {
		return nil, err
	}
	logPDU(ctx, "recv", p)
	return p, nil
}

func writePDUConn(ctx context.Context, conn net.Conn, p pdu.PDU) error {
	logPDU(ctx, "send", p)
	return pdu.Write(conn, p)
}

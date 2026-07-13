package pdu

import "fmt"

// AReleaseRQ is an A-RELEASE-RQ PDU.
type AReleaseRQ struct{}

func (p *AReleaseRQ) Type() byte { return TypeAReleaseRQ }

// Encode serializes the PDU.
func (p *AReleaseRQ) Encode() ([]byte, error) {
	return encodeHeader(TypeAReleaseRQ, make([]byte, 4)), nil
}

// DecodeAReleaseRQ parses an A-RELEASE-RQ PDU.
func DecodeAReleaseRQ(raw []byte) (*AReleaseRQ, error) {
	if len(raw) != 10 {
		return nil, fmt.Errorf("pdu: A-RELEASE-RQ length %d", len(raw))
	}
	if raw[0] != TypeAReleaseRQ {
		return nil, fmt.Errorf("%w: got 0x%02x want A-RELEASE-RQ", ErrUnexpectedType, raw[0])
	}
	return &AReleaseRQ{}, nil
}

// AReleaseRP is an A-RELEASE-RP PDU.
type AReleaseRP struct{}

func (p *AReleaseRP) Type() byte { return TypeAReleaseRP }

// Encode serializes the PDU.
func (p *AReleaseRP) Encode() ([]byte, error) {
	return encodeHeader(TypeAReleaseRP, make([]byte, 4)), nil
}

// DecodeAReleaseRP parses an A-RELEASE-RP PDU.
func DecodeAReleaseRP(raw []byte) (*AReleaseRP, error) {
	if len(raw) != 10 {
		return nil, fmt.Errorf("pdu: A-RELEASE-RP length %d", len(raw))
	}
	if raw[0] != TypeAReleaseRP {
		return nil, fmt.Errorf("%w: got 0x%02x want A-RELEASE-RP", ErrUnexpectedType, raw[0])
	}
	return &AReleaseRP{}, nil
}

// AAbort is an A-ABORT PDU.
type AAbort struct {
	Source           byte
	ReasonDiagnostic byte
}

func (p *AAbort) Type() byte { return TypeAAbort }

// Encode serializes the PDU.
func (p *AAbort) Encode() ([]byte, error) {
	body := []byte{0x00, 0x00, p.Source, p.ReasonDiagnostic}
	return encodeHeader(TypeAAbort, body), nil
}

// DecodeAAbort parses an A-ABORT PDU.
func DecodeAAbort(raw []byte) (*AAbort, error) {
	if len(raw) != 10 {
		return nil, fmt.Errorf("pdu: A-ABORT length %d", len(raw))
	}
	if raw[0] != TypeAAbort {
		return nil, fmt.Errorf("%w: got 0x%02x want A-ABORT", ErrUnexpectedType, raw[0])
	}
	return &AAbort{
		Source:           raw[8],
		ReasonDiagnostic: raw[9],
	}, nil
}

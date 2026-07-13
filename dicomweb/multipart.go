package dicomweb

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"strings"
)

func multipartContentType(boundary string) string {
	return fmt.Sprintf(`%s; type="%s"; boundary=%s`, MediaTypeMultipart, MediaTypeDICOM, boundary)
}

func writeDICOMParts(w io.Writer, boundary string, parts [][]byte) error {
	mw := multipart.NewWriter(w)
	if err := mw.SetBoundary(boundary); err != nil {
		return err
	}
	for i, part := range parts {
		h := make(textproto.MIMEHeader)
		h.Set("Content-Type", MediaTypeDICOM)
		h.Set("Content-Transfer-Encoding", "binary")
		pw, err := mw.CreatePart(h)
		if err != nil {
			return fmt.Errorf("dicomweb: multipart part %d: %w", i, err)
		}
		if _, err := pw.Write(part); err != nil {
			return err
		}
	}
	return mw.Close()
}

func readDICOMParts(body io.Reader, contentType string) ([][]byte, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, fmt.Errorf("dicomweb: Content-Type: %w", err)
	}
	switch {
	case mediaType == MediaTypeDICOM:
		b, err := io.ReadAll(body)
		if err != nil {
			return nil, err
		}
		return [][]byte{b}, nil
	case mediaType == MediaTypeMultipart || strings.HasPrefix(mediaType, "multipart/"):
		boundary := params["boundary"]
		if boundary == "" {
			return nil, fmt.Errorf("dicomweb: multipart missing boundary")
		}
		mr := multipart.NewReader(body, boundary)
		var parts [][]byte
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			ct := p.Header.Get("Content-Type")
			if ct != "" {
				mt, _, err := mime.ParseMediaType(ct)
				if err == nil && mt != MediaTypeDICOM && mt != "application/octet-stream" {
					_, _ = io.Copy(io.Discard, p)
					continue
				}
			}
			b, err := io.ReadAll(p)
			if err != nil {
				return nil, err
			}
			parts = append(parts, b)
		}
		if len(parts) == 0 {
			return nil, fmt.Errorf("dicomweb: multipart contained no DICOM parts")
		}
		return parts, nil
	default:
		return nil, fmt.Errorf("dicomweb: unsupported Content-Type %q", contentType)
	}
}

func encodeSTOWBody(instances [][]byte) (contentType string, body *bytes.Buffer, err error) {
	if len(instances) == 0 {
		return "", nil, fmt.Errorf("dicomweb: no instances to store")
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	boundary := mw.Boundary()
	for i, part := range instances {
		h := make(textproto.MIMEHeader)
		h.Set("Content-Type", MediaTypeDICOM)
		h.Set("Content-Transfer-Encoding", "binary")
		pw, err := mw.CreatePart(h)
		if err != nil {
			return "", nil, fmt.Errorf("dicomweb: stow part %d: %w", i, err)
		}
		if _, err := pw.Write(part); err != nil {
			return "", nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return "", nil, err
	}
	return multipartContentType(boundary), &buf, nil
}

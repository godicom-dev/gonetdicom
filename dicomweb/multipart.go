package dicomweb

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
)

func multipartContentType(boundary string) string {
	return fmt.Sprintf(`%s; type="%s"; boundary=%s`, MediaTypeMultipart, MediaTypeDICOM, boundary)
}

func frameMultipartContentType(boundary, transferSyntax string) string {
	return fmt.Sprintf(`%s; type="%s"; transfer-syntax=%s; boundary=%s`,
		MediaTypeMultipart, MediaTypeOctetStream, transferSyntax, boundary)
}

// writeDICOMParts writes parts as a multipart/related body and sets the
// response Content-Type to match.
//
// The boundary is the writer's own randomly generated one (crypto/rand backed).
// It must never be a fixed string: DICOM part bodies are data-controlled, so a
// predictable delimiter appearing inside an instance would silently truncate
// the response. Callers must not have written a body before calling this.
func writeDICOMParts(w http.ResponseWriter, parts [][]byte) error {
	mw := multipart.NewWriter(w)
	w.Header().Set("Content-Type", multipartContentType(mw.Boundary()))
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

func renderedMultipartContentType(boundary, mediaType string) string {
	return fmt.Sprintf(`%s; type="%s"; boundary=%s`, MediaTypeMultipart, mediaType, boundary)
}

// writeRenderedParts writes already-encoded images as a multipart/related body
// whose parts are all of one image media type. Each part is one instance rendered
// the way the request asked for. Callers must not have written a body before
// calling this.
//
// type is a single media type rather than "*": this is one answer to one request,
// and a response that mixed JPEG and PNG parts would leave the requestor parsing
// each part's Content-Type to find out what it got.
func writeRenderedParts(w http.ResponseWriter, parts [][]byte, mediaType string) error {
	mw := multipart.NewWriter(w)
	w.Header().Set("Content-Type", renderedMultipartContentType(mw.Boundary(), mediaType))
	for i, part := range parts {
		h := make(textproto.MIMEHeader)
		h.Set("Content-Type", mediaType)
		h.Set("Content-Transfer-Encoding", "binary")
		pw, err := mw.CreatePart(h)
		if err != nil {
			return fmt.Errorf("dicomweb: rendered part %d: %w", i, err)
		}
		if _, err := pw.Write(part); err != nil {
			return err
		}
	}
	return mw.Close()
}

// readRenderedParts splits a Retrieve Rendered response into its image parts and
// reports the media type they are in. A body that is one image rather than a
// multipart is returned as a single part, because a server that answers a
// one-instance request with a bare image is answering the same request.
func readRenderedParts(body io.Reader, contentType string) (mediaType string, parts [][]byte, err error) {
	mt, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", nil, fmt.Errorf("dicomweb: Content-Type: %w", err)
	}
	if strings.HasPrefix(mt, "image/") {
		b, err := io.ReadAll(body)
		if err != nil {
			return "", nil, err
		}
		return mt, [][]byte{b}, nil
	}
	if mt != MediaTypeMultipart && !strings.HasPrefix(mt, "multipart/") {
		return "", nil, fmt.Errorf("dicomweb: unsupported Content-Type %q", contentType)
	}
	boundary := params["boundary"]
	if boundary == "" {
		return "", nil, fmt.Errorf("dicomweb: multipart missing boundary")
	}
	mr := multipart.NewReader(body, boundary)
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", nil, err
		}
		partType := p.Header.Get("Content-Type")
		partMediaType, _, mErr := mime.ParseMediaType(partType)
		if partType != "" && (mErr != nil || !strings.HasPrefix(partMediaType, "image/")) {
			_, _ = io.Copy(io.Discard, p)
			continue
		}
		b, err := io.ReadAll(p)
		if err != nil {
			return "", nil, err
		}
		// The parts are all one media type (see writeRenderedParts); the first
		// part that says so settles it, and a part with no Content-Type of its
		// own leaves it to the multipart's type parameter.
		if mediaType == "" && partMediaType != "" {
			mediaType = partMediaType
		}
		parts = append(parts, b)
	}
	if len(parts) == 0 {
		return "", nil, fmt.Errorf("dicomweb: multipart contained no image parts")
	}
	if mediaType == "" {
		mediaType = params["type"]
	}
	return mediaType, parts, nil
}

// writeFrameParts writes Retrieve Frames as multipart/related of
// application/octet-stream. Each part is one requested frame in the instance's
// transfer syntax. Callers must not have written a body before calling this.
func writeFrameParts(w http.ResponseWriter, frames [][]byte, transferSyntax string) error {
	mw := multipart.NewWriter(w)
	w.Header().Set("Content-Type", frameMultipartContentType(mw.Boundary(), transferSyntax))
	for i, frame := range frames {
		h := make(textproto.MIMEHeader)
		h.Set("Content-Type", MediaTypeOctetStream)
		pw, err := mw.CreatePart(h)
		if err != nil {
			return fmt.Errorf("dicomweb: frame part %d: %w", i, err)
		}
		if _, err := pw.Write(frame); err != nil {
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
			if errors.Is(err, io.EOF) {
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

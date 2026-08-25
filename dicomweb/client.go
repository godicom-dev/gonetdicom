// Package dicomweb implements a DICOMweb (PS3.18) client and origin-server MVP.
//
// Supported transactions:
//   - WADO-RS Retrieve Study / Series / Instance (+ metadata)
//   - STOW-RS Store Instances (multipart/related; type="application/dicom")
//   - QIDO-RS Search for Studies / Series / Instances
package dicomweb

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/godicom-dev/gonetdicom"
)

const (
	MediaTypeDICOM     = "application/dicom"
	MediaTypeDICOMJSON = "application/dicom+json"
	MediaTypeMultipart = "multipart/related"
)

// Client is a DICOMweb user agent.
type Client struct {
	BaseURL    string        // e.g. https://pacs.example/dicom-web
	HTTPClient *http.Client  // optional; defaults via Timeout / NewClient
	Timeout    time.Duration // used when HTTPClient is nil
	TLS        *tls.Config   // used by NewClient for the default transport
	Logger     *slog.Logger  // optional; nil falls back to context then DiscardHandler
	// MaxResponseBytes bounds how much of one response body a call reads before
	// failing with ErrTooLarge. Zero uses DefaultMaxResponseBytes; negative reads
	// without a bound. Retrieving a whole study buffers every instance, so this is
	// what stands between a wrong Content-Length and the process's memory.
	MaxResponseBytes int64
}

func (c *Client) maxResponseBytes() int64 {
	if c == nil || c.MaxResponseBytes == 0 {
		return DefaultMaxResponseBytes
	}
	return c.MaxResponseBytes
}

func (c *Client) httpClient() *http.Client {
	if c != nil && c.HTTPClient != nil {
		return c.HTTPClient
	}
	timeout := 30 * time.Second
	if c != nil && c.Timeout > 0 {
		timeout = c.Timeout
	}
	return &http.Client{Timeout: timeout}
}

func (c *Client) logger(ctx context.Context) *slog.Logger {
	var opt *slog.Logger
	if c != nil {
		opt = c.Logger
	}
	return gonetdicom.ResolveLogger(ctx, opt).With(gonetdicom.AttrComponent, gonetdicom.ComponentDICOMweb)
}

func (c *Client) base() (*url.URL, error) {
	if c == nil || strings.TrimSpace(c.BaseURL) == "" {
		return nil, fmt.Errorf("dicomweb: empty BaseURL")
	}
	u, err := url.Parse(strings.TrimRight(c.BaseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("dicomweb: BaseURL: %w", err)
	}
	return u, nil
}

// ErrInvalidPath reports a path segment that cannot be used as given.
//
// In DICOMweb the resource is the path, and the variable segments are UIDs: a UID
// carrying a "/" or a ".." segment names a different instance, series or service
// than the caller asked for, and the request that goes out looks perfectly
// well-formed. Percent-escaping such a segment instead would only turn it into a
// request no server can answer, so a UID that is not a UID is an error here —
// before anything is sent.
var ErrInvalidPath = errors.New("dicomweb: invalid path segment")

func (c *Client) resolve(parts ...string) (string, error) {
	base, err := c.base()
	if err != nil {
		return "", err
	}
	joined := strings.TrimRight(base.Path, "/")
	for _, p := range parts {
		p = strings.Trim(p, "/")
		if err := checkPathSegment(p); err != nil {
			return "", err
		}
		joined += "/" + p
	}
	out := *base
	out.Path = joined
	out.RawQuery = ""
	out.Fragment = ""
	return out.String(), nil
}

// checkPathSegment accepts one path segment of a DICOMweb URL. The variable
// segments are UIDs — digits and dots — and the fixed ones are words such as
// "studies", so RFC 3986's unreserved set covers every legitimate value. "." and
// ".." are excluded on top of that, since a path resolves them away instead of
// carrying them.
func checkPathSegment(p string) error {
	if p == "" {
		return fmt.Errorf("%w: empty", ErrInvalidPath)
	}
	if p == "." || p == ".." {
		return fmt.Errorf("%w: %q names another resource", ErrInvalidPath, p)
	}
	for i := 0; i < len(p); i++ {
		switch ch := p[i]; {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9':
		case ch == '-', ch == '.', ch == '_', ch == '~':
		default:
			return fmt.Errorf("%w: %q contains %q", ErrInvalidPath, p, string(rune(ch)))
		}
	}
	return nil
}

func (c *Client) do(ctx context.Context, req *http.Request) (*http.Response, error) {
	log := c.logger(ctx)
	if log.Enabled(ctx, slog.LevelDebug) {
		log.DebugContext(ctx, "request",
			gonetdicom.AttrMethod, req.Method,
			gonetdicom.AttrURL, req.URL.String(),
		)
	}
	resp, err := c.httpClient().Do(req.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	if log.Enabled(ctx, slog.LevelDebug) {
		log.DebugContext(ctx, "response",
			gonetdicom.AttrHTTPStatus, resp.StatusCode,
			gonetdicom.AttrURL, req.URL.String(),
		)
	}
	return resp, nil
}

func readErrorBody(resp *http.Response) string {
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	return strings.TrimSpace(string(b))
}

func checkStatus(resp *http.Response, want ...int) error {
	for _, code := range want {
		if resp.StatusCode == code {
			return nil
		}
	}
	body := readErrorBody(resp)
	if body == "" {
		return fmt.Errorf("dicomweb: unexpected status %s", resp.Status)
	}
	return fmt.Errorf("dicomweb: unexpected status %s: %s", resp.Status, body)
}

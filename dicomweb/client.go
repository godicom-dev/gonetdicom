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
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
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
	Logger     *slog.Logger  // optional request logging
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

func (c *Client) log() *slog.Logger {
	if c == nil {
		return nil
	}
	return c.Logger
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

func (c *Client) resolve(parts ...string) (string, error) {
	base, err := c.base()
	if err != nil {
		return "", err
	}
	joined := strings.TrimRight(base.Path, "/")
	for _, p := range parts {
		p = strings.Trim(p, "/")
		if p == "" {
			continue
		}
		joined += "/" + p
	}
	out := *base
	out.Path = joined
	out.RawQuery = ""
	out.Fragment = ""
	return out.String(), nil
}

func (c *Client) do(ctx context.Context, req *http.Request) (*http.Response, error) {
	if log := c.log(); log != nil {
		log.Debug("dicomweb: request", "method", req.Method, "url", req.URL.String())
	}
	resp, err := c.httpClient().Do(req.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	if log := c.log(); log != nil {
		log.Debug("dicomweb: response", "status", resp.StatusCode, "url", req.URL.String())
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

package dicomweb

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// ClientOption configures a Client built by NewClient.
type ClientOption func(*Client) error

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(hc *http.Client) ClientOption {
	return func(c *Client) error {
		c.HTTPClient = hc
		return nil
	}
}

// WithTimeout sets the default HTTP client timeout (ignored if WithHTTPClient is used).
func WithTimeout(d time.Duration) ClientOption {
	return func(c *Client) error {
		if d <= 0 {
			return fmt.Errorf("dicomweb: timeout must be positive")
		}
		c.Timeout = d
		return nil
	}
}

// WithTLSConfig installs a TLS client config on the default transport.
func WithTLSConfig(cfg *tls.Config) ClientOption {
	return func(c *Client) error {
		c.TLS = cfg
		return nil
	}
}

// WithLogger sets an optional slog logger for request diagnostics.
func WithLogger(log *slog.Logger) ClientOption {
	return func(c *Client) error {
		c.Logger = log
		return nil
	}
}

// NewClient builds a Client with BaseURL and options.
func NewClient(baseURL string, opts ...ClientOption) (*Client, error) {
	c := &Client{
		BaseURL: baseURL,
		Timeout: 30 * time.Second,
	}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(c); err != nil {
			return nil, err
		}
	}
	if c.HTTPClient == nil {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		if c.TLS != nil {
			tr.TLSClientConfig = c.TLS
		}
		c.HTTPClient = &http.Client{
			Timeout:   c.Timeout,
			Transport: tr,
		}
	}
	return c, nil
}

//go:build no_statsd

package statsd

import "net/http"

// Client is a no-op stub used when the no_statsd build tag is active.
type Client struct{}

func New(_, _ string) (*Client, error)                           { return &Client{}, nil }
func (c *Client) Count(_ string, _ int64, _ map[string]string)   {}
func (c *Client) Timing(_ string, _ int64, _ map[string]string)  {}
func (c *Client) Gauge(_ string, _ float64, _ map[string]string) {}

func Instrument(_ *Client, next http.Handler) http.Handler { return next }

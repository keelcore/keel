//go:build !no_statsd

package statsd

import (
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Client sends StatsD metrics over UDP using DogStatsD tag syntax.
type Client struct {
	conn   net.Conn
	prefix string
}

// New dials the UDP endpoint and returns a ready Client.
func New(endpoint, prefix string) (*Client, error) {
	conn, err := net.Dial("udp", endpoint)
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn, prefix: prefix}, nil
}

// Count emits a counter metric.
func (c *Client) Count(name string, value int64, tags map[string]string) {
	c.emit(fmt.Sprintf("%s.%s:%d|c%s", c.prefix, name, value, tagsStr(tags)))
}

// Timing emits a timer metric in milliseconds.
func (c *Client) Timing(name string, ms int64, tags map[string]string) {
	c.emit(fmt.Sprintf("%s.%s:%d|ms%s", c.prefix, name, ms, tagsStr(tags)))
}

// Gauge emits a gauge metric.
func (c *Client) Gauge(name string, value float64, tags map[string]string) {
	c.emit(fmt.Sprintf("%s.%s:%g|g%s", c.prefix, name, value, tagsStr(tags)))
}

func (c *Client) emit(msg string) {
	_, _ = c.conn.Write([]byte(msg))
}

// tagsStr formats a tag map as DogStatsD "|#key:val,key:val" (sorted for determinism).
func tagsStr(tags map[string]string) string {
	if len(tags) == 0 {
		return ""
	}
	parts := make([]string, 0, len(tags))
	for k, v := range tags {
		parts = append(parts, k+":"+v)
	}
	sort.Strings(parts)
	return "|#" + strings.Join(parts, ",")
}

// statusCapture records the response status code.
type statusCapture struct {
	http.ResponseWriter
	status int
}

func (sc *statusCapture) WriteHeader(code int) {
	sc.status = code
	sc.ResponseWriter.WriteHeader(code)
}

// Instrument wraps next to emit RED metrics (requests_total, request_duration_ms)
// to the StatsD client after each request.
func Instrument(c *Client, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sc := &statusCapture{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sc, r)
		tags := map[string]string{
			"method": r.Method,
			"status": strconv.Itoa(sc.status),
		}
		c.Count("requests_total", 1, tags)
		c.Timing("request_duration_ms", time.Since(start).Milliseconds(), tags)
	})
}
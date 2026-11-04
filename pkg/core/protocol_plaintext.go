// pkg/core/protocol_plaintext.go
package core

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/keelcore/keel/pkg/config"
	"github.com/keelcore/keel/pkg/core/protonotice"
)

// tlsHandshakeRecord is the first byte of every TLS connection (record type 22).
const tlsHandshakeRecord = 0x16

// plaintextListener answers plain HTTP sent to the HTTPS port with the
// KEEL-PROTO-006 rejection instead of net/http's built-in 400. The check runs on
// a connection's first read, inside the per-connection handshake, so a slow
// client never blocks Accept.
type plaintextListener struct {
	net.Listener
	cfg      config.Config
	rejected func(protonotice.Code)
}

func (l plaintextListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &sniffConn{Conn: c, l: l}, nil
}

// sniffConn inspects the first byte: a TLS record passes through; anything else is
// read as an HTTP request, answered with the rejection, and closed.
type sniffConn struct {
	net.Conn
	l       plaintextListener
	checked bool
	first   []byte
}

func (c *sniffConn) Read(p []byte) (int, error) {
	if !c.checked {
		c.checked = true
		var b [1]byte
		if _, err := io.ReadFull(c.Conn, b[:]); err != nil {
			return 0, err
		}
		if b[0] != tlsHandshakeRecord {
			c.rejectPlaintext(b[0])
			return 0, io.EOF
		}
		c.first = b[:]
	}
	if len(c.first) > 0 && len(p) > 0 {
		p[0] = c.first[0]
		c.first = nil
		return 1, nil
	}
	return c.Conn.Read(p)
}

func (c *sniffConn) rejectPlaintext(first byte) {
	defer c.Conn.Close()
	_ = c.Conn.SetDeadline(time.Now().Add(5 * time.Second))
	host := ""
	br := bufio.NewReader(io.MultiReader(bytes.NewReader([]byte{first}), c.Conn))
	if req, err := http.ReadRequest(br); err == nil {
		host = req.Host
	}
	if c.l.rejected != nil {
		c.l.rejected(protonotice.PlaintextOnHTTPS)
	}
	_ = protonotice.WriteRawRejection(c.Conn, protonotice.PlaintextOnHTTPS, noticeBase(c.l.cfg, host), "")
}

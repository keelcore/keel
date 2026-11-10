package core

import (
	"net"
	"testing"
)

// A client that closes before sending a byte: the read error is returned as is.
func TestSniffConn_ClosedBeforeFirstByte(t *testing.T) {
	server, client := net.Pipe()
	_ = client.Close()
	c := &sniffConn{Conn: server}
	if _, err := c.Read(make([]byte, 4)); err == nil {
		t.Fatal("want the read error")
	}
}

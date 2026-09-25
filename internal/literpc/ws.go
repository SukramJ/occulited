package literpc

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// A WebSocket server for one direction (RFC 6455, D-74): the handshake, unmasked text frames
// out, pings out, and a reader that only consumes what the client sends - pongs, a close, and
// text frames it ignores. The stdlib has no WebSocket, and a dependency for ~150 lines of
// framing is not worth its supply chain (D-15's argument).

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// WSConn is one upgraded connection.
type WSConn struct {
	conn net.Conn
	rw   *bufio.ReadWriter
	wmu  sync.Mutex
	done chan struct{}
	once sync.Once
	// PongAt is when the last pong arrived (the heartbeat's liveness).
	pongMu sync.Mutex
	pongAt time.Time
	// WriteTimeout bounds one frame's write; a client that does not read is closed.
	WriteTimeout time.Duration
}

// IsWebSocket says whether a request asks for the upgrade.
func IsWebSocket(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") && strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

// Upgrade performs the handshake and hijacks the connection. The subprotocol is echoed when
// the client offers it. An error here was answered already (400/426).
func Upgrade(w http.ResponseWriter, r *http.Request) (*WSConn, error) {
	if !IsWebSocket(r) {
		http.Error(w, "a WebSocket upgrade is expected", http.StatusUpgradeRequired)
		return nil, errors.New("not an upgrade")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" || r.Header.Get("Sec-WebSocket-Version") != "13" {
		http.Error(w, "Sec-WebSocket-Key and version 13 are required", http.StatusBadRequest)
		return nil, errors.New("bad handshake")
	}
	// through the ResponseController, which unwraps a middleware's wrapper (main's
	// statusRecorder says Unwrap) down to the server's own writer, the hijacker
	conn, rw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		http.Error(w, "the connection cannot be upgraded here: "+err.Error(), http.StatusInternalServerError)
		return nil, err
	}
	h := sha1.Sum([]byte(key + wsGUID))
	resp := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(h[:]) + "\r\n"
	for _, p := range strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",") {
		if strings.TrimSpace(p) == Subprotocol {
			resp += "Sec-WebSocket-Protocol: " + Subprotocol + "\r\n"
			break
		}
	}
	resp += "\r\n"
	if _, err := rw.WriteString(resp); err != nil {
		conn.Close()
		return nil, err
	}
	if err := rw.Flush(); err != nil {
		conn.Close()
		return nil, err
	}
	c := &WSConn{conn: conn, rw: rw, done: make(chan struct{}), WriteTimeout: 30 * time.Second, pongAt: time.Now()}
	go c.readLoop()
	return c, nil
}

// Done is closed when the client closed or the connection failed.
func (c *WSConn) Done() <-chan struct{} { return c.done }

// PongAt is when the client last answered a ping (or connected).
func (c *WSConn) PongAt() time.Time {
	c.pongMu.Lock()
	defer c.pongMu.Unlock()
	return c.pongAt
}

func (c *WSConn) finish() {
	c.once.Do(func() { close(c.done); c.conn.Close() })
}

// writeFrame writes one unmasked frame.
func (c *WSConn) writeFrame(opcode byte, payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	select {
	case <-c.done:
		return io.ErrClosedPipe
	default:
	}
	if c.WriteTimeout > 0 {
		_ = c.conn.SetWriteDeadline(time.Now().Add(c.WriteTimeout))
	}
	head := []byte{0x80 | opcode}
	n := len(payload)
	switch {
	case n < 126:
		head = append(head, byte(n))
	case n < 1<<16:
		head = append(head, 126, byte(n>>8), byte(n))
	default:
		head = append(head, 127)
		head = binary.BigEndian.AppendUint64(head, uint64(n))
	}
	if _, err := c.rw.Write(head); err != nil {
		c.finish()
		return err
	}
	if _, err := c.rw.Write(payload); err != nil {
		c.finish()
		return err
	}
	if err := c.rw.Flush(); err != nil {
		c.finish()
		return err
	}
	return nil
}

// WriteText sends one text frame.
func (c *WSConn) WriteText(b []byte) error { return c.writeFrame(0x1, b) }

// Ping sends a ping; the client's pong is noted by the reader.
func (c *WSConn) Ping() error { return c.writeFrame(0x9, []byte("ping")) }

// Close sends a close frame with the code and reason and closes the connection.
func (c *WSConn) Close(code int, reason string) {
	payload := []byte{byte(code >> 8), byte(code)}
	payload = append(payload, reason...)
	_ = c.writeFrame(0x8, payload)
	c.finish()
}

// readLoop consumes the client's frames: a ping is answered, a pong noted, a close closes,
// text and binary are ignored (the stream is one-way; resume is by reconnecting).
func (c *WSConn) readLoop() {
	defer c.finish()
	for {
		_ = c.conn.SetReadDeadline(time.Time{})
		var h [2]byte
		if _, err := io.ReadFull(c.rw, h[:]); err != nil {
			return
		}
		opcode := h[0] & 0x0f
		masked := h[1]&0x80 != 0
		n := uint64(h[1] & 0x7f)
		switch n {
		case 126:
			var b [2]byte
			if _, err := io.ReadFull(c.rw, b[:]); err != nil {
				return
			}
			n = uint64(binary.BigEndian.Uint16(b[:]))
		case 127:
			var b [8]byte
			if _, err := io.ReadFull(c.rw, b[:]); err != nil {
				return
			}
			n = binary.BigEndian.Uint64(b[:])
		}
		if n > 1<<20 {
			return // a client frame of over a megabyte is not one of ours
		}
		var mask [4]byte
		if masked {
			if _, err := io.ReadFull(c.rw, mask[:]); err != nil {
				return
			}
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(c.rw, payload); err != nil {
			return
		}
		if masked {
			for i := range payload {
				payload[i] ^= mask[i%4]
			}
		}
		switch opcode {
		case 0x8:
			code := 1000
			if len(payload) >= 2 {
				code = int(binary.BigEndian.Uint16(payload[:2]))
			}
			_ = c.writeFrame(0x8, []byte{byte(code >> 8), byte(code)})
			return
		case 0x9:
			_ = c.writeFrame(0xA, payload)
		case 0xA:
			c.pongMu.Lock()
			c.pongAt = time.Now()
			c.pongMu.Unlock()
		}
	}
}

// wsSink is the WebSocket encoder: {"id", "type", "data"} per text frame.
type wsSink struct {
	c *WSConn
}

func (s wsSink) Send(id, typ string, data any) error {
	b, err := marshalWS(id, typ, data)
	if err != nil {
		return err
	}
	return s.c.WriteText(b)
}

func (s wsSink) Ping() error {
	if time.Since(s.c.PongAt()) > 3*Heartbeat {
		s.c.Close(CloseNormal, "no pong")
		return fmt.Errorf("no pong for %s", 3*Heartbeat)
	}
	return s.c.Ping()
}

func (s wsSink) Close(code int, reason string) { s.c.Close(code, reason) }

// NewWSSink is the sink for an upgraded connection.
func NewWSSink(c *WSConn) Sink { return wsSink{c: c} }

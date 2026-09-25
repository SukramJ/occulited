// Package mqttpub mirrors the metadata store to an MQTT broker as retained messages in
// mqtt-smarthome style (meta-api.md, "Optional MQTT publication"): a consumer without an HTTP
// client still gets the names. It is a mirror of the store, never an input to it.
//
// The client below is the subset of MQTT 3.1.1 this needs - CONNECT, PUBLISH at QoS 0 with the
// retain flag, PINGREQ, DISCONNECT - written here rather than pulling in a library for four
// packet types (AGENTS.md: stdlib first).
package mqttpub

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// Conn is one MQTT connection.
type Conn struct {
	mu   sync.Mutex
	c    net.Conn
	r    *bufio.Reader
	keep time.Duration
}

// Dial connects and authenticates. clientID must be unique per broker; keepalive in seconds.
func Dial(ctx context.Context, addr, clientID, username, password string, keepalive time.Duration) (*Conn, error) {
	d := net.Dialer{Timeout: 10 * time.Second}
	nc, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	c := &Conn{c: nc, r: bufio.NewReader(nc), keep: keepalive}
	// CONNECT: protocol name, level 4, flags (clean session + user/pass), keepalive, payload
	flags := byte(0x02)
	payload := mqttString(clientID)
	if username != "" {
		flags |= 0x80
		payload = append(payload, mqttString(username)...)
		if password != "" {
			flags |= 0x40
			payload = append(payload, mqttString(password)...)
		}
	}
	ka := uint16(keepalive / time.Second)
	varHeader := append(mqttString("MQTT"), 4, flags, byte(ka>>8), byte(ka))
	_ = nc.SetDeadline(time.Now().Add(10 * time.Second))
	if err := c.write(0x10, append(varHeader, payload...)); err != nil {
		nc.Close()
		return nil, err
	}
	typ, body, err := c.read()
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("CONNACK: %w", err)
	}
	if typ != 0x20 || len(body) < 2 {
		nc.Close()
		return nil, errors.New("CONNACK: unexpected packet")
	}
	if body[1] != 0 {
		nc.Close()
		return nil, fmt.Errorf("CONNACK: refused (code %d)", body[1])
	}
	_ = nc.SetDeadline(time.Time{})
	return c, nil
}

// Publish sends one retained QoS 0 message; an empty payload clears the retained value.
func (c *Conn) Publish(topic string, payload []byte) error {
	pkt := append(mqttString(topic), payload...)
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return c.write(0x30|0x01, pkt) // retain
}

// Ping keeps the connection alive; the broker answers PINGRESP, read by the background reader.
func (c *Conn) Ping() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return c.write(0xC0, nil)
}

// Close sends DISCONNECT and closes.
func (c *Conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.write(0xE0, nil)
	return c.c.Close()
}

// Serve reads packets until the connection dies (PINGRESP and anything else are discarded).
func (c *Conn) Serve() error {
	for {
		if _, _, err := c.read(); err != nil {
			return err
		}
	}
}

func (c *Conn) write(header byte, body []byte) error {
	buf := []byte{header}
	n := len(body)
	for {
		b := byte(n % 128)
		n /= 128
		if n > 0 {
			b |= 0x80
		}
		buf = append(buf, b)
		if n == 0 {
			break
		}
	}
	_, err := c.c.Write(append(buf, body...))
	return err
}

func (c *Conn) read() (byte, []byte, error) {
	h, err := c.r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	n, mult := 0, 1
	for i := 0; i < 4; i++ {
		b, err := c.r.ReadByte()
		if err != nil {
			return 0, nil, err
		}
		n += int(b&0x7F) * mult
		if b&0x80 == 0 {
			break
		}
		mult *= 128
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(c.r, body); err != nil {
		return 0, nil, err
	}
	return h & 0xF0, body, nil
}

func mqttString(s string) []byte {
	return append([]byte{byte(len(s) >> 8), byte(len(s))}, s...)
}

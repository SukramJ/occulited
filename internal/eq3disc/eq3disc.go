// Package eq3disc answers eQ-3's discovery on UDP 43439 (task 163), in place of `eq3configd`.
//
// eq3configd is not a radio daemon, despite its unit's "Device configuration daemon": it is the
// counterpart of eQ-3's NetFinder, and it both answers "who are you" and *writes the network
// configuration* on request. The second half is what it is dropped for - the maintainer,
// 2026-09-18: "no network configuration anymore with Netfinder on openccu-lite". Everything an
// openccu-lite system changes about its network is changed on its Network page.
//
// What is kept is the first half, because our own tools find a system with it: homematic-manager,
// hm2mqtt.js and mqtt-interfaces-core all send the same datagram (version 2, type `eQ3-*`, serial
// `*`, opcode `I`) and read the type, the serial and the version out of the answer.
//
// The wire format, as eq3configd had it and as a lab box answered it byte for byte (2026-09-22):
//
//	request  <version> [<sender id 3B> <counter 1B> if version >= 2] <type> 00 <serial> 00 <opcode> [payload]
//	answer   <the same first bytes> <own type> 00 <own serial> 00 '>' <opcode> <answer>
//
// with the answer to `I` being 01, the version string, 00, 00, and the answer to anything else the
// single NAK byte 00. The type and the serial of a request are patterns: they are compared
// character by character and a `*` ends the comparison with a match, so `eQ3-*` matches, `*`
// matches, and an exact `eQ3-HmIP-CCU3-App` matches as well (the comparison stops at the end of
// the shorter string).
//
// What is deliberately not kept, beyond the writing: no network restart from a UDP packet (any
// request on a system whose netconfig had an empty CURRENT_IP made eq3configd restart the network),
// no reading of `netconfig` at all, and no encryption with the built-in default key.
package eq3disc

import (
	"bytes"
	"strings"
)

// Port is eQ-3's discovery port.
const Port = 43439

// The opcodes. Only Identify is answered; everything else gets the NAK.
const (
	OpIdentify = 'I' // "who are you": the type, the serial and the version
	OpCurrent  = 'n' // eq3configd: the CURRENT_* values of /etc/config/netconfig
	OpConfig   = 'c' // eq3configd: the configured values, the hostname and the crypt byte
	OpWrite    = 'C' // eq3configd: write netconfig and restart the network
)

// NAK is the answer to every opcode but Identify.
const NAK = 0x00

// replyMark is the byte between the own serial and the opcode in an answer.
const replyMark = '>'

// DefaultType is what this system calls itself. The maintainer, 2026-09-18: "if everything probes
// eQ3-* then i would suggest we keep that. perhaps just append -lite to the standard response" -
// so the prefix stays and every probe in the field still matches, while a reader of the answer
// sees at once that this is not a CCU3.
const DefaultType = "eQ3-HmIP-CCU3-App-lite"

// Request is a parsed datagram.
type Request struct {
	// Version is the protocol's first byte. From 2 on, a sender id and a counter follow it, and
	// the answer has to carry them back unchanged - that is how a client tells its own answer
	// from another one on a broadcast.
	Version byte
	Sender  [3]byte
	Counter byte
	// Type and Serial are patterns, not names: see IsTarget.
	Type, Serial string
	Opcode       byte
}

// ParseRequest reads a datagram. ok is false for anything that is not one - too short, no
// terminator, or an answer somebody else broadcast (those begin with the same header but carry a
// '>' where a request has its opcode; they are recognised by the marker after the serial).
func ParseRequest(b []byte) (Request, bool) {
	if len(b) < 2 {
		return Request{}, false
	}
	r := Request{Version: b[0]}
	at := 1
	if r.Version >= 2 {
		if len(b) < 6 {
			return Request{}, false
		}
		copy(r.Sender[:], b[1:4])
		r.Counter = b[4]
		at = 5
	}
	str := func() (string, bool) {
		end := bytes.IndexByte(b[at:], 0)
		if end < 0 {
			return "", false
		}
		s := string(b[at : at+end])
		at += end + 1
		return s, true
	}
	var ok bool
	if r.Type, ok = str(); !ok {
		return Request{}, false
	}
	if r.Serial, ok = str(); !ok {
		return Request{}, false
	}
	if at >= len(b) {
		return Request{}, false
	}
	r.Opcode = b[at]
	// an answer, not a request: somebody else's reply heard on the broadcast
	if r.Opcode == replyMark {
		return Request{}, false
	}
	return r, true
}

// IsTarget is eq3configd's `udpframe::IsTarget`, character by character: a `*` in the pattern ends
// the comparison with a match, and so does the end of either string. That last part is why an
// exact `eQ3-HmIP-CCU3-App` probe finds a system that calls itself `eQ3-HmIP-CCU3-App-lite` - the
// comparison stops when the pattern runs out - and why no second pattern is needed here.
func IsTarget(pattern, own string) bool {
	for i := 0; i < len(pattern) && i < len(own); i++ {
		if pattern[i] == '*' {
			return true
		}
		if pattern[i] != own[i] {
			return false
		}
	}
	return true
}

// Device is this system as the discovery answers for it.
type Device struct {
	// Type is what it calls itself; empty = DefaultType.
	Type string
	// Serial is /var/board_sgtin, else /var/board_serial.
	Serial string
	// SerialFunc, when set, is asked instead of Serial for every request: the serial can become
	// known after the responder started (B-199, ssdp.Identity).
	SerialFunc func() string
	// Version is the string a client shows as the firmware.
	Version string
}

func (d Device) serial() string {
	if d.SerialFunc != nil {
		return d.SerialFunc()
	}
	return d.Serial
}

func (d Device) typ() string {
	if d.Type == "" {
		return DefaultType
	}
	return d.Type
}

// Answers says whether this request is for this system: both patterns have to match, as
// eq3configd required.
func (d Device) Answers(r Request) bool {
	return IsTarget(r.Type, d.typ()) && IsTarget(r.Serial, d.serial())
}

// Wildcard says whether the request named a serial pattern rather than this system's serial. Such
// a probe is a broadcast that every system on the network hears, so the answer waits a random
// moment first (eq3configd: up to 1.3 s) instead of every system answering at once.
func Wildcard(r Request) bool { return strings.ContainsRune(r.Serial, '*') }

// Reply builds an answer: the request's first bytes back unchanged, this system's type and serial,
// the marker, the opcode, and the body.
func (d Device) Reply(r Request, body []byte) []byte {
	var b bytes.Buffer
	b.WriteByte(r.Version)
	if r.Version >= 2 {
		b.Write(r.Sender[:])
		b.WriteByte(r.Counter)
	}
	b.WriteString(d.typ())
	b.WriteByte(0)
	b.WriteString(d.serial())
	b.WriteByte(0)
	b.WriteByte(replyMark)
	b.WriteByte(r.Opcode)
	b.Write(body)
	return b.Bytes()
}

// Answer is what goes back for this request: the identity for `I`, the NAK for everything else.
// `n`, `c` and `C` - read the network configuration, and write it - are the NAK too: the Network
// page is the one place an openccu-lite system's network is changed.
func (d Device) Answer(r Request) []byte {
	if r.Opcode != OpIdentify {
		return d.Reply(r, []byte{NAK})
	}
	// 01, the version, its terminator, and the count of service protocols, which is none. The
	// bytes are a lab box's own answer, read off the wire before this was written.
	body := make([]byte, 0, len(d.Version)+3)
	body = append(body, 0x01)
	body = append(body, d.Version...)
	body = append(body, 0x00, 0x00)
	return d.Reply(r, body)
}

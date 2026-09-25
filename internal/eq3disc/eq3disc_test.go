package eq3disc

import (
	"bytes"
	"testing"
)

// The probe homematic-manager, hm2mqtt.js and mqtt-interfaces-core all send: version 2, the sender
// id and counter, type `eQ3-*`, serial `*`, opcode `I`.
var probe = append([]byte{0x02, 0x8f, 0x91, 0xc0, 0x01}, []byte("eQ3-*\x00*\x00I")...)

var box = Device{Serial: "3014F711A0001F58A9A728D4", Version: "1.0.0-dev.19"}

func TestParseRequest(t *testing.T) {
	r, ok := ParseRequest(probe)
	if !ok {
		t.Fatal("the probe every tool sends must parse")
	}
	if r.Version != 2 || r.Sender != [3]byte{0x8f, 0x91, 0xc0} || r.Counter != 1 {
		t.Errorf("header: %+v", r)
	}
	if r.Type != "eQ3-*" || r.Serial != "*" || r.Opcode != OpIdentify {
		t.Errorf("body: %+v", r)
	}

	// version 1 has no sender id and no counter
	v1 := append([]byte{0x01}, []byte("eQ3-*\x00*\x00I")...)
	if r, ok := ParseRequest(v1); !ok || r.Version != 1 || r.Type != "eQ3-*" || r.Opcode != OpIdentify {
		t.Errorf("version 1: %+v %v", r, ok)
	}

	for _, c := range []struct {
		name string
		in   []byte
	}{
		{"empty", nil},
		{"one byte", []byte{0x02}},
		{"a version 2 header cut short", []byte{0x02, 0x8f, 0x91}},
		{"no terminator after the type", append([]byte{0x02, 0x8f, 0x91, 0xc0, 0x01}, []byte("eQ3-*")...)},
		{"no serial", append([]byte{0x02, 0x8f, 0x91, 0xc0, 0x01}, []byte("eQ3-*\x00*")...)},
		{"no opcode", append([]byte{0x02, 0x8f, 0x91, 0xc0, 0x01}, []byte("eQ3-*\x00*\x00")...)},
		// somebody else's answer, heard on the broadcast: the marker stands where an opcode would
		{"an answer, not a request", append([]byte{0x02, 0x8f, 0x91, 0xc0, 0x01}, []byte("eQ3-HmIP-CCU3-App\x00SERIAL\x00>I\x01x\x00\x00")...)},
	} {
		if _, ok := ParseRequest(c.in); ok {
			t.Errorf("%s must not parse as a request", c.name)
		}
	}
}

// eq3configd's own comparison: character by character, a `*` ends it with a match, and so does the
// end of either string.
func TestIsTarget(t *testing.T) {
	const own = DefaultType
	for _, pattern := range []string{"eQ3-*", "*", "eQ3-HmIP-CCU3-App", DefaultType, "eQ3-HmIP-CCU3-App-lite-and-more"} {
		if !IsTarget(pattern, own) {
			t.Errorf("%q must match %q", pattern, own)
		}
	}
	for _, pattern := range []string{"eQ3-HM-CCU2-App", "HmIP-*", "eQ3-HmIP-CCU2*"} {
		if IsTarget(pattern, own) {
			t.Errorf("%q must not match %q", pattern, own)
		}
	}
	// the serial is compared the same way
	if !IsTarget("*", box.Serial) || !IsTarget("3014F711*", box.Serial) || IsTarget("3014F999*", box.Serial) {
		t.Error("the serial patterns")
	}
}

func TestAnswersAndWildcard(t *testing.T) {
	r, _ := ParseRequest(probe)
	if !box.Answers(r) {
		t.Error("the probe every tool sends must be answered")
	}
	if !Wildcard(r) {
		t.Error("a serial pattern is a broadcast probe: the answer waits a moment")
	}
	// a foreign type is not this system
	foreign, _ := ParseRequest(append([]byte{0x02, 0x8f, 0x91, 0xc0, 0x01}, []byte("eQ3-HM-CCU2-App\x00*\x00I")...))
	if box.Answers(foreign) {
		t.Error("a probe for another product must not be answered")
	}
	// this system by its own serial: answered, and without a wait
	exact, _ := ParseRequest(append([]byte{0x02, 0x8f, 0x91, 0xc0, 0x01}, []byte("*\x00"+box.Serial+"\x00I")...))
	if !box.Answers(exact) || Wildcard(exact) {
		t.Error("a probe naming this system's serial is answered at once")
	}
	// another system's serial
	other, _ := ParseRequest(append([]byte{0x02, 0x8f, 0x91, 0xc0, 0x01}, []byte("*\x003014F711000000000000000A\x00I")...))
	if box.Answers(other) {
		t.Error("another system's serial must not be answered")
	}
}

// The bytes a lab box (the Charly, eq3configd, 2026-09-22) answered, with this system's type and
// version in place of its own. The shape is the contract: homematic-manager checks the first five
// bytes, then reads the type and the serial to their terminators, skips three bytes and reads the
// version.
func TestIdentifyAnswer(t *testing.T) {
	r, _ := ParseRequest(probe)
	got := box.Answer(r)
	want := append([]byte{0x02, 0x8f, 0x91, 0xc0, 0x01}, []byte(DefaultType+"\x00"+box.Serial+"\x00>I\x011.0.0-dev.19\x00\x00")...)
	if !bytes.Equal(got, want) {
		t.Fatalf("the answer is\n%q\nwant\n%q", got, want)
	}
	// what a client reads out of it
	if !bytes.HasPrefix(got, []byte{0x02, 0x8f, 0x91, 0xc0, 0x01}) {
		t.Error("the first five bytes are the request's, or a client drops the answer")
	}
	at := 5
	read := func() string {
		end := bytes.IndexByte(got[at:], 0)
		s := string(got[at : at+end])
		at += end + 1
		return s
	}
	if typ := read(); typ != DefaultType {
		t.Errorf("type %q", typ)
	}
	if serial := read(); serial != box.Serial {
		t.Errorf("serial %q", serial)
	}
	at += 3 // '>', the opcode, and the byte before the version
	if v := read(); v != "1.0.0-dev.19" {
		t.Errorf("version %q", v)
	}
}

// Everything but Identify is refused: no network configuration from a UDP packet.
func TestEverythingElseIsRefused(t *testing.T) {
	for _, op := range []byte{OpCurrent, OpConfig, OpWrite, 'Z', 0x00, 0xff} {
		req := append([]byte{0x02, 0x8f, 0x91, 0xc0, 0x01}, []byte("eQ3-*\x00*\x00")...)
		req = append(req, op)
		r, ok := ParseRequest(req)
		if !ok {
			t.Fatalf("opcode %q did not parse", op)
		}
		got := box.Answer(r)
		want := append([]byte{0x02, 0x8f, 0x91, 0xc0, 0x01}, []byte(DefaultType+"\x00"+box.Serial+"\x00>")...)
		want = append(want, op, NAK)
		if !bytes.Equal(got, want) {
			t.Errorf("opcode %q:\n%q\nwant\n%q", op, got, want)
		}
	}
}

// A version 1 request gets a version 1 answer: no sender id, no counter.
func TestVersionOneAnswer(t *testing.T) {
	r, _ := ParseRequest(append([]byte{0x01}, []byte("*\x00*\x00I")...))
	got := box.Answer(r)
	want := append([]byte{0x01}, []byte(DefaultType+"\x00"+box.Serial+"\x00>I\x011.0.0-dev.19\x00\x00")...)
	if !bytes.Equal(got, want) {
		t.Errorf("\n%q\nwant\n%q", got, want)
	}
}

// B-199: the serial is asked for every request, so an answer after the radio detection has written
// /var/board_sgtin carries the SGTIN, and a probe for that SGTIN is answered, not only for the
// host name the responder saw at start.
func TestSerialFunc(t *testing.T) {
	serial := "ccu-vm-1"
	d := Device{Serial: "ignored", SerialFunc: func() string { return serial }, Version: "1.0.0-dev.24"}
	r, _ := ParseRequest(probe)
	if got := d.Answer(r); !bytes.Contains(got, []byte("\x00ccu-vm-1\x00>I")) {
		t.Fatalf("before: %q", got)
	}
	serial = "3014F711A000041709ADFA5E"
	if got := d.Answer(r); !bytes.Contains(got, []byte("\x00"+serial+"\x00>I")) {
		t.Fatalf("after: %q", got)
	}
	exact := Request{Version: 1, Type: "eQ3-*", Serial: serial, Opcode: OpIdentify}
	if !d.Answers(exact) {
		t.Error("a probe for the SGTIN is not answered")
	}
	if d.Answers(Request{Version: 1, Type: "eQ3-*", Serial: "ccu-vm-1", Opcode: OpIdentify}) {
		t.Error("a probe for the old host name is still answered")
	}
}

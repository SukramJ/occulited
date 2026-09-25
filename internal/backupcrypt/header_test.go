package backupcrypt

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"strings"
	"testing"

	"filippo.io/age"
	"filippo.io/age/armor"
)

func encryptTo(t *testing.T, plain []byte, meta MetaRecipient, recipients ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := Encrypt(&buf, meta, recipients...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSniffAndDecrypt(t *testing.T) {
	box, _ := age.GenerateX25519Identity()
	rec, _ := NewSecret()
	other, _ := age.GenerateX25519Identity()
	plain := make([]byte, 200*1024) // four chunks
	_, _ = rand.Read(plain)
	meta := MetaRecipient{BoxFingerprint: Fingerprint(box.Recipient().String()), RecoveryFingerprint: rec.Fingerprint()}
	enc := encryptTo(t, plain, meta, box.Recipient().String(), rec.Recipient())

	h, r, err := Sniff(bytes.NewReader(enc))
	if err != nil {
		t.Fatal(err)
	}
	if !h.Encrypted || h.Armored || h.Stanzas != 2 || h.Scrypt || h.BoxFingerprint != meta.BoxFingerprint || h.RecoveryFingerprint != rec.Fingerprint() {
		t.Fatalf("header %+v", h)
	}
	if !h.Opens(box) || !h.Opens(rec.Identity()) || h.Opens(other) {
		t.Error("Opens")
	}
	// the reader yields the whole file again, and each identity decrypts it
	for _, id := range []age.Identity{box, rec.Identity()} {
		_, r, _ = Sniff(bytes.NewReader(enc))
		out, err := Decrypt(r, id)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(out)
		if err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("decrypt: %v, %d bytes", err, len(got))
		}
	}
	// the wrong key is refused at the header, before any payload
	_, r, _ = Sniff(bytes.NewReader(enc))
	if _, err := Decrypt(r, other); !errors.Is(err, ErrWrongKey) {
		t.Errorf("wrong key: %v", err)
	}
	// the age tool reads the file as a plain age file: the meta stanza is ignored by the library too
	if out, err := age.Decrypt(bytes.NewReader(enc), rec.Identity()); err != nil {
		t.Errorf("age.Decrypt: %v", err)
	} else if got, _ := io.ReadAll(out); !bytes.Equal(got, plain) {
		t.Error("age.Decrypt content")
	}
	if !strings.Contains(string(enc[:600]), "-> "+MetaStanzaType+" v1 box=") {
		t.Errorf("no meta stanza in %q", enc[:300])
	}
}

func TestSniffTampering(t *testing.T) {
	box, _ := age.GenerateX25519Identity()
	plain := make([]byte, 3*64*1024+100)
	_, _ = rand.Read(plain)
	enc := encryptTo(t, plain, MetaRecipient{}, box.Recipient().String())
	hdrEnd := bytes.Index(enc, []byte("\n--- ")) + 1
	hdrEnd += bytes.IndexByte(enc[hdrEnd:], '\n') + 1
	payload := hdrEnd + 16 // the nonce
	mutations := map[string][]byte{
		"a byte flipped in chunk 3": func() []byte {
			m := bytes.Clone(enc)
			m[payload+2*(64*1024+16)+10] ^= 1
			return m
		}(),
		"the last byte removed":  enc[:len(enc)-1],
		"the last chunk removed": enc[:payload+3*(64*1024+16)],
		"a changed metadata stanza (header MAC)": func() []byte {
			m := bytes.Clone(enc)
			i := bytes.Index(m, []byte("-> X25519 "))
			m = append(m[:i], append([]byte("-> "+MetaStanzaType+" v1 rec=0000000000000000\n\n"), m[i:]...)...)
			return m
		}(),
	}
	for name, m := range mutations {
		h, r, err := Sniff(bytes.NewReader(m))
		if err != nil || !h.Encrypted {
			t.Errorf("%s: sniff %v", name, err)
			continue
		}
		out, err := Decrypt(r, box)
		if err == nil {
			_, err = io.ReadAll(out)
		}
		if !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestSniffOtherFiles(t *testing.T) {
	// a plain .sbk (a tar) passes through untouched
	tar := append([]byte("usr_local.tar.gz"), make([]byte, 600)...)
	h, r, err := Sniff(bytes.NewReader(tar))
	if err != nil || h.Encrypted {
		t.Fatalf("%v %+v", err, h)
	}
	if got, _ := io.ReadAll(r); !bytes.Equal(got, tar) {
		t.Error("plain content changed")
	}
	// a tiny file
	if h, r, err := Sniff(strings.NewReader("sbk")); err != nil || h.Encrypted {
		t.Errorf("%v %+v", err, h)
	} else if got, _ := io.ReadAll(r); string(got) != "sbk" {
		t.Errorf("%q", got)
	}
	// a file the age tool wrote: two recipients, no meta stanza
	a, _ := age.GenerateX25519Identity()
	b, _ := age.GenerateX25519Identity()
	var buf bytes.Buffer
	w, _ := age.Encrypt(&buf, a.Recipient(), b.Recipient())
	_, _ = w.Write([]byte("hello"))
	_ = w.Close()
	h, r, err = Sniff(bytes.NewReader(buf.Bytes()))
	if err != nil || !h.Encrypted || h.Stanzas != 2 || h.BoxFingerprint != "" || h.RecoveryFingerprint != "" {
		t.Errorf("%v %+v", err, h)
	}
	if out, err := Decrypt(r, b); err != nil {
		t.Error(err)
	} else if got, _ := io.ReadAll(out); string(got) != "hello" {
		t.Errorf("%q", got)
	}
	// armored
	var arm bytes.Buffer
	aw := armor.NewWriter(&arm)
	w, _ = age.Encrypt(aw, a.Recipient())
	_, _ = w.Write([]byte("armored"))
	_ = w.Close()
	_ = aw.Close()
	h, r, err = Sniff(bytes.NewReader(arm.Bytes()))
	if err != nil || !h.Encrypted || !h.Armored || h.Stanzas != 1 {
		t.Errorf("%v %+v", err, h)
	}
	if out, err := Decrypt(r, a); err != nil {
		t.Error(err)
	} else if got, _ := io.ReadAll(out); string(got) != "armored" {
		t.Errorf("%q", got)
	}
	// a passphrase file: recognised as such
	var sc bytes.Buffer
	sr, _ := age.NewScryptRecipient("passphrase")
	sr.SetWorkFactor(10)
	w, _ = age.Encrypt(&sc, sr)
	_, _ = w.Write([]byte("x"))
	_ = w.Close()
	h, _, err = Sniff(bytes.NewReader(sc.Bytes()))
	if err != nil || !h.Scrypt || h.Stanzas != 1 {
		t.Errorf("%v %+v", err, h)
	}
	// a header that never ends
	if _, _, err := Sniff(strings.NewReader(ageIntro + "\n-> X25519 abc\n")); !errors.Is(err, ErrCorrupt) {
		t.Errorf("unterminated header: %v", err)
	}
}

// A 1 GiB stream keeps the heap small: streaming, not buffering (skipped with -short).
func TestStreamingMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("1 GiB stream")
	}
	box, _ := age.GenerateX25519Identity()
	pr, pw := io.Pipe()
	go func() {
		w, _ := Encrypt(pw, MetaRecipient{}, box.Recipient().String())
		_, _ = io.Copy(w, io.LimitReader(zeroReader{}, 1<<30))
		_ = w.Close()
		_ = pw.Close()
	}()
	_, r, err := Sniff(pr)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Decrypt(r, box)
	if err != nil {
		t.Fatal(err)
	}
	n, err := io.Copy(io.Discard, out)
	if err != nil || n != 1<<30 {
		t.Fatalf("%d %v", n, err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

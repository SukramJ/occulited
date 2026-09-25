package backupcrypt

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"filippo.io/age"
	"filippo.io/age/armor"
)

// MetaStanzaType is the type of the header stanza that names, in the clear, which keys a file
// was encrypted to: "-> <MetaStanzaType> v1 box=<fingerprint> rec=<fingerprint>". age identities
// ignore stanzas they do not know (the spec's rule), so the file stays a plain age file for age -d.
// D-80: the name follows the public repository path and is fixed before the first release that
// writes encrypted backups; until then it is the module path.
const MetaStanzaType = "github.com/hobbyquaker/occulited/backup"

const (
	ageIntro     = "age-encryption.org/v1"
	armorBegin   = "-----BEGIN AGE ENCRYPTED FILE-----"
	maxHeaderLen = 64 * 1024 // an age header with two recipients is ~300 bytes; a bigger one is not ours
)

// MetaRecipient writes the metadata stanza. It is an age.Recipient that wraps nothing: the
// stanza has no body, and no identity ever unwraps it.
type MetaRecipient struct {
	BoxFingerprint      string
	RecoveryFingerprint string
}

// Wrap implements age.Recipient.
func (m MetaRecipient) Wrap([]byte) ([]*age.Stanza, error) {
	args := []string{"v1"}
	if m.BoxFingerprint != "" {
		args = append(args, "box="+strings.ReplaceAll(m.BoxFingerprint, "-", ""))
	}
	if m.RecoveryFingerprint != "" {
		args = append(args, "rec="+strings.ReplaceAll(m.RecoveryFingerprint, "-", ""))
	}
	return []*age.Stanza{{Type: MetaStanzaType, Args: args}}, nil
}

// Header is what the sniff of an upload tells before any key is used.
type Header struct {
	// Encrypted is false for anything that is not an age file: a plain .sbk goes on as today.
	Encrypted bool `json:"encrypted"`
	// Armored: the file was ASCII-armored (-----BEGIN AGE ENCRYPTED FILE-----); accepted.
	Armored bool `json:"armored,omitempty"`
	// The fingerprints from the metadata stanza, grouped; empty when the file has none (a file
	// written by the age tool, say).
	BoxFingerprint      string `json:"box_fingerprint,omitempty"`
	RecoveryFingerprint string `json:"recovery_fingerprint,omitempty"`
	// Stanzas counts the recipient stanzas; Scrypt says one of them is a passphrase stanza, which
	// this system cannot open.
	Stanzas int  `json:"stanzas"`
	Scrypt  bool `json:"scrypt,omitempty"`

	raw []byte // the header bytes as read, for the decryptor
}

// Sniff looks at the start of r. For an age file it reads the whole header (through the MAC line)
// and returns it with a reader that yields the file from its first byte again, so the caller can
// hand that reader to Decrypt. For anything else it returns Encrypted false and the same kind of
// reader. Nothing is consumed beyond the header.
func Sniff(r io.Reader) (Header, io.Reader, error) {
	br := bufio.NewReaderSize(r, 32*1024)
	peek, _ := br.Peek(len(armorBegin))
	var h Header
	switch {
	case bytes.HasPrefix(peek, []byte(ageIntro+"\n")):
	case bytes.HasPrefix(peek, []byte(armorBegin)):
		h.Armored = true
		// the armored file is decoded as a whole: age's own reader, and what it yields is the
		// binary file this function then parses like an unarmored one
		return sniffBinary(h, bufio.NewReaderSize(armor.NewReader(br), 32*1024))
	default:
		return h, br, nil
	}
	return sniffBinary(h, br)
}

func sniffBinary(h Header, br *bufio.Reader) (Header, io.Reader, error) {
	var raw bytes.Buffer
	line, err := readLine(br, &raw)
	if err != nil || line != ageIntro {
		return h, nil, fmt.Errorf("not an age file: %w", errors.Join(err, ErrCorrupt))
	}
	h.Encrypted = true
	for {
		line, err = readLine(br, &raw)
		if err != nil {
			return h, nil, fmt.Errorf("age header: %w", errors.Join(err, ErrCorrupt))
		}
		if raw.Len() > maxHeaderLen {
			return h, nil, fmt.Errorf("age header too long: %w", ErrCorrupt)
		}
		if strings.HasPrefix(line, "--- ") {
			break
		}
		if !strings.HasPrefix(line, "-> ") {
			continue // a stanza body line
		}
		f := strings.Fields(line[3:])
		if len(f) == 0 {
			return h, nil, fmt.Errorf("age header: empty stanza: %w", ErrCorrupt)
		}
		switch f[0] {
		case MetaStanzaType:
			for _, a := range f[1:] {
				if v, ok := strings.CutPrefix(a, "box="); ok {
					h.BoxFingerprint = regroup(v)
				} else if v, ok := strings.CutPrefix(a, "rec="); ok {
					h.RecoveryFingerprint = regroup(v)
				}
			}
		case "scrypt":
			h.Scrypt = true
			h.Stanzas++
		default:
			h.Stanzas++
		}
	}
	h.raw = raw.Bytes()
	return h, io.MultiReader(bytes.NewReader(h.raw), br), nil
}

// readLine reads one \n-terminated line, appends it to raw and returns it without the newline.
func readLine(br *bufio.Reader, raw *bytes.Buffer) (string, error) {
	s, err := br.ReadString('\n')
	if err != nil {
		return "", err
	}
	raw.WriteString(s)
	return strings.TrimSuffix(s, "\n"), nil
}

func regroup(hex string) string {
	if len(hex) != 16 {
		return hex
	}
	return hex[0:4] + "-" + hex[4:8] + "-" + hex[8:12] + "-" + hex[12:16]
}

// The errors the decryption tells apart; the API maps them to wrong-key and corrupt.
var (
	// ErrWrongKey: the header opened with none of the identities offered. Nothing of the payload
	// was read.
	ErrWrongKey = errors.New("this backup is not encrypted to that key")
	// ErrCorrupt: a chunk failed to authenticate, the file is truncated, or the header is not an
	// age header. The output so far is not a backup.
	ErrCorrupt = errors.New("the encrypted backup is damaged or was tampered with")
)

// Opens reports whether the header can be opened by id without reading any payload.
func (h Header) Opens(id age.Identity) bool {
	if !h.Encrypted || id == nil {
		return false
	}
	_, err := age.DecryptHeader(h.raw, id)
	return err == nil
}

// Decrypt returns the plaintext reader for the file r (from its first byte, as Sniff returned
// it) with the identities. ErrWrongKey when none matches; a later read error from the returned
// reader means ErrCorrupt (CorruptErr wraps it).
func Decrypt(r io.Reader, ids ...age.Identity) (io.Reader, error) {
	out, err := age.Decrypt(r, ids...)
	if err != nil {
		var nomatch *age.NoIdentityMatchError
		if errors.As(err, &nomatch) {
			return nil, fmt.Errorf("%w: %v", ErrWrongKey, err)
		}
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	return &corruptReader{r: out}, nil
}

// corruptReader marks every non-EOF read error as ErrCorrupt.
type corruptReader struct{ r io.Reader }

func (c *corruptReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		return n, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	return n, err
}

// Encrypt writes an encrypted stream to dst for the recipients (age1… strings) with the metadata
// stanza. The returned writer must be closed for the final chunk.
func Encrypt(dst io.Writer, meta MetaRecipient, recipients ...string) (io.WriteCloser, error) {
	rs := make([]age.Recipient, 0, len(recipients)+1)
	for _, s := range recipients {
		r, err := age.ParseX25519Recipient(s)
		if err != nil {
			return nil, fmt.Errorf("recipient: %w", err)
		}
		rs = append(rs, r)
	}
	rs = append(rs, meta)
	return age.Encrypt(dst, rs...)
}

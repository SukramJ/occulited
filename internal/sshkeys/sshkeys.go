// Package sshkeys reads the public keys of root's authorized_keys and keeps occulited's own section
// of that file (task 185). The Remote access page adds a pasted key into the section and removes
// it again; every other line of the file is someone else's and stays as it is.
//
// The section is marked by two comment lines:
//
//	# BEGIN openccu-lite: added on the Remote access page, removed there - edit outside this section
//	ssh-ed25519 AAAA… laptop
//	# END openccu-lite
//
// A key the page adds is a modern one without options (Parse): ssh-ed25519, ecdsa-sha2-nistp256/384/
// 521, the security-key forms sk-ssh-ed25519@openssh.com and sk-ecdsa-sha2-nistp256@openssh.com,
// and ssh-rsa of at least 3072 bits. The helper runs the same check before it writes as root.
package sshkeys

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// The section's comment lines.
const (
	Begin = "# BEGIN openccu-lite: added on the Remote access page, removed there - edit outside this section"
	End   = "# END openccu-lite"
)

// MinRSABits is the smallest RSA key the page takes.
const MinRSABits = 3072

// Key is one public key.
type Key struct {
	Type        string `json:"type"`
	Bits        int    `json:"bits,omitempty"`
	Comment     string `json:"comment,omitempty"`
	Fingerprint string `json:"fingerprint"` // SHA256:… as ssh-keygen -l prints it
	// Options is the options prefix of a line outside the section (from="…", command="…"); a key
	// of the section has none.
	Options string `json:"options,omitempty"`
	blob    string
}

// Line is the key as a line of authorized_keys: type, key and comment, without options.
func (k Key) Line() string {
	if k.Comment == "" {
		return k.Type + " " + k.blob
	}
	return k.Type + " " + k.blob + " " + k.Comment
}

var modern = map[string]bool{
	"ssh-ed25519":                        true,
	"ecdsa-sha2-nistp256":                true,
	"ecdsa-sha2-nistp384":                true,
	"ecdsa-sha2-nistp521":                true,
	"sk-ssh-ed25519@openssh.com":         true,
	"sk-ecdsa-sha2-nistp256@openssh.com": true,
	"ssh-rsa":                            true,
}

// known is every key type OpenSSH takes in authorized_keys, for reading lines outside the section.
var known = map[string]bool{
	"ssh-dss": true, "ssh-rsa-cert-v01@openssh.com": true, "ssh-ed25519-cert-v01@openssh.com": true,
	"ecdsa-sha2-nistp256-cert-v01@openssh.com": true, "ecdsa-sha2-nistp384-cert-v01@openssh.com": true,
	"ecdsa-sha2-nistp521-cert-v01@openssh.com": true, "sk-ssh-ed25519-cert-v01@openssh.com": true,
	"sk-ecdsa-sha2-nistp256-cert-v01@openssh.com": true,
}

// Parse reads one pasted key: a modern type, the key, and an optional comment - no options prefix,
// one key only. The comment becomes the key's name; line breaks inside the paste are an error.
func Parse(s string) (Key, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Key{}, errors.New("no key")
	}
	if strings.ContainsAny(s, "\r\n") {
		return Key{}, errors.New("one key per line: the paste holds a line break")
	}
	f := strings.Fields(s)
	if !modern[f[0]] {
		if known[f[0]] {
			return Key{}, fmt.Errorf("the key type %s is not taken here: ssh-ed25519, ecdsa-sha2-nistp256/384/521, the security-key forms or ssh-rsa of %d bits and more", f[0], MinRSABits)
		}
		return Key{}, errors.New("not a public key line: it starts with the key type (ssh-ed25519 …); an options prefix such as from= or command= is not taken here")
	}
	if len(f) < 2 {
		return Key{}, errors.New("the key itself is missing after its type")
	}
	k, err := decode(f[0], f[1])
	if err != nil {
		return Key{}, err
	}
	if k.Type == "ssh-rsa" && k.Bits < MinRSABits {
		return Key{}, fmt.Errorf("an RSA key of %d bits is too short: %d bits at least", k.Bits, MinRSABits)
	}
	k.Comment = strings.Join(f[2:], " ")
	return k, nil
}

// decode checks that the base64 key is one of type typ and reads its size and fingerprint.
func decode(typ, b64 string) (Key, error) {
	blob, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return Key{}, errors.New("the key is not valid base64")
	}
	r := reader{b: blob}
	inner := string(r.next())
	if r.err != nil || inner != typ {
		return Key{}, fmt.Errorf("the key's data is not a %s key", typ)
	}
	k := Key{Type: typ, blob: b64}
	switch {
	case typ == "ssh-ed25519" || typ == "sk-ssh-ed25519@openssh.com":
		if len(r.next()) != 32 || r.err != nil {
			return Key{}, errors.New("an Ed25519 key is 32 bytes")
		}
		k.Bits = 256
	case strings.Contains(typ, "nistp"):
		curve := string(r.next())
		point := r.next()
		if r.err != nil || !strings.Contains(typ, curve) || len(point) == 0 {
			return Key{}, errors.New("the ECDSA key does not match its curve")
		}
		k.Bits = map[string]int{"nistp256": 256, "nistp384": 384, "nistp521": 521}[curve]
	case typ == "ssh-rsa":
		_ = r.next() // e
		n := r.next()
		if r.err != nil || len(n) == 0 {
			return Key{}, errors.New("the RSA key's modulus is missing")
		}
		k.Bits = new(big.Int).SetBytes(n).BitLen()
	}
	sum := sha256.Sum256(blob)
	k.Fingerprint = "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
	return k, nil
}

// reader reads the length-prefixed strings of the SSH wire format.
type reader struct {
	b   []byte
	err error
}

func (r *reader) next() []byte {
	if r.err != nil || len(r.b) < 4 {
		r.err = errors.New("short")
		return nil
	}
	n := binary.BigEndian.Uint32(r.b)
	if uint64(n) > uint64(len(r.b)-4) {
		r.err = errors.New("short")
		return nil
	}
	v := r.b[4 : 4+n]
	r.b = r.b[4+n:]
	return v
}

// parseAny reads a line outside the section: options, type, key, comment - as sshd would. A line
// that holds no key it can read is skipped.
func parseAny(line string) (Key, bool) {
	f := fieldsQuoted(line)
	for i, w := range f {
		if modern[w] || known[w] {
			if i+1 >= len(f) {
				return Key{}, false
			}
			k, err := decode(w, f[i+1])
			if err != nil {
				return Key{}, false
			}
			k.Options = strings.Join(f[:i], " ")
			k.Comment = strings.Join(f[i+2:], " ")
			return k, true
		}
	}
	return Key{}, false
}

// fieldsQuoted splits at blanks outside double quotes: an options prefix such as
// command="echo a b" is one field.
func fieldsQuoted(s string) []string {
	var out []string
	var cur strings.Builder
	quoted := false
	for _, c := range s {
		switch {
		case c == '"':
			quoted = !quoted
			cur.WriteRune(c)
		case (c == ' ' || c == '\t') && !quoted:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(c)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// File is authorized_keys read: the section's keys and the keys outside it.
type File struct {
	Managed []Key `json:"managed"`
	Other   []Key `json:"other"`
}

// Read splits a file into its section's keys and the others. A line of the section that is not
// a key Parse takes is left out of Managed (and dropped at the next write of the section).
func Read(b []byte) File {
	out := File{Managed: []Key{}, Other: []Key{}}
	in := false
	for _, line := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == Begin:
			in = true
			continue
		case t == End:
			in = false
			continue
		case t == "" || strings.HasPrefix(t, "#"):
			continue
		}
		if in {
			if k, err := Parse(t); err == nil {
				out.Managed = append(out.Managed, k)
			}
		} else if k, ok := parseAny(t); ok {
			out.Other = append(out.Other, k)
		}
	}
	return out
}

// WithSection is the file with its section replaced by keys: every line outside the section as it
// was, the section at the end, none when keys is empty. A file without a final newline gets one.
func WithSection(b []byte, keys []Key) []byte {
	var kept []string
	in := false
	for _, line := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == Begin:
			in = true
			continue
		case t == End:
			in = false
			continue
		}
		if !in {
			kept = append(kept, line)
		}
	}
	// the split leaves one empty string after a final newline; blank lines at the end go
	for len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
		kept = kept[:len(kept)-1]
	}
	var buf bytes.Buffer
	for _, l := range kept {
		buf.WriteString(l)
		buf.WriteByte('\n')
	}
	if len(keys) > 0 {
		if buf.Len() > 0 {
			buf.WriteByte('\n')
		}
		buf.WriteString(Begin + "\n")
		for _, k := range keys {
			buf.WriteString(k.Line() + "\n")
		}
		buf.WriteString(End + "\n")
	}
	return buf.Bytes()
}

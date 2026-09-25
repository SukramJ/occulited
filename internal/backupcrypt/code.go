// Package backupcrypt encrypts backups (openccu-lite task 91, D-64, D-65, D-80): the firmware's
// .sbk, unchanged, inside one age v1 stream (filippo.io/age) with two X25519 recipients - the
// system's own identity, kept in a directory no backup contains, and the user's recovery key,
// which the system never stores. The recovery key is a short code (D-64: 28 Crockford base32
// symbols in seven groups, 128 bits plus a 10-bit checksum) from which the age identity is
// derived with HKDF-SHA256; an age identity string is accepted wherever the code is, since the
// emergency kit prints both.
//
// Nothing here touches the privilege boundary or the HTTP API: the package derives keys, reads
// and writes age headers and keeps the state files; internal/httpapi wires it to the routes.
package backupcrypt

import (
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"filippo.io/age"
)

// The recovery code: 26 data symbols carry 128 random bits (the top two bits of the 130 are
// zero), the last two symbols the first 10 bits of SHA-256 over the 16 bytes. Crockford's alphabet
// has no I, L, O or U; typing i, l or o is read as 1, 1 and 0 (Crockford's own rule).
const (
	codeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	codeDataLen  = 26
	codeCheckLen = 2
	codeLen      = codeDataLen + codeCheckLen
	codeGroup    = 4

	// The derivation of the age identity from the code: HKDF-SHA256 with a fixed salt and info,
	// the same on the system and in the browser (ui/src/lib/recoverykey.ts). Changing either
	// makes every existing recovery key useless - never change them.
	hkdfSalt = "openccu-lite backup recovery key"
	hkdfInfo = "age X25519 identity v1"
)

// The errors a typed secret can produce; the API turns them into its error codes.
var (
	// ErrTypo is a checksum failure: a character was mistyped, this is not a wrong key.
	ErrTypo = errors.New("the recovery key has a typo: its check symbols do not match")
	// ErrFormat is anything that is neither a recovery code nor an age identity.
	ErrFormat = errors.New("not a recovery key: 28 letters and digits in seven groups of four, or an age identity")
	// ErrIsRecipient is an age1… public key pasted where the secret was asked.
	ErrIsRecipient = errors.New("this is the public part (age1…); the recovery key is the code from the emergency kit or the line starting with AGE-SECRET-KEY-1")
	// ErrUnsupported is a post-quantum or plugin age identity.
	ErrUnsupported = errors.New("only classic X25519 age identities are supported, not post-quantum or plugin ones")
)

// Secret is a parsed recovery key: the code (empty when an age identity was given directly) and
// the age identity it stands for.
type Secret struct {
	code     []byte // the 16 raw bytes, nil for a raw identity
	identity *age.X25519Identity
}

// NewSecret makes a fresh random recovery key.
func NewSecret() (*Secret, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	return secretFromBytes(raw)
}

func secretFromBytes(raw []byte) (*Secret, error) {
	id, err := identityFromCode(raw)
	if err != nil {
		return nil, err
	}
	return &Secret{code: raw, identity: id}, nil
}

// identityFromCode is the derivation: HKDF-SHA256(code) -> 32 bytes -> the age identity.
func identityFromCode(raw []byte) (*age.X25519Identity, error) {
	seed, err := hkdf.Key(sha256.New, raw, []byte(hkdfSalt), hkdfInfo, 32)
	if err != nil {
		return nil, err
	}
	return age.ParseX25519Identity(IdentityString(seed))
}

// IdentityString encodes 32 secret bytes as an age identity (AGE-SECRET-KEY-1…).
func IdentityString(seed []byte) string {
	return strings.ToUpper(bech32Encode("age-secret-key-", seed))
}

// Code is the recovery code grouped for reading: XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX. Empty when
// the secret was given as an age identity.
func (s *Secret) Code() string {
	if s.code == nil {
		return ""
	}
	return groupCode(encodeCode(s.code))
}

// Identity is the age identity (AGE-SECRET-KEY-1…) the backups are decrypted with.
func (s *Secret) Identity() *age.X25519Identity { return s.identity }

// IdentityString is the identity as the emergency kit prints it, for age -d on a PC.
func (s *Secret) IdentityString() string { return s.identity.String() }

// Recipient is the public half (age1…), what the system stores and encrypts to.
func (s *Secret) Recipient() string { return s.identity.Recipient().String() }

// Fingerprint is the recipient's fingerprint.
func (s *Secret) Fingerprint() string { return Fingerprint(s.Recipient()) }

// ParseSecret reads what the user typed or pasted: the code in any grouping, case or spacing
// (i, l and o forgiven), or an age identity. The errors say what was wrong without saying "wrong
// key": a checksum failure is a typo.
func ParseSecret(s string) (*Secret, error) {
	trimmed := strings.TrimSpace(s)
	upper := strings.ToUpper(strings.Join(strings.Fields(trimmed), ""))
	switch {
	case strings.HasPrefix(upper, "AGE1"):
		return nil, ErrIsRecipient
	case strings.HasPrefix(upper, "AGE-SECRET-KEY-PQ-"), strings.HasPrefix(upper, "AGE-PLUGIN-"):
		return nil, ErrUnsupported
	case strings.HasPrefix(upper, "AGE-SECRET-KEY-1"):
		id, err := age.ParseX25519Identity(upper)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrTypo, err)
		}
		return &Secret{identity: id}, nil
	}
	raw, err := decodeCode(upper)
	if err != nil {
		return nil, err
	}
	return secretFromBytes(raw)
}

// encodeCode writes the 16 bytes as the 28 ungrouped symbols.
func encodeCode(raw []byte) string {
	sym := make([]byte, 0, codeLen)
	// 128 bits into 26 symbols: two zero bits in front
	var acc uint32
	bits := 2
	for _, b := range raw {
		acc = acc<<8 | uint32(b)
		bits += 8
		for bits >= 5 {
			bits -= 5
			sym = append(sym, codeAlphabet[(acc>>bits)&31])
		}
	}
	sum := sha256.Sum256(raw)
	check := uint16(sum[0])<<2 | uint16(sum[1])>>6 // the first 10 bits
	sym = append(sym, codeAlphabet[check>>5], codeAlphabet[check&31])
	return string(sym)
}

// decodeCode reads the upper-case symbols back into the 16 bytes; the grouping hyphens (and
// underscores, which a keyboard layout may produce for them) are dropped first.
func decodeCode(s string) ([]byte, error) {
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	if len(s) != codeLen {
		return nil, ErrFormat
	}
	vals := make([]byte, codeLen)
	for i := 0; i < codeLen; i++ {
		v, ok := codeValue(s[i])
		if !ok {
			return nil, ErrFormat
		}
		vals[i] = v
	}
	if vals[0]>>3 != 0 { // the two padding bits are zero: a first symbol above 7 is a typo
		return nil, ErrTypo
	}
	raw := make([]byte, 0, 16)
	var acc uint32
	bits := 0
	for i, v := range vals[:codeDataLen] {
		if i == 0 {
			acc, bits = uint32(v&7), 3
			continue
		}
		acc = acc<<5 | uint32(v)
		bits += 5
		for bits >= 8 {
			bits -= 8
			raw = append(raw, byte(acc>>bits))
		}
	}
	sum := sha256.Sum256(raw)
	check := uint16(sum[0])<<2 | uint16(sum[1])>>6
	if uint16(vals[codeDataLen])<<5|uint16(vals[codeDataLen+1]) != check {
		return nil, ErrTypo
	}
	return raw, nil
}

func codeValue(c byte) (byte, bool) {
	switch c {
	case 'I', 'L':
		return 1, true
	case 'O':
		return 0, true
	}
	i := strings.IndexByte(codeAlphabet, c)
	if i < 0 {
		return 0, false
	}
	return byte(i), true
}

func groupCode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i += codeGroup {
		if i > 0 {
			b.WriteByte('-')
		}
		b.WriteString(s[i:min(i+codeGroup, len(s))])
	}
	return b.String()
}

// Fingerprint identifies a recipient without revealing it: the first 8 bytes of SHA-256 over
// the recipient string, as four groups of four hex digits (cd22-595a-8524-3c56).
func Fingerprint(recipient string) string {
	sum := sha256.Sum256([]byte(recipient))
	h := fmt.Sprintf("%x", sum[:8])
	return h[0:4] + "-" + h[4:8] + "-" + h[8:12] + "-" + h[12:16]
}

// ParseRecipient accepts an age1… X25519 recipient (what the browser sends at setup) and returns
// its canonical string. Post-quantum (age1pq…) and plugin recipients are refused.
func ParseRecipient(s string) (string, error) {
	lower := strings.ToLower(strings.TrimSpace(s))
	// a classic recipient first: its bech32 data after "age1" is random, and 1 in 1024 begin with
	// "pq" - B-210, the setup test that failed now and then. A post-quantum one has its own
	// prefix, "age1pq1" ("1" is the separator, never a bech32 data character).
	r, err := age.ParseX25519Recipient(lower)
	if err != nil {
		if strings.HasPrefix(lower, "age1pq1") {
			return "", ErrUnsupported
		}
		if strings.HasPrefix(lower, "age1") && len(lower) > 62 {
			return "", ErrUnsupported // a plugin recipient: age1<name>1…
		}
		return "", fmt.Errorf("%w: %v", ErrFormat, err)
	}
	return r.String(), nil
}

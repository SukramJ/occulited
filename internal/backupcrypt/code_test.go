package backupcrypt

import (
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"filippo.io/age"
)

func TestCodeRoundTrip(t *testing.T) {
	for i := 0; i < 200; i++ {
		sec, err := NewSecret()
		if err != nil {
			t.Fatal(err)
		}
		code := sec.Code()
		if len(code) != 34 || strings.Count(code, "-") != 6 {
			t.Fatalf("code %q", code)
		}
		for _, c := range strings.ReplaceAll(code, "-", "") {
			if !strings.ContainsRune(codeAlphabet, c) {
				t.Fatalf("symbol %q outside the alphabet in %q", c, code)
			}
		}
		back, err := ParseSecret(code)
		if err != nil {
			t.Fatalf("%s: %v", code, err)
		}
		if back.Recipient() != sec.Recipient() || back.Code() != code || back.IdentityString() != sec.IdentityString() {
			t.Fatalf("round trip changed the key: %s -> %s", code, back.Code())
		}
	}
}

// The derivation is pinned: a changed salt, info or encoding would make every printed recovery
// key useless. The same vector is in ui/src/lib/recoverykey.test.ts.
func TestDerivationVector(t *testing.T) {
	raw := make([]byte, 16)
	for i := range raw {
		raw[i] = byte(i * 17)
	}
	sec, err := secretFromBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	const wantCode = "0024-H36H-2NCS-VRH6-DAQF-6DVV-QZN3"
	if sec.Code() != wantCode {
		t.Errorf("code %s", sec.Code())
	}
	const wantRecipient = "age14zwu3lsf95tuncdt5qzgp3a7jy7gjdsjmujl7d5n5tdv6qlpfu6qd7fvkk"
	const wantIdentity = "AGE-SECRET-KEY-173SCPSZRZXMZDYVU3QVPS6W8EF4LQWXGD4K06XZJJWMA0MQWA99S0V84MH"
	if sec.Recipient() != wantRecipient {
		t.Errorf("recipient %s", sec.Recipient())
	}
	if sec.IdentityString() != wantIdentity {
		t.Errorf("identity %s", sec.IdentityString())
	}
	if sec.Fingerprint() != Fingerprint(wantRecipient) || len(sec.Fingerprint()) != 19 {
		t.Errorf("fingerprint %s", sec.Fingerprint())
	}
	// the identity string opens what the recipient encrypts, in the age library's own hands
	if back, err := ParseSecret(wantIdentity); err != nil || back.Recipient() != wantRecipient || back.Code() != "" {
		t.Errorf("identity parse: %v %+v", err, back)
	}
}

func TestParseSecretForgivesTyping(t *testing.T) {
	sec, _ := NewSecret()
	code := sec.Code()
	flat := strings.ReplaceAll(code, "-", "")
	variants := []string{
		strings.ToLower(code),
		flat,
		"  " + code + "\n",
		strings.ReplaceAll(code, "-", " "),
		strings.ReplaceAll(code, "-", "_"),
		flat[:7] + "\n" + flat[7:],
		strings.NewReplacer("0", "o", "1", "l").Replace(code),
		strings.NewReplacer("0", "O", "1", "I").Replace(code),
	}
	for _, v := range variants {
		got, err := ParseSecret(v)
		if err != nil {
			t.Errorf("%q: %v", v, err)
			continue
		}
		if got.Code() != code {
			t.Errorf("%q -> %s, want %s", v, got.Code(), code)
		}
	}
}

func TestParseSecretErrors(t *testing.T) {
	sec, _ := NewSecret()
	code := sec.Code()
	// one symbol changed: a typo, never "wrong key"
	b := []byte(code)
	for i, c := range b {
		if c != '-' {
			if c == 'A' {
				b[i] = 'B'
			} else {
				b[i] = 'A'
			}
			break
		}
	}
	cases := []struct {
		in   string
		want error
	}{
		{string(b), ErrTypo},
		{code[:20], ErrFormat},
		{code + "A", ErrFormat},
		{strings.Replace(code, code[1:2], "U", 1), ErrFormat},
		{"", ErrFormat},
		{"hello world", ErrFormat},
		{sec.Recipient(), ErrIsRecipient},
		{"AGE-SECRET-KEY-PQ-1QQQQ", ErrUnsupported},
		{"AGE-PLUGIN-YUBIKEY-1QQQ", ErrUnsupported},
		{sec.IdentityString()[:len(sec.IdentityString())-1] + "Q", ErrTypo},
	}
	for _, c := range cases {
		_, err := ParseSecret(c.in)
		if !errors.Is(err, c.want) {
			t.Errorf("%q: got %v, want %v", c.in, err, c.want)
		}
	}
	// a valid code whose checksum symbols happen to collide is not a concern here; the
	// checksum's coverage is 10 bits, so 1023 of 1024 single-symbol typos are caught
	caught := 0
	flat := strings.ReplaceAll(code, "-", "")
	for i := 0; i < len(flat); i++ {
		for _, r := range codeAlphabet {
			if byte(r) == flat[i] {
				continue
			}
			mut := flat[:i] + string(r) + flat[i+1:]
			if _, err := ParseSecret(mut); err != nil {
				caught++
			}
		}
	}
	if caught < 26*31*99/100 {
		t.Errorf("only %d of %d single-symbol typos caught", caught, 28*31)
	}
}

func TestBech32MatchesAge(t *testing.T) {
	for i := 0; i < 50; i++ {
		seed := make([]byte, 32)
		_, _ = rand.Read(seed)
		s := IdentityString(seed)
		id, err := age.ParseX25519Identity(s)
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		if id.String() != s {
			t.Fatalf("age re-encodes %s as %s", s, id.String())
		}
	}
}

func TestParseRecipient(t *testing.T) {
	id, _ := age.GenerateX25519Identity()
	r := id.Recipient().String()
	if got, err := ParseRecipient(" " + strings.ToUpper(r) + "\n"); err != nil || got != r {
		t.Errorf("%v %s", err, got)
	}
	if _, err := ParseRecipient("age1pq1qqqq"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("pq: %v", err)
	}
	if _, err := ParseRecipient("age1yubikey1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("plugin: %v", err)
	}
	last := "q"
	if strings.HasSuffix(r, "q") {
		last = "p"
	}
	if _, err := ParseRecipient(r[:len(r)-1] + last); !errors.Is(err, ErrFormat) {
		t.Errorf("checksum: %v", err)
	}
	if _, err := ParseRecipient(id.String()); !errors.Is(err, ErrFormat) {
		t.Errorf("an identity is not a recipient: %v", err)
	}
}

func TestFingerprint(t *testing.T) {
	fp := Fingerprint("age14zwu3lsf95tuncdt5qzgp3a7jy7gjdsjmujl7d5n5tdv6qlpfu6qd7fvkk")
	if fp != "ea77-3877-2a7b-31f8" {
		t.Errorf("%s", fp)
	}
	if Fingerprint("a") == Fingerprint("b") {
		t.Error("collision")
	}
}

// B-210: a classic X25519 recipient is "age1" and random bech32 data, so 1 in 1024 of them begin
// with "age1pq" - it is accepted; a post-quantum one ("age1pq1…") is not.
func TestClassicRecipientBeginningWithPQ(t *testing.T) {
	for i := 0; i < 200000; i++ {
		id, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}
		r := id.Recipient().String()
		if !strings.HasPrefix(r, "age1pq") {
			continue
		}
		got, err := ParseRecipient(r)
		if err != nil || got != r {
			t.Fatalf("%s: %q %v", r, got, err)
		}
		if _, err := ParseRecipient("age1pq1" + r[7:]); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("a post-quantum prefix: %v", err)
		}
		return
	}
	t.Fatal("no classic recipient beginning with age1pq in 200000 tries")
}

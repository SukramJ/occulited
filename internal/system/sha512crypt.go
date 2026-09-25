package system

import (
	"crypto/rand"
	"crypto/sha512"
	"strings"
)

// SHA-512-crypt ($6$, Ulrich Drepper's "Unix crypt using SHA-256 and SHA-512", the default 5000
// rounds): the hash lighttpd's mod_authn_file checks through crypt(3) for classic RPC's pair (task
// 143). The standard library has no crypt; this is the specification's algorithm, checked against
// its own test vector and openssl passwd -6.

const cryptAlphabet = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// sha512Crypt is password hashed with salt (at most 16 characters are used) as "$6$<salt>$<hash>".
func sha512Crypt(password, salt string) string {
	if len(salt) > 16 {
		salt = salt[:16]
	}
	p, s := []byte(password), []byte(salt)

	b := sha512.New()
	b.Write(p)
	b.Write(s)
	b.Write(p)
	sumB := b.Sum(nil)

	a := sha512.New()
	a.Write(p)
	a.Write(s)
	for n := len(p); n > 0; n -= 64 {
		a.Write(sumB[:min(n, 64)])
	}
	for n := len(p); n > 0; n >>= 1 {
		if n&1 != 0 {
			a.Write(sumB)
		} else {
			a.Write(p)
		}
	}
	sumA := a.Sum(nil)

	dp := sha512.New()
	for range p {
		dp.Write(p)
	}
	sumDP := dp.Sum(nil)
	pSeq := make([]byte, 0, len(p))
	for n := len(p); n > 0; n -= 64 {
		pSeq = append(pSeq, sumDP[:min(n, 64)]...)
	}

	ds := sha512.New()
	for i := 0; i < 16+int(sumA[0]); i++ {
		ds.Write(s)
	}
	sumDS := ds.Sum(nil)
	sSeq := make([]byte, 0, len(s))
	for n := len(s); n > 0; n -= 64 {
		sSeq = append(sSeq, sumDS[:min(n, 64)]...)
	}

	c := sumA
	for i := 0; i < 5000; i++ {
		h := sha512.New()
		if i&1 != 0 {
			h.Write(pSeq)
		} else {
			h.Write(c)
		}
		if i%3 != 0 {
			h.Write(sSeq)
		}
		if i%7 != 0 {
			h.Write(pSeq)
		}
		if i&1 != 0 {
			h.Write(c)
		} else {
			h.Write(pSeq)
		}
		c = h.Sum(nil)
	}

	var out strings.Builder
	out.WriteString("$6$" + salt + "$")
	enc := func(b2, b1, b0 byte, n int) {
		w := uint(b2)<<16 | uint(b1)<<8 | uint(b0)
		for ; n > 0; n-- {
			out.WriteByte(cryptAlphabet[w&0x3f])
			w >>= 6
		}
	}
	order := [][3]int{
		{0, 21, 42}, {22, 43, 1}, {44, 2, 23}, {3, 24, 45}, {25, 46, 4}, {47, 5, 26}, {6, 27, 48},
		{28, 49, 7}, {50, 8, 29}, {9, 30, 51}, {31, 52, 10}, {53, 11, 32}, {12, 33, 54}, {34, 55, 13},
		{56, 14, 35}, {15, 36, 57}, {37, 58, 16}, {59, 17, 38}, {18, 39, 60}, {40, 61, 19}, {62, 20, 41},
	}
	for _, o := range order {
		enc(c[o[0]], c[o[1]], c[o[2]], 4)
	}
	enc(0, 0, c[63], 2)
	return out.String()
}

// newCryptSalt is 16 random characters of the crypt alphabet.
func newCryptSalt() string {
	return randomFrom(cryptAlphabet, 16)
}

// randomFrom is n characters drawn uniformly from alphabet (len(alphabet) <= 256).
func randomFrom(alphabet string, n int) string {
	out := make([]byte, 0, n)
	buf := make([]byte, 1)
	limit := 256 - 256%len(alphabet)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			panic(err)
		}
		if int(buf[0]) < limit {
			out = append(out, alphabet[int(buf[0])%len(alphabet)])
		}
	}
	return string(out)
}

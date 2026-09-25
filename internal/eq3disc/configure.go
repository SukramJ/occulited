package eq3disc

// Writing a device's network settings (`C`, openccu-lite task 220). A device with its network
// encryption on answers a `C` sent in clear with code 3 and 16 bytes of IV; the client then sends
// the same command wrapped: opcode `*`, and the payload AES-128-CBC encrypted with the key and
// that IV - 16 random bytes, the marker "eQ-3__UDP-Crypt" and its NUL, the real opcode, its data,
// random bytes up to a whole block. The key is the MD5 of the device's password: the `PW` on an
// access point's sticker, a LAN gateway's key. The documented scheme; our own implementation.

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"errors"
	"fmt"
	"net/netip"
)

// cryptMarker opens every encrypted payload.
const cryptMarker = "eQ-3__UDP-Crypt\x00"

// OpCrypt is the opcode of the encrypted wrapper.
const OpCrypt = '*'

// KeyOf is the AES key for a device password.
func KeyOf(password string) [16]byte { return md5.Sum([]byte(password)) }

// Seal wraps one command for a device that asked for encryption.
func Seal(key [16]byte, iv []byte, op byte, data []byte) ([]byte, error) {
	if len(iv) != aes.BlockSize {
		return nil, errors.New("the IV is not 16 bytes")
	}
	plain := make([]byte, 16, 16+len(cryptMarker)+1+len(data)+aes.BlockSize)
	if _, err := rand.Read(plain); err != nil {
		return nil, err
	}
	plain = append(plain, cryptMarker...)
	plain = append(plain, op)
	plain = append(plain, data...)
	if r := len(plain) % aes.BlockSize; r != 0 {
		pad := make([]byte, aes.BlockSize-r)
		if _, err := rand.Read(pad); err != nil {
			return nil, err
		}
		plain = append(plain, pad...)
	}
	blk, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(plain))
	cipher.NewCBCEncrypter(blk, iv).CryptBlocks(out, plain)
	return out, nil
}

// Open is Seal's reverse: the opcode and the data with the padding (the length is the command's).
// For the tests, and for reading a wrapped answer.
func Open(key [16]byte, iv, sealed []byte) (op byte, data []byte, err error) {
	if len(iv) != aes.BlockSize || len(sealed) < 3*aes.BlockSize || len(sealed)%aes.BlockSize != 0 {
		return 0, nil, errors.New("not a sealed payload")
	}
	blk, err := aes.NewCipher(key[:])
	if err != nil {
		return 0, nil, err
	}
	plain := make([]byte, len(sealed))
	cipher.NewCBCDecrypter(blk, iv).CryptBlocks(plain, sealed)
	if string(plain[16:16+len(cryptMarker)]) != cryptMarker {
		return 0, nil, ErrWrongPassword
	}
	rest := plain[16+len(cryptMarker):]
	return rest[0], rest[1:], nil
}

// ErrWrongPassword: the device asked for encryption again, or did not answer the wrapped command.
var ErrWrongPassword = errors.New("the device did not take the password")

// ErrNoAnswer: the device did not answer at all.
var ErrNoAnswer = errors.New("the device did not answer")

// ErrRefused: the device answered the command with an error.
var ErrRefused = errors.New("the device refused the settings")

// Configure writes the network settings of one device, addressed by its exact type and serial and
// sent to `to` (its address, or a broadcast for a device in another subnet). password is used when
// the device asks for encryption; a device with encryption off takes the command in clear. It
// returns whether the device said it restarts.
func (c *Client) Configure(ctx context.Context, to netip.Addr, typ, serial string, cfg Config, password string) (restarts bool, err error) {
	payload, err := SetConfigPayload(cfg)
	if err != nil {
		return false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	conn, err := c.listen()
	if err != nil {
		return false, err
	}
	defer conn.Close()
	sender := newSender()
	a, ok := c.ask(ctx, conn, []netip.Addr{to}, sender, 1, typ, serial, OpWrite, payload)
	if !ok {
		return false, ErrNoAnswer
	}
	if a.Code == CodeEncrypted {
		if password == "" {
			return false, ErrWrongPassword
		}
		if len(a.Data) < aes.BlockSize {
			return false, fmt.Errorf("the device asked for encryption without an IV")
		}
		sealed, err := Seal(KeyOf(password), a.Data[:aes.BlockSize], OpWrite, payload)
		if err != nil {
			return false, err
		}
		// one try: a wrong password is said, never retried
		got, err := c.exchange(ctx, conn, []netip.Addr{to}, BuildRequest(sender, 2, typ, serial, OpCrypt, sealed), sender, 2, 2*c.timeout(), func(x []Answer) bool { return len(x) > 0 })
		if err != nil {
			return false, err
		}
		a = Answer{}
		for _, x := range got {
			if x.Serial == serial {
				a = x
				break
			}
		}
		if a.Serial == "" || a.Code == CodeEncrypted {
			return false, ErrWrongPassword
		}
	}
	switch a.Code {
	case CodeOK:
		return false, nil
	case CodeOKRestart:
		return true, nil
	}
	return false, ErrRefused
}

// Lookup finds one device by its serial, wherever it is: an Identify for that serial to the
// broadcast targets (it reaches a device set to another subnet, too), then `n` and `c`.
func (c *Client) Lookup(ctx context.Context, serial string) (*Found, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	conn, err := c.listen()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	sender := newSender()
	answers, err := c.exchange(ctx, conn, c.targets(), BuildRequest(sender, 1, "*", serial, OpIdentify, nil), sender, 1, c.wait(), func(a []Answer) bool {
		for _, x := range a {
			if x.Serial == serial {
				return true
			}
		}
		return false
	})
	if err != nil {
		return nil, err
	}
	for _, a := range answers {
		if a.Serial != serial || a.Opcode != OpIdentify || a.Code != CodeOK {
			continue
		}
		v, svc := ParseIdentify(a.Data)
		f := &Found{Type: a.Type, Serial: a.Serial, Version: v, ProtocolVersion: int(a.Version), IP: a.From.String(), Services: svc}
		if n, ok := c.ask(ctx, conn, []netip.Addr{a.From}, sender, 2, a.Type, serial, OpCurrent, nil); ok && n.Code == CodeOK {
			if r, ok := readAddresses(n.Data); ok {
				f.Runtime = &r
			}
		}
		if cf, ok := c.ask(ctx, conn, []netip.Addr{a.From}, sender, 3, a.Type, serial, OpConfig, nil); ok && cf.Code == CodeOK {
			if x, ok := ParseConfig(cf.Data); ok {
				f.Config = &x
			}
		}
		return f, nil
	}
	return nil, ErrNoAnswer
}

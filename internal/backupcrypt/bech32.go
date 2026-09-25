package backupcrypt

// Bech32 (BIP-173), encoding only: age's identity and recipient strings are Bech32, and
// filippo.io/age keeps its encoder internal. The decoder is age's own (ParseX25519Identity), so
// a string made here is checked against it in the tests.

const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

var bech32Gen = [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}

func bech32Polymod(values []byte) uint32 {
	chk := uint32(1)
	for _, v := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i := 0; i < 5; i++ {
			if (top>>uint(i))&1 == 1 {
				chk ^= bech32Gen[i]
			}
		}
	}
	return chk
}

func bech32HRPExpand(hrp string) []byte {
	out := make([]byte, 0, 2*len(hrp)+1)
	for i := 0; i < len(hrp); i++ {
		out = append(out, hrp[i]>>5)
	}
	out = append(out, 0)
	for i := 0; i < len(hrp); i++ {
		out = append(out, hrp[i]&31)
	}
	return out
}

// bech32Encode writes data (8-bit bytes) under the lower-case hrp; the caller upper-cases the
// whole string for an identity, as age does.
func bech32Encode(hrp string, data []byte) string {
	// 8-bit groups into 5-bit ones, padded
	var five []byte
	var acc uint32
	bits := 0
	for _, b := range data {
		acc = acc<<8 | uint32(b)
		bits += 8
		for bits >= 5 {
			bits -= 5
			five = append(five, byte(acc>>bits)&31)
		}
	}
	if bits > 0 {
		five = append(five, byte(acc<<(5-bits))&31)
	}
	values := append(bech32HRPExpand(hrp), five...)
	polymod := bech32Polymod(append(values, 0, 0, 0, 0, 0, 0)) ^ 1
	out := []byte(hrp + "1")
	for _, v := range five {
		out = append(out, bech32Charset[v])
	}
	for i := 0; i < 6; i++ {
		out = append(out, bech32Charset[(polymod>>uint(5*(5-i)))&31])
	}
	return string(out)
}

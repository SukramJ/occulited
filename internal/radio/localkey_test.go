package radio

import (
	"strings"
	"testing"
)

// task 149: the key lines of hmip_user.conf - written beside the choices, every other line kept
func TestLocalKeyLines(t *testing.T) {
	conf := "occulite.hmip.adapter=5F298D97AF\n# a comment\nocculite.hmip.path=direct"
	if ReadLocalKey(conf).Enabled() {
		t.Fatal("no key")
	}
	on := SetLocalKey(conf, "00112233445566778899AABBCCDDEEFF", "FFEEDDCCBBAA99887766554433221100")
	k := ReadLocalKey(on)
	if !k.Enabled() || k.NetworkKey != "00112233445566778899AABBCCDDEEFF" || k.BackboneKey != "FFEEDDCCBBAA99887766554433221100" || k.KeyServerMode != KeyServerLocal {
		t.Fatalf("on: %+v\n%s", k, on)
	}
	for _, keep := range []string{"occulite.hmip.adapter=5F298D97AF\n", "# a comment\n", "occulite.hmip.path=direct\n"} {
		if !strings.Contains(on, keep) {
			t.Errorf("lost %q:\n%s", keep, on)
		}
	}
	// a known network key alone: no backbone key line
	if nk := SetLocalKey(conf, "00112233445566778899AABBCCDDEEFF", ""); ReadLocalKey(nk).BackboneKey != "" || strings.Contains(nk, BackboneKeyKey) {
		t.Fatalf("network key only:\n%s", nk)
	}
	// set twice: one line per key
	again := SetLocalKey(on, "0123456789ABCDEF0123456789ABCDEF", "0123456789ABCDEF0123456789ABCDEF")
	if strings.Count(again, NetworkKeyKey+"=") != 1 || strings.Count(again, KeyServerModeKey+"=") != 1 {
		t.Fatalf("twice:\n%s", again)
	}
	// a hand-written base key goes when full keys are set
	if strings.Contains(SetLocalKey("Network.Key.Base=00112233445566778899AABBCC\n", "0123456789ABCDEF0123456789ABCDEF", "0123456789ABCDEF0123456789ABCDEF"), "Base") {
		t.Fatal("base kept")
	}
	// the override flips the mode only
	if ov := SetKeyServerMode(on, KeyServerLocalFallback); ReadLocalKey(ov).KeyServerMode != KeyServerLocalFallback || ReadLocalKey(ov).NetworkKey == "" {
		t.Fatalf("override:\n%s", ov)
	}
	// the revert takes the snapshot's key lines - none here - and keeps a choice made since
	later := strings.Replace(on, "path=direct", "path=multimacd", 1)
	back := RestoreKeyLines(later, conf+"\n")
	if ReadLocalKey(back).Enabled() || ReadLocalKey(back).KeyServerMode != "" || !strings.Contains(back, "path=multimacd") {
		t.Fatalf("revert:\n%s", back)
	}
	// a snapshot with a hand-set key gets it back
	hand := "Network.Key=0123456789ABCDEF0123456789ABCDEF\n"
	if ReadLocalKey(RestoreKeyLines(on, hand)).NetworkKey != "0123456789ABCDEF0123456789ABCDEF" {
		t.Fatal("hand key not restored")
	}
}

// task 192: the pairing fact for a client - the three modes and an unconfigured system, each with
// and without device keys; a hand-edited line that is no key does not count
func TestPairingOf(t *testing.T) {
	const two = "3014F711A0000B3D1C89D8DF=00112233445566778899AABBCCDDEEFF\n3014F711A0000B3D1C89D8E0=FFEEDDCCBBAA99887766554433221100\nnot a key\n"
	for _, c := range []struct {
		name, conf, keys string
		want             Pairing
	}{
		{"unset, no map", "occulite.hmip.path=direct\n", "", Pairing{KeyServerMode: KeyServerLocalFallback, OfflinePairing: true}},
		{"unset, two keys", "", two, Pairing{KeyServerMode: KeyServerLocalFallback, DeviceKeys: 2, OfflinePairing: true}},
		{"KEYSERVER_LOCAL", "KeyServer.Mode=KEYSERVER_LOCAL\n", "# empty\n", Pairing{KeyServerMode: KeyServerLocalFallback, OfflinePairing: true}},
		{"KEYSERVER_LOCAL, keys", "KeyServer.Mode = KEYSERVER_LOCAL\n", two, Pairing{KeyServerMode: KeyServerLocalFallback, DeviceKeys: 2, OfflinePairing: true}},
		{"KEYSERVER", "KeyServer.Mode=KEYSERVER\n", "", Pairing{KeyServerMode: KeyServerKeyServerOnly, OfflinePairing: true}},
		{"KEYSERVER, keys", "KeyServer.Mode=KEYSERVER\n", two, Pairing{KeyServerMode: KeyServerKeyServerOnly, DeviceKeys: 2, OfflinePairing: true}},
		{"LOCAL", SetLocalKey("", "00112233445566778899AABBCCDDEEFF", ""), "", Pairing{KeyServerMode: KeyServerLocal}},
		{"LOCAL, keys", SetLocalKey("", "00112233445566778899AABBCCDDEEFF", ""), two, Pairing{KeyServerMode: KeyServerLocal, DeviceKeys: 2}},
	} {
		if got := PairingOf(c.conf, c.keys); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestNormalizeAndGenerateKey(t *testing.T) {
	if k, err := NormalizeKey(" 0011-2233 4455:6677 8899aabb ccddeeff "); err != nil || k != "00112233445566778899AABBCCDDEEFF" {
		t.Fatalf("%q %v", k, err)
	}
	for _, bad := range []string{"", "0011", "00112233445566778899AABBCCDDEEFG", "0102030405060708090A0B0C0D0E0F10", "00000000000000000000000000000000", "00112233445566778899AABBCCDDEEFF00"} {
		if _, err := NormalizeKey(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	a, err := GenerateKey()
	b, _ := GenerateKey()
	if err != nil || len(a) != 32 || a == b || strings.ToUpper(a) != a {
		t.Fatalf("%q %q %v", a, b, err)
	}
	if !ExchangeIDSet("adapter.address=0x1\naccesspoint.exchange.id = 5\n") || ExchangeIDSet("adapter.address=0x1\n") {
		t.Fatal("exchange id")
	}
}

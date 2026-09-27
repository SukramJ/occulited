package radio

import (
	"reflect"
	"strings"
	"testing"
)

func TestPrintedKeyToHex(t *testing.T) {
	// the printed KEY is the key as one base-32 number, the first character the highest digit
	// (a vector made with that reading, independently of the WebUI's byte-wise loop)
	if got := PrintedKeyToHex("014E2PG2EBSQQZXQ5TL1U58CHH"); got != "0123456789ABCDEFFEDCBA9876543210" {
		t.Fatal(got)
	}
	if got := PrintedKeyToHex(strings.Repeat("0", 26)); got != strings.Repeat("0", 32) {
		t.Fatal(got)
	}
	// 26 characters are 130 bits: the two above the key fall off, as in the WebUI
	if got := PrintedKeyToHex("Z" + strings.Repeat("0", 25)); got != "E0"+strings.Repeat("0", 30) {
		t.Fatal(got)
	}
	if got := PrintedKeyToHex(strings.Repeat("0", 25) + "Z"); got != strings.Repeat("0", 30)+"1F" {
		t.Fatal(got)
	}
}

func TestParseDeviceCodeAndKey(t *testing.T) {
	want := DeviceKey{SGTIN: "3014F711A0000E0000000A05", Key: "0123456789ABCDEFFEDCBA9876543210"}
	for _, code := range []string{
		"EQ01SG3014F711A0000E0000000A05DLK0123456789ABCDEFFEDCBA9876543210",
		"  eq01sg3014f711a0000e0000000a05dlk0123456789abcdeffedcba9876543210\n",
		"prefix EQ01SG3014F711A0000E0000000A05DLK0123456789ABCDEFFEDCBA9876543210 suffix",
	} {
		if got, err := ParseDeviceCode(code); err != nil || got != want {
			t.Fatalf("%q: %+v %v", code, got, err)
		}
	}
	if got := want.Payload(); got != "EQ01SG3014F711A0000E0000000A05DLK0123456789ABCDEFFEDCBA9876543210" {
		t.Fatal(got)
	}
	for _, bad := range []string{"", "WIFI:S:x;;", "EQ01SG3014F711A0000E0000000A05DLK0123"} {
		if _, err := ParseDeviceCode(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	// the sticker's forms: the SGTIN with dashes, the printed key in groups
	if got, err := ParseDeviceKey("3014-F711-A000-0E00-0000-0A05", "014E2-PG2EB-SQQZX-Q5TL1-U58CHH"); err != nil || got != want {
		t.Fatalf("printed: %+v %v", got, err)
	}
	if got, err := ParseDeviceKey("3014f711a0000e0000000a05", "0123456789abcdeffedcba9876543210"); err != nil || got != want {
		t.Fatalf("hex: %+v %v", got, err)
	}
	for _, c := range []struct{ sgtin, key, msg string }{
		{"3014-F711-A000-0E00-0000", "014E2PG2EBSQQZXQ5TL1U58CHH", "24 characters"},
		{"3014F711A0000E0000000A05", "014E2PG2EBSQQZXQ5TL1U58CHO", "no D, I, O or V"},
		{"3014F711A0000E0000000A05", "014E2PG2EBSQQZXQ5TL1U58CH", "not 25 characters"},
		{"3014F711A0000E0000000A05", strings.Repeat("0", 32), "all zeros"},
	} {
		if _, err := ParseDeviceKey(c.sgtin, c.key); err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Fatalf("%+v: %v", c, err)
		}
	}
}

func TestDeviceKeyMap(t *testing.T) {
	keys, bad := ParseDeviceKeyMap("# a comment\r\n! another\n3014F711A0000E0000000A05 = 0123456789abcdeffedcba9876543210\n" +
		"3014F711A00000000000000A:00000000000000000000000000000001\nnot a line\n3014F711A0000E0000000A05=0123456789ABCDEF0123456789ABCDEF\nx=y\n")
	want := []DeviceKey{
		{SGTIN: "3014F711A00000000000000A", Key: "00000000000000000000000000000001"},
		{SGTIN: "3014F711A0000E0000000A05", Key: "0123456789ABCDEF0123456789ABCDEF"},
	}
	if !reflect.DeepEqual(keys, want) || bad != 2 {
		t.Fatalf("%+v %d", keys, bad)
	}
	text := FormatDeviceKeyMap([]DeviceKey{want[1], want[0]})
	if again, bad := ParseDeviceKeyMap(text); !reflect.DeepEqual(again, want) || bad != 0 {
		t.Fatalf("round trip:\n%s", text)
	}
	if !strings.Contains(text, "\n3014F711A00000000000000A=00000000000000000000000000000001\n3014F711A0000E0000000A05=") {
		t.Fatalf("sorted:\n%s", text)
	}
}

func TestMappingFileLine(t *testing.T) {
	conf := "Network.Key=0123456789ABCDEF0123456789ABCDEF\nSGTIN.LocalKey.MappingFile=/old\n"
	got := SetMappingFile(conf, DeviceKeyMapFile)
	if MappingFile(got) != DeviceKeyMapFile || !strings.HasPrefix(got, "Network.Key=0123456789ABCDEF0123456789ABCDEF\n") || strings.Contains(got, "/old") {
		t.Fatalf("%q", got)
	}
	if MappingFile("") != "" || SetMappingFile("", "/x") != "SGTIN.LocalKey.MappingFile=/x\n" {
		t.Fatal("empty")
	}
	if !DeviceKeysRead("") || !DeviceKeysRead(KeyServerLocal) || DeviceKeysRead(KeyServerKeyServerOnly) {
		t.Fatal("modes")
	}
	if DeviceAddressOf("3014F711A0000E0000000A05") != "000E0000000A05" || DeviceAddressOf("x") != "" {
		t.Fatal("address")
	}
}

package system

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // the captures' text tables are in the lab's zone
)

// testdata/boots/list-boots-*.{json,txt} are captures of 2026-09-12 (fork 7006221d4, systemd 258.7):
// 119 is the x86_64 VM with its persistent journal (28 boots), charly the Pi 3 and rpi4 the Pi 4,
// both with the journal in RAM (one boot; the Pi 4's first entry is from before its clock was set).
func TestParseListBootsCaptures(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, first, last, newest string
		n                         int
	}{
		{"119", "2026-09-07T15:15:45+02:00", "2026-09-12T19:14:42+02:00", "1ababc97dfba43dababcc454714283ab", 28},
		{"charly", "2026-09-12T19:06:11+02:00", "2026-09-12T19:14:45+02:00", "6c97f67681ee40c78e69d3bc0c03efea", 1},
		{"rpi4", "2026-03-13T18:08:30+01:00", "2026-09-12T19:15:04+02:00", "648df1b0f925453bbf4e4cf221fc0545", 1},
	} {
		js, err := os.ReadFile("testdata/boots/list-boots-" + tc.name + ".json")
		if err != nil {
			t.Fatal(err)
		}
		txt, err := os.ReadFile("testdata/boots/list-boots-" + tc.name + ".txt")
		if err != nil {
			t.Fatal(err)
		}
		fromJSON, err := ParseListBoots(js, berlin)
		if err != nil || len(fromJSON) != tc.n {
			t.Fatalf("%s json: %v %d", tc.name, err, len(fromJSON))
		}
		fromText, err := ParseListBoots(txt, berlin)
		if err != nil || len(fromText) != tc.n {
			t.Fatalf("%s text: %v %d", tc.name, err, len(fromText))
		}
		// the two forms tell the same boots, to the second
		for i := range fromJSON {
			if fromJSON[i] != fromText[i] {
				t.Errorf("%s boot %d: json %+v, text %+v", tc.name, i, fromJSON[i], fromText[i])
			}
		}
		oldest, newest := fromJSON[0], fromJSON[tc.n-1]
		if oldest.Index != 1-tc.n || oldest.First != tc.first || newest.Index != 0 || newest.BootID != tc.newest || newest.Last != tc.last {
			t.Errorf("%s: oldest %+v newest %+v", tc.name, oldest, newest)
		}
	}
}

// The fixtures of `journalctl --list-boots` below are SYNTHETIC, for the shapes the captures above do
// not show: one JSON object per line, `idx`, numbers as strings, an id in UUID form, and the text
// table of systemd before 252 (no header, the two times joined by a dash).

const listBootsJSON = `[{"index":-2,"boot_id":"1e2b5b5c1d6f4b0e9a1c3f6e7d8a9b0c","first_entry":1757484723123456,"last_entry":1757533211654321},{"index":-1,"boot_id":"7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d","first_entry":1757533290000000,"last_entry":1757660400000000},{"index":0,"boot_id":"4c1d2a6b0e8f4a2b9c3d5e7f8a1b2c3d","first_entry":1757660460000000,"last_entry":1757692800000000}]`

const listBootsText = `IDX BOOT ID                          FIRST ENTRY                  LAST ENTRY
 -2 1e2b5b5c1d6f4b0e9a1c3f6e7d8a9b0c Wed 2025-09-10 08:12:03 CEST Wed 2025-09-10 21:40:11 CEST
 -1 7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d Wed 2025-09-10 21:41:30 CEST Fri 2025-09-12 09:00:00 CEST
  0 4c1d2a6b0e8f4a2b9c3d5e7f8a1b2c3d Fri 2025-09-12 09:01:00 CEST Fri 2025-09-12 18:00:00 CEST
`

const listBootsTextOld = `-1 7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d Wed 2025-09-10 21:41:30 CEST—Fri 2025-09-12 09:00:00 CEST
 0 4c1d2a6b0e8f4a2b9c3d5e7f8a1b2c3d Fri 2025-09-12 09:01:00 CEST—Fri 2025-09-12 18:00:00 CEST
`

func TestParseListBoots(t *testing.T) {
	cest := time.FixedZone("CEST", 2*3600)
	boots, err := ParseListBoots([]byte(listBootsJSON), cest)
	if err != nil || len(boots) != 3 {
		t.Fatalf("json: %v %+v", err, boots)
	}
	if b := boots[0]; b.Index != -2 || b.BootID != "1e2b5b5c1d6f4b0e9a1c3f6e7d8a9b0c" || b.First != "2025-09-10T08:12:03+02:00" || b.Last != "2025-09-10T21:40:11+02:00" {
		t.Errorf("json first: %+v", b)
	}
	if b := boots[2]; b.Index != 0 || b.BootID != "4c1d2a6b0e8f4a2b9c3d5e7f8a1b2c3d" {
		t.Errorf("json last: %+v", b)
	}

	// the table: the same boots, the times read in the box's zone
	text, err := ParseListBoots([]byte(listBootsText), cest)
	if err != nil || len(text) != 3 {
		t.Fatalf("text: %v %+v", err, text)
	}
	if b := text[0]; b.Index != -2 || b.BootID != boots[0].BootID || b.First != "2025-09-10T08:12:03+02:00" || b.Last != "2025-09-10T21:40:11+02:00" {
		t.Errorf("text first: %+v", b)
	}
	old, err := ParseListBoots([]byte(listBootsTextOld), cest)
	if err != nil || len(old) != 2 || old[0].Index != -1 || old[1].Last != "2025-09-12T18:00:00+02:00" {
		t.Errorf("old text: %v %+v", err, old)
	}

	// one object per line, idx for index, numbers as strings, an id in UUID form, a broken row
	lines := `{"idx":-1,"boot_id":"7A8B9C0D-1E2F-3A4B-5C6D-7E8F9A0B1C2D","first_entry":"1757533290000000","last_entry":"1757660400000000"}
{"idx":0,"boot_id":"not an id"}
{"idx":0,"boot_id":"4c1d2a6b0e8f4a2b9c3d5e7f8a1b2c3d","first_entry":1757660460000000}`
	nd, err := ParseListBoots([]byte(lines), cest)
	if err != nil || len(nd) != 2 || nd[0].Index != -1 || nd[0].BootID != "7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d" || nd[0].First == "" || nd[1].Last != "" {
		t.Errorf("lines: %v %+v", err, nd)
	}

	if empty, err := ParseListBoots([]byte("\n"), cest); err != nil || len(empty) != 0 || empty == nil {
		t.Errorf("empty: %v %#v", err, empty)
	}
	if _, err := ParseListBoots([]byte("[{"), cest); err == nil {
		t.Error("a broken array is an error")
	}
}

func TestJournalBoots(t *testing.T) {
	var calls []string
	j := JournalLog{Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return []byte(listBootsJSON), nil
	}}
	boots, err := j.Boots(context.Background())
	if err != nil || len(boots) != 3 || calls[0] != "journalctl --list-boots -o json --no-pager -q" {
		t.Errorf("%v %v %+v", err, calls, boots)
	}
}

func TestValidBoot(t *testing.T) {
	for s, want := range map[string]bool{
		"": true, "0": true, "-1": true, "-12": true, "-9999": true,
		"4c1d2a6b0e8f4a2b9c3d5e7f8a1b2c3d": true,
		"1":                                false, "-0": false, "-10000": false, "all": false, "--1": false,
		"4C1D2A6B0E8F4A2B9C3D5E7F8A1B2C3D":  false, // the routes lower-case it first
		"4c1d2a6b0e8f4a2b9c3d5e7f8a1b2c3":   false,
		"4c1d2a6b0e8f4a2b9c3d5e7f8a1b2c3d;": false,
		"-1 --since=today":                  false,
	} {
		if ValidBoot(s) != want {
			t.Errorf("ValidBoot(%q) = %v", s, !want)
		}
	}
	if got := NormalizeBoot(" 4C1D2A6B-0E8F-4A2B-9C3D-5E7F8A1B2C3D\n"); got != "4c1d2a6b0e8f4a2b9c3d5e7f8a1b2c3d" {
		t.Errorf("normalize: %q", got)
	}
	if got := NormalizeBoot("-1"); got != "-1" {
		t.Errorf("an offset stays: %q", got)
	}
}

func TestRootBootHelpers(t *testing.T) {
	r := rootWith(t, map[string]string{
		"proc/sys/kernel/random/boot_id": "4c1d2a6b-0e8f-4a2b-9c3d-5e7f8a1b2c3d\n",
		"proc/uptime":                    "88.61 150.20\n",
		"proc/self/mountinfo":            "25 1 179:2 / / ro,relatime - ext4 /dev/root ro\n40 25 179:3 /var/log/journal /var/log/journal rw,relatime - ext4 /dev/mmcblk0p3 rw\n",
	})
	if r.BootID() != "4c1d2a6b0e8f4a2b9c3d5e7f8a1b2c3d" {
		t.Errorf("boot id %q", r.BootID())
	}
	for b, want := range map[string]bool{"": true, "0": true, "4c1d2a6b0e8f4a2b9c3d5e7f8a1b2c3d": true, "-1": false, "1e2b5b5c1d6f4b0e9a1c3f6e7d8a9b0c": false} {
		if r.IsThisBoot(b) != want {
			t.Errorf("IsThisBoot(%q) = %v", b, !want)
		}
	}
	if up, ok := r.Uptime(); !ok || up != 88610*time.Millisecond {
		t.Errorf("uptime %v %v", up, ok)
	}
	if !r.JournalPersistent() {
		t.Error("the bind mount over /var/log/journal is the persistent journal")
	}

	bare := rootWith(t, map[string]string{"proc/self/mountinfo": "25 1 179:2 / / ro,relatime - ext4 /dev/root ro\n"})
	if bare.BootID() != "" || bare.IsThisBoot("4c1d2a6b0e8f4a2b9c3d5e7f8a1b2c3d") || !bare.IsThisBoot("0") || bare.JournalPersistent() {
		t.Error("no /proc boot id, no mount")
	}
	if _, ok := bare.Uptime(); ok {
		t.Error("no /proc/uptime")
	}
}

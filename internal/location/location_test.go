package location

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Location
		ok   bool
	}{
		{"userfs:etc/occulite/data", Location{Kind: KindUserfs, Folder: "etc/occulite/data"}, true},
		{"usb:LOGSTICK/journal", Location{Kind: KindUSB, Name: "LOGSTICK", Folder: "journal"}, true},
		{"usb:Ünï_2:x/a/b", Location{Kind: KindUSB, Name: "Ünï_2:x", Folder: "a/b"}, true},
		{"share:nas/openccu/journal", Location{Kind: KindShare, Name: "nas", Folder: "openccu/journal"}, true},
		{"share:nas", Location{Kind: KindShare, Name: "nas"}, true},
		{"share:nas/", Location{Kind: KindShare, Name: "nas"}, true},
		{"userfs:", Location{}, false},
		{"userfs:../etc", Location{}, false},
		{"userfs:/etc", Location{}, false},
		{"usb:LOG STICK/j", Location{}, false},
		{"usb:LOGSTICK", Location{}, false},
		{"usb:LOGSTICK/.hidden", Location{}, false},
		{"usb:LOGSTICK/a/b/c/d/e", Location{}, false},
		{"share:NAS/x", Location{}, false},
		{"share:nas/a//b", Location{}, false},
		{"/media/usb1/backup", Location{}, false},
		{"sftp:host/x", Location{}, false},
	} {
		got, err := Parse(tc.in)
		if tc.ok != (err == nil) || (tc.ok && got != tc.want) {
			t.Errorf("%q: %+v %v", tc.in, got, err)
		}
		if err != nil && !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", tc.in, err)
		}
		if tc.ok {
			back, err := Parse(got.String())
			if err != nil || back != got {
				t.Errorf("%q: round trip %q", tc.in, got.String())
			}
		}
	}
	if (Location{Kind: KindUSB, Name: "L", Folder: "x"}).ID() != "usb:L" || (Location{Kind: KindUserfs, Folder: "x"}).ID() != "userfs" {
		t.Fatal("ID")
	}
	if SharePath("nas", "") != "/media/net/nas" || SharePath("nas", "a/b") != "/media/net/nas/a/b" || UserfsPath("backup") != "/usr/local/backup" {
		t.Fatal("paths")
	}
}

func TestRules(t *testing.T) {
	for _, tc := range []struct {
		use, loc string
		ok       bool
	}{
		{UseJournal, "userfs:var/log/journal", true},
		{UseJournal, "userfs:var/log", false},
		{UseJournal, "usb:L/journal", true},
		{UseJournal, "share:nas/journal", true},
		{UseBackup, "userfs:backup", true},
		{UseBackup, "userfs:backup/ccu", true},
		{UseBackup, "userfs:etc", false},
		{UseBackup, "usb:L/backup", true},
		{UseBackup, "share:nas", true},
		{UseStore, "userfs:etc/occulite/data", true},
		{UseStore, "userfs:etc/occulite", true},
		{UseStore, "userfs:etc/occulitex", false},
		{UseStore, "usb:L/db", true}, // task 229: the snapshot
		{UseStore, "share:nas/db", false},
		{"other", "userfs:x", false},
	} {
		l, err := Parse(tc.loc)
		if err != nil {
			t.Fatal(err)
		}
		if err := Check(tc.use, l); (err == nil) != tc.ok {
			t.Errorf("%s %s: %v", tc.use, tc.loc, err)
		}
	}
	if r := RuleFor(UseStore, KindShare); r.Allowed || r.Code != "no-share-for-store" {
		t.Fatalf("%+v", r)
	}
}

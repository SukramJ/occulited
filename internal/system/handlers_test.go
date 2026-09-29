package system

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// The fixtures are verbatim from a CCU3 running all four interface processes, tabs and escapes
// included. The Java file's "\:" is what java.util.Properties writes and is the whole reason
// LegacyService needs its own parser.
const (
	rfdHandlers = "http://127.0.0.1:2048\tnr_Ab1Cd2_BidCos-RF\n" +
		"http://127.0.0.1:39292/bidcos\tBidCos-RF_java\n" +
		"http://198.51.100.9:2049\tmb_BidCos_RF\n" +
		"xmlrpc_bin://127.0.0.1:31999\t1007\n"

	legacyHandlers = "#This is the handler list for legacy backend\n" +
		"#Sun Sep 06 20:44:50 CEST 2026\n" +
		"18732=http\\://127.0.0.1\\:31999\n" +
		"HmIP-RF_java=http\\://127.0.0.1\\:39292/bidcos\n" +
		"mb_HmIP_RF=http\\://198.51.100.9\\:2049\n" +
		"nr_Ab1Cd2_HmIP-RF=http\\://127.0.0.1\\:2048\n"
)

func TestParsePlainHandlers(t *testing.T) {
	got := parsePlainHandlers(rfdHandlers)
	if len(got) != 4 {
		t.Fatalf("want 4 subscribers, got %d: %+v", len(got), got)
	}
	// sorted by id
	if got[0].ID != "1007" || got[0].URL != "xmlrpc_bin://127.0.0.1:31999" || !got[0].Local {
		t.Errorf("ReGa's binary callback: %+v", got[0])
	}
	if got[1].ID != "BidCos-RF_java" || got[1].URL != "http://127.0.0.1:39292/bidcos" {
		t.Errorf("the box's own Java server: %+v", got[1])
	}
	// the one that matters for D-29: a client somewhere else on the network
	var remote *InterfaceSubscriber
	for i := range got {
		if got[i].ID == "mb_BidCos_RF" {
			remote = &got[i]
		}
	}
	if remote == nil || remote.Local {
		t.Errorf("a callback to another host must not be Local: %+v", remote)
	}
}

func TestParseJavaHandlers(t *testing.T) {
	got := parseJavaHandlers(legacyHandlers)
	if len(got) != 4 {
		t.Fatalf("want 4, got %d: %+v", len(got), got)
	}
	for _, s := range got {
		// every escaped colon must be back, or the URL is unusable
		if got := s.URL; len(got) > 0 && (contains(got, `\:`) || contains(got, `\\`)) {
			t.Errorf("%s: escapes left in %q", s.ID, got)
		}
	}
	byID := map[string]InterfaceSubscriber{}
	for _, s := range got {
		byID[s.ID] = s
	}
	if u := byID["HmIP-RF_java"].URL; u != "http://127.0.0.1:39292/bidcos" {
		t.Errorf("HmIP-RF_java: %q", u)
	}
	if !byID["18732"].Local {
		t.Errorf("18732 should be local: %+v", byID["18732"])
	}
	if byID["mb_HmIP_RF"].Local {
		t.Errorf("mb_HmIP_RF is on another host and must not be Local: %+v", byID["mb_HmIP_RF"])
	}
}

// B-80: rfd kept Homematic Manager's old registration beside the new one, and the page keyed its
// rows by the id alone. Id and callback together are unique in the answer.
func TestSubscribersRegisteredTwice(t *testing.T) {
	tests := []struct {
		name  string
		plain string
		java  string
		want  []string // id + " " + url, in order
	}{
		{
			name:  "the same id with two callbacks is two subscribers",
			plain: "xmlrpc://127.0.0.1:2141\thmm_VirtualDevices\nxmlrpc://127.0.0.1:2140\thmm_VirtualDevices\n",
			want:  []string{"hmm_VirtualDevices xmlrpc://127.0.0.1:2140", "hmm_VirtualDevices xmlrpc://127.0.0.1:2141"},
		},
		{
			name:  "a line that is there twice is one subscriber",
			plain: "xmlrpc://127.0.0.1:2140\thmm_VirtualDevices\nhttp://127.0.0.1:2048\tnr_x\nxmlrpc://127.0.0.1:2140\thmm_VirtualDevices\n",
			want:  []string{"hmm_VirtualDevices xmlrpc://127.0.0.1:2140", "nr_x http://127.0.0.1:2048"},
		},
		{
			name: "the Java file alike",
			java: "hmm_HmIP-RF=http\\://127.0.0.1\\:2140\nhmm_HmIP-RF=http\\://127.0.0.1\\:2140\nhmm_HmIP-RF=http\\://127.0.0.1\\:2139\n",
			want: []string{"hmm_HmIP-RF http://127.0.0.1:2139", "hmm_HmIP-RF http://127.0.0.1:2140"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parsePlainHandlers(tt.plain)
			if tt.java != "" {
				got = parseJavaHandlers(tt.java)
			}
			var ids []string
			for _, s := range got {
				ids = append(ids, s.ID+" "+s.URL)
			}
			if len(ids) != len(tt.want) {
				t.Fatalf("got %q, want %q", ids, tt.want)
			}
			for i := range ids {
				if ids[i] != tt.want[i] {
					t.Errorf("row %d: got %q, want %q", i, ids[i], tt.want[i])
				}
			}
		})
	}
}

func TestInterfaceSubscribersReadsTheRightFile(t *testing.T) {
	r := rootWith(t, map[string]string{
		"var/RFD.handlers":           rfdHandlers,
		"var/LegacyService.handlers": legacyHandlers,
		"var/HMSERVER.handlers":      "", // running, nobody subscribed
	})
	if n := len(r.InterfaceSubscribers("BidCos-RF")); n != 4 {
		t.Errorf("BidCos-RF: %d", n)
	}
	if n := len(r.InterfaceSubscribers("HmIP-RF")); n != 4 {
		t.Errorf("HmIP-RF: %d", n)
	}
	// an empty file is a running daemon with no clients
	if got := r.InterfaceSubscribers("VirtualDevices"); got == nil || len(got) != 0 {
		t.Errorf("VirtualDevices: %+v (want empty, not nil)", got)
	}
	// no file at all: the daemon is not running, which is normal on a box with no wired bus
	if got := r.InterfaceSubscribers("HM-Wired"); got == nil || len(got) != 0 {
		t.Errorf("HM-Wired: %+v (want empty, not nil)", got)
	}
	// an interface nobody maps
	if got := r.InterfaceSubscribers("Nonsense"); got == nil || len(got) != 0 {
		t.Errorf("unknown: %+v", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// task 76: the same id with two callbacks marks both as a duplicate registration; an id that is
// there once, or a line that is there twice with one callback (one subscriber), is not marked.
func TestDuplicateHint(t *testing.T) {
	tests := []struct {
		name  string
		plain string
		want  map[string]bool // id + " " + url -> duplicate
	}{
		{"two callbacks", "xmlrpc://127.0.0.1:2141\thmm_VirtualDevices\nxmlrpc://127.0.0.1:2140\thmm_VirtualDevices\nhttp://127.0.0.1:2048\tnr_x\n",
			map[string]bool{"hmm_VirtualDevices xmlrpc://127.0.0.1:2140": true, "hmm_VirtualDevices xmlrpc://127.0.0.1:2141": true, "nr_x http://127.0.0.1:2048": false}},
		{"one line twice", "xmlrpc://127.0.0.1:2140\thmm_VirtualDevices\nxmlrpc://127.0.0.1:2140\thmm_VirtualDevices\n",
			map[string]bool{"hmm_VirtualDevices xmlrpc://127.0.0.1:2140": false}},
		{"different ids", "http://127.0.0.1:1\ta\nhttp://127.0.0.1:1\tb\n",
			map[string]bool{"a http://127.0.0.1:1": false, "b http://127.0.0.1:1": false}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parsePlainHandlers(tt.plain)
			if len(got) != len(tt.want) {
				t.Fatalf("got %+v, want %v", got, tt.want)
			}
			for _, s := range got {
				if want, ok := tt.want[s.ID+" "+s.URL]; !ok || s.Duplicate != want {
					t.Errorf("%s %s: duplicate %v, want %v", s.ID, s.URL, s.Duplicate, want)
				}
			}
		})
	}
}

// B-270: the daemon took our registration when its handlers file lists exactly it and was written
// since the init began; an older file is a previous run's entry.
func TestHoldsRegistration(t *testing.T) {
	const cb = "http://127.0.0.1:8184/cb/VirtualDevices"
	r := rootWith(t, map[string]string{
		"var/HMSERVER.handlers":      "http://127.0.0.1:2048\tnr_x_VirtualDevices\n" + cb + "\tocculited_VirtualDevices\n",
		"var/LegacyService.handlers": "occulited_HmIP-RF=http\\://127.0.0.1\\:8184/cb/HmIP-RF\n",
	})
	now := time.Now()
	if !r.HoldsRegistration("VirtualDevices", "occulited_VirtualDevices", cb, now.Add(-time.Minute)) {
		t.Error("the entry, written since: not taken")
	}
	if !r.HoldsRegistration("HmIP-RF", "occulited_HmIP-RF", "http://127.0.0.1:8184/cb/HmIP-RF", now.Add(-time.Minute)) {
		t.Error("the Java file's entry: not taken")
	}
	for _, c := range []struct{ name, id, url string }{
		{"VirtualDevices", "occulited_Other", cb},                // another id
		{"VirtualDevices", "occulited_VirtualDevices", cb + "x"}, // another callback
		{"BidCos-RF", "occulited_VirtualDevices", cb},            // no file
		{"Nonsense", "occulited_VirtualDevices", cb},             // no interface
	} {
		if r.HoldsRegistration(c.name, c.id, c.url, now.Add(-time.Minute)) {
			t.Errorf("%+v: taken", c)
		}
	}
	// a file older than the init began
	old := now.Add(-time.Hour)
	if err := os.Chtimes(r.Path("/var/HMSERVER.handlers"), old, old); err != nil {
		t.Fatal(err)
	}
	if r.HoldsRegistration("VirtualDevices", "occulited_VirtualDevices", cb, now.Add(-time.Minute)) {
		t.Error("a stale file counted")
	}
}

// B-228: the interface processes' ports - the list's, and the classic ones with their backends.
func TestInterfacePorts(t *testing.T) {
	r := rootWith(t, map[string]string{
		"etc/config/InterfacesList.xml": `<interfaces><ipc><name>BidCos-RF</name><url>xmlrpc_bin://127.0.0.1:32001</url></ipc>` +
			`<ipc><name>VirtualDevices</name><url>xmlrpc://127.0.0.1:39292/groups</url></ipc>` +
			`<ipc><name>CCU-Jack</name><url>xmlrpc://127.0.0.1:2121/RPC3</url></ipc></interfaces>`,
	})
	got := fmt.Sprint(r.InterfacePorts())
	if got != "[2000 2001 2010 2121 9292 32000 32001 32010 39292 42000 42001 42010 49292]" {
		t.Fatalf("ports %s", got)
	}
}

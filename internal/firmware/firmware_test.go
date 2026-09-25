package firmware

import (
	"compress/gzip"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseIndexJSONP(t *testing.T) {
	body := []byte(`homematic.com.setDeviceFirmwareVersions([{"type":"HmIP-BBL","version":"1.10.16"},{"type":"HM-CC-TC","version":"2.6.0"},{"nope":1}]);`)
	idx, err := ParseIndex(body)
	if err != nil || len(idx) != 2 || idx[0].Type != "HM-CC-TC" || idx[1].Version != "1.10.16" {
		t.Fatalf("%v %+v", err, idx)
	}
	if _, err := ParseIndex([]byte(`<html>maintenance</html>`)); err == nil {
		t.Fatal("html must not parse")
	}
}

func TestCompareVersions(t *testing.T) {
	cases := [][3]string{{"1.10.16", "1.9.0", "1"}, {"V1_10_16", "1.10.16", "0"}, {"2.6.0", "2.6.0000", "0"}, {"1.4", "1.4.1", "-1"}, {"", "1.0", "-1"}}
	for _, c := range cases {
		want := map[string]int{"-1": -1, "0": 0, "1": 1}[c[2]]
		if got := CompareVersions(c[0], c[1]); got != want {
			t.Fatalf("%q vs %q: want %d got %d", c[0], c[1], want, got)
		}
	}
}

func TestPlanScopesToPairedAndNewer(t *testing.T) {
	idx := []Entry{{Type: "HmIP-BBL", Version: "1.10.16"}, {Type: "HM-CC-TC", Version: "2.6.0"}, {Type: "HmIP-PDT", Version: "1.0.4"}}
	devs := []Device{{Type: "HmIP-BBL", Firmware: "1.10.6"}, {Type: "HmIP-BBL", Firmware: "1.10.16"}, {Type: "HmIP-PDT", Firmware: "1.0.4"}}
	plan := Plan(idx, devs)
	if len(plan) != 1 || plan[0].Type != "HmIP-BBL" {
		t.Fatalf("plan: %+v (BBL has one device on 1.10.6; PDT is current; TC not paired)", plan)
	}
}

func TestDownloadAndPrune(t *testing.T) {
	var gz []byte
	{
		f, _ := os.CreateTemp(t.TempDir(), "x")
		w := gzip.NewWriter(f)
		blob := make([]byte, 8192)
		_, _ = rand.Read(blob) // incompressible, so the archive stays above the size check
		_, _ = w.Write(blob)
		_ = w.Close()
		_ = f.Close()
		gz, _ = os.ReadFile(f.Name())
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/firmware/api/firmware/search/DEVICE":
			_, _ = w.Write([]byte(`homematic.com.setDeviceFirmwareVersions([{"type":"HmIP-BBL","version":"1.10.16"}])`))
		case r.URL.Path == "/firmware/download" && r.URL.Query().Get("product") == "HmIP-BBL":
			w.Header().Set("Content-Disposition", `attachment; filename=HmIP-BBL_update_V1_10_16_230616.tgz`)
			_, _ = w.Write(gz)
		case r.URL.Path == "/firmware/download" && r.URL.Query().Get("product") == "HM-BAD":
			w.Header().Set("Content-Disposition", `attachment; filename=HM-BAD_update_V1.tgz`)
			_, _ = w.Write([]byte("not gzip at all, and long enough to pass the size check ......................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................................."))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL)
	idx, err := c.Index(t.Context())
	if err != nil || len(idx) != 1 {
		t.Fatalf("index: %v %+v", err, idx)
	}
	dir := t.TempDir()
	name, n, err := c.Download(t.Context(), "HmIP-BBL", dir)
	if err != nil || name != "HmIP-BBL_update_V1_10_16_230616.tgz" || n < 1024 {
		t.Fatalf("download: %v %q %d", err, name, n)
	}
	if _, _, err := c.Download(t.Context(), "HM-BAD", dir); err == nil {
		t.Fatal("a non-gzip body must be refused")
	}
	if _, _, err := c.Download(t.Context(), "../x", dir); err == nil {
		t.Fatal("path-like type must be refused")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("no partial files may linger: %v", entries)
	}
	// prune: an older BBL file and a file for an unpaired type go, the newest paired stays
	_ = os.WriteFile(filepath.Join(dir, "HmIP-BBL_update_V1_10_6_220704.tgz"), gz, 0o644)
	_ = os.WriteFile(filepath.Join(dir, "HM-CC-TC_update_V2_6_0000_150812.tgz"), gz, 0o644)
	_ = os.WriteFile(filepath.Join(dir, "HMIP-HAP_3_0_36_241218.tgz"), gz, 0o644)
	removed, err := Prune(dir, []string{"HmIP-BBL", "HMIP-HAP"})
	if err != nil || len(removed) != 2 || removed[0] != "HM-CC-TC_update_V2_6_0000_150812.tgz" || removed[1] != "HmIP-BBL_update_V1_10_6_220704.tgz" {
		t.Fatalf("prune: %v %v", err, removed)
	}
	if TypeOfFile("HMIP-HAP_3_0_36_241218.tgz") != "HMIP-HAP" || TypeOfFile("HmIP-BBL_update_V1_10_16_230616.tgz") != "HmIP-BBL" {
		t.Fatal("type of file")
	}
}

// B-195: the WebUI's matching - case, the last underscore, HAP-JS1, HAP-B1 - and a doubled entry.
func TestPlanMatchesLikeTheWebUI(t *testing.T) {
	idx := []Entry{
		{Type: "HmIP-WRC2", Version: "1.18.2"},
		{Type: "HmIP-WRC2-2", Version: "2.8.8"},
		{Type: "HM-LC-Dim1TPBU-FM", Version: "2.9.0"},
		{Type: "HM-LC-Dim1TPBU-FM", Version: "2.10.0"},
		{Type: "HM-Sen-Foo_Bar", Version: "1.2"},
		{Type: "HmIP-HAP-JS1", Version: "3.0.36"},
		{Type: "HmIP-HAP", Version: "3.0.40"},
		{Type: "HmIP-PDT", Version: "2.2.4"},
	}
	cases := []struct {
		name string
		devs []Device
		want []string // "type version" of the plan, the index's spelling
	}{
		{"upper case HMIP-WRC2 gets HmIP-WRC2", []Device{{Type: "HMIP-WRC2", Firmware: "1.0.3"}}, []string{"HmIP-WRC2 1.18.2"}},
		{"the -2 revision is a type of its own", []Device{{Type: "HmIP-WRC2", Firmware: "1.18.2"}}, nil},
		{"a doubled index entry: the higher version", []Device{{Type: "HM-LC-Dim1TPBU-FM", Firmware: "2.9"}}, []string{"HM-LC-Dim1TPBU-FM 2.10.0"}},
		{"the last underscore is a space", []Device{{Type: "hm-sen-foo bar", Firmware: "1.1"}}, []string{"HM-Sen-Foo_Bar 1.2"}},
		{"HmIP-HAP JS1 is HmIP-HAP-JS1", []Device{{Type: "HmIP-HAP JS1", Firmware: "3.0.30"}}, []string{"HmIP-HAP-JS1 3.0.36"}},
		{"HmIP-HAP-B1 takes HmIP-HAP", []Device{{Type: "HmIP-HAP-B1", Firmware: "3.0.36"}}, []string{"HmIP-HAP 3.0.40"}},
		{"the oldest device of a type counts, whatever its spelling", []Device{{Type: "HmIP-WRC2", Firmware: "1.18.2"}, {Type: "HMIP-WRC2", Firmware: "1.0.3"}}, []string{"HmIP-WRC2 1.18.2"}},
		{"current and unlisted types plan nothing", []Device{{Type: "HmIP-PDT", Firmware: "2.2.4"}, {Type: "HM-CC-TC", Firmware: "2.1"}}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for _, e := range Plan(idx, c.devs) {
				got = append(got, e.Type+" "+e.Version)
			}
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Fatalf("plan %v, want %v", got, c.want)
			}
		})
	}
	if _, ok := ByKey(idx).Match("HM-CC-TC"); ok {
		t.Fatal("HM-CC-TC is not listed")
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		index, fw string
		want      bool
	}{
		{"1.18.2", "1.0.3", true},
		{"1.18.2", "1.18.2", false},
		{"2.1.4", "2.1", false}, // BidCos: major.minor only, as the WebUI compares
		{"2.2.0", "2.1", true},
		{"1.4", "1.4.1", false},
	}
	for _, c := range cases {
		if got := Newer(c.index, c.fw); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.index, c.fw, got)
		}
	}
}

func TestIndexAsksWithProductAndVersion(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = w.Write([]byte(`homematic.com.setDeviceFirmwareVersions([])`))
	}))
	defer srv.Close()
	c := New(srv.URL)
	if _, err := c.Index(t.Context()); err != nil || query != "" {
		t.Fatalf("bare: %v %q", err, query)
	}
	c.SystemVersion = "3.89.9.20260914"
	if _, err := c.Index(t.Context()); err != nil || query != "product=HM-CCU3&version=3.89.9.20260914" {
		t.Fatalf("with version: %v %q", err, query)
	}
}

func TestDownloadAsksWithTheIndexSpelling(t *testing.T) {
	var product string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		product = r.URL.Query().Get("product")
		http.NotFound(w, r)
	}))
	defer srv.Close()
	_, _, _ = New(srv.URL).Download(t.Context(), "HmIP-HAP JS1", t.TempDir())
	if product != "HmIP-HAP JS1" {
		t.Fatalf("product %q", product)
	}
	if _, _, err := New(srv.URL).Download(t.Context(), " x", t.TempDir()); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("a leading space: %v", err)
	}
}

package sysupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/outboundpin"
)

// What the release check and the download send (task 266, the fork's docs/privacy.md): the feed URL
// with a GitHub Accept header and the cached ETag, then the checksum file and the asset - no
// version, serial or address of the system in any request; Go's own User-Agent. A change here is
// a change to the privacy statement.
func TestOutboundRequestsArePinned(t *testing.T) {
	body := releaseZip()
	sum := sha256.Sum256(body)
	var srv *httptest.Server
	h, log := outboundpin.Recorder(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/releases":
			if r.Header.Get("If-None-Match") == `"v1"` {
				w.WriteHeader(304)
				return
			}
			w.Header().Set("ETag", `"v1"`)
			fmt.Fprintf(w, `[{"tag_name":"1.0.1","published_at":"2026-10-01T10:00:00Z","assets":[
{"name":"openccu-lite-x86_64-ova-1.0.1.zip","browser_download_url":"%s/asset","size":%d},
{"name":"openccu-lite-x86_64-ova-1.0.1.zip.sha256","browser_download_url":"%s/asset.sha256","size":100}]}]`, srv.URL, len(body), srv.URL)
		case "/asset":
			_, _ = w.Write(body)
		case "/asset.sha256":
			fmt.Fprintf(w, "%s  openccu-lite-x86_64-ova-1.0.1.zip\n", hex.EncodeToString(sum[:]))
		}
	}))
	srv = httptest.NewServer(h)
	t.Cleanup(srv.Close)
	s := New(fakeRoot(t, "VERSION=3.89.8.20260906\nPRODUCT=ova\nPLATFORM=ova\nVARIANT=lite\nLITE=1.0.0\n"), srv.URL+"/repos/o/r/releases", true, nil)
	for i := 0; i < 2; i++ {
		if err := s.Check(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Download(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"GET /repos/o/r/releases\nAccept: application/vnd.github+json\nAccept-Encoding: gzip\nUser-Agent: Go-http-client/1.1",
		"GET /repos/o/r/releases\nAccept: application/vnd.github+json\nAccept-Encoding: gzip\nIf-None-Match: \"v1\"\nUser-Agent: Go-http-client/1.1",
		"GET /asset.sha256\nAccept-Encoding: gzip\nUser-Agent: Go-http-client/1.1",
		"GET /asset\nAccept-Encoding: gzip\nUser-Agent: Go-http-client/1.1",
	}
	if got := log.Requests(); strings.Join(got, "\n\n") != strings.Join(want, "\n\n") {
		t.Errorf("the requests changed - update docs/privacy.md in the fork with this test:\n%s", strings.Join(got, "\n\n"))
	}
}

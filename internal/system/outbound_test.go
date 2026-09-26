package system

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/outboundpin"
)

// What an addon's update check sends when its `Update:` is an absolute URL (task 266, the fork's
// docs/privacy.md): that URL with cmd=check_version and the installed version of that addon - no
// token (the Bearer goes only to a system-relative URL), nothing about the system; Go's own
// User-Agent. A change here is a change to the privacy statement.
func TestAddonUpdateCheckOutboundIsPinned(t *testing.T) {
	h, log := outboundpin.Recorder(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("2.0.0")) }))
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	b := AddonScripts{UpdateToken: "olt_secret", HTTP: srv.Client()}
	b.CheckUpdate(context.Background(), Addon{ID: "x", Version: "1.2.3", Update: srv.URL + "/addon/update-check"}, "http://127.0.0.1:80")
	want := "GET /addon/update-check?cmd=check_version&version=1.2.3\nAccept-Encoding: gzip\nUser-Agent: Go-http-client/1.1"
	if got := strings.Join(log.Requests(), "\n\n"); got != want {
		t.Errorf("the request changed - update docs/privacy.md in the fork with this test:\n%s", got)
	}
}

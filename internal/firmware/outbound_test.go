package firmware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/outboundpin"
)

// What the device firmware check sends to eQ-3's update service (task 266, the fork's
// docs/privacy.md): the index is asked with the product HM-CCU3 and the system's firmware version,
// a download with the device type and serial 0 - never a device's serial or the system's address
// or name; Go's own User-Agent. A change here is a change to the privacy statement.
func TestOutboundRequestsArePinned(t *testing.T) {
	h, log := outboundpin.Recorder(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/firmware/download" {
			w.Header().Set("Content-Disposition", `attachment; filename=HmIP-BBL_update_V1_10_16_230616.tgz`)
			_, _ = w.Write([]byte("x"))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New(srv.URL)
	c.SystemVersion = "3.89.11.20260919"
	if _, err := c.Index(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, _, _ = c.Download(t.Context(), "HmIP-BBL", t.TempDir())
	want := []string{
		"GET /firmware/api/firmware/search/DEVICE?product=HM-CCU3&version=3.89.11.20260919\nAccept-Encoding: gzip\nUser-Agent: Go-http-client/1.1",
		"GET /firmware/download?cmd=download&product=HmIP-BBL&serial=0\nAccept-Encoding: gzip\nUser-Agent: Go-http-client/1.1",
	}
	if got := log.Requests(); strings.Join(got, "\n\n") != strings.Join(want, "\n\n") {
		t.Errorf("the requests changed - update docs/privacy.md in the fork with this test:\n%s", strings.Join(got, "\n\n"))
	}
}

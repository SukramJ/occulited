package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/acme"
	"github.com/hobbyquaker/occulited/internal/certpem"
	"github.com/hobbyquaker/occulited/internal/certpem/testcert"
	"github.com/hobbyquaker/occulited/internal/system"
)

// hstsStubInstaller is the certificate stub plus what the box's installer does on the way back
// to self-signed: HSTS that is on goes into the clearing state (system.CertInstaller.Remove,
// tested in its own package).
type hstsStubInstaller struct {
	stubInstaller
	root system.Root
}

func (s *hstsStubInstaller) Remove(ctx context.Context, log func(string)) ([]string, error) {
	if s.root.HTTPSSettings().HSTS {
		_ = os.WriteFile(filepath.Join(string(s.root), system.HSTSClearUntilFile), []byte(strconv.FormatInt(time.Now().Add(7*24*time.Hour).Unix(), 10)), 0o644)
		_ = os.WriteFile(filepath.Join(string(s.root), system.HSTSMarker), []byte("0\n"), 0o644)
	}
	return s.stubInstaller.Remove(ctx, log)
}

// TestHTTPSRoutes: 501 without the config, the defaults with the certificate described, HSTS
// refused on a self-signed certificate and allowed on an issued one, the markers and the
// reload behind a PUT, the max-age rule, and the switch back to self-signed reporting HSTS off.
func TestHTTPSRoutes(t *testing.T) {
	r := fakeRoot(t)
	svc := scriptBox{system.AddonScripts{Root: r}}
	mux := http.NewServeMux()
	(&SystemAPI{Root: r, Services: svc, Log: testLog, Addons: svc, Manager: svc, Nav: svc}).Register(mux)
	srv := httptest.NewServer(mux)
	if st, _, _ := do(t, srv, "GET", "/api/system/v1/https", "", nil); st != 501 {
		t.Fatalf("no config: %d", st)
	}
	srv.Close()

	cs, err := acme.New(filepath.Join(t.TempDir(), "acme"), nil)
	if err != nil {
		t.Fatal(err)
	}
	inst := &hstsStubInstaller{root: r}
	_, _ = inst.Remove(context.Background(), nil) // self-signed to start with
	cs.Issuer, cs.Installer = &stubIssuer{}, inst
	var cmds []string
	rec := func(_ context.Context, name string, args ...string) ([]byte, error) {
		cmds = append(cmds, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	mux = http.NewServeMux()
	(&SystemAPI{Root: r, Services: svc, Log: testLog, Addons: svc, Manager: svc, Nav: svc, Cert: cs, HTTPS: &system.HTTPSConfig{Root: r, Run: rec, Systemd: true}}).Register(mux)
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	st, out, _ := do(t, srv, "GET", "/api/system/v1/https", "", nil)
	if st != 200 || out["redirect_https"] != false || out["hsts"] != false || out["hsts_max_age_days"] != float64(7) || out["hsts_clearing"] != false || out["hsts_clearing_until"] != nil {
		t.Fatalf("defaults: %d %v", st, out)
	}
	if c := out["certificate"].(map[string]any); c["self_signed"] != true || c["managed"] != false || c["mode"] != "self-signed" {
		t.Fatalf("certificate: %v", c)
	}
	// the guard: HSTS on a self-signed certificate is refused, nothing written, no reload
	st, out, _ = do(t, srv, "PUT", "/api/system/v1/https", `{"redirect_https":true,"hsts":true,"hsts_max_age_days":365}`, nil)
	if st != 422 || out["error"] != "invalid" || !strings.Contains(out["message"].(string), "self-signed") || len(cmds) != 0 {
		t.Fatalf("hsts on self-signed: %d %v %v", st, out, cmds)
	}
	if _, err := os.Stat(filepath.Join(string(r), system.HTTPSRedirectMarker)); err == nil {
		t.Fatal("the redirect marker was written despite the refusal")
	}
	// the redirect alone is fine on any certificate: the marker, one reload
	st, out, _ = do(t, srv, "PUT", "/api/system/v1/https", `{"redirect_https":true,"hsts":false,"hsts_max_age_days":365}`, nil)
	if st != 200 || out["redirect_https"] != true || out["hsts"] != false {
		t.Fatalf("redirect: %d %v", st, out)
	}
	if _, err := os.Stat(filepath.Join(string(r), system.HTTPSRedirectMarker)); err != nil {
		t.Fatalf("redirect marker: %v", err)
	}
	if len(cmds) != 1 || cmds[0] != "systemctl reload --no-pager -- lighttpd.service" {
		t.Fatalf("%v", cmds)
	}
	// an issued certificate: HSTS may go on, the marker carries the seconds
	leaf, key, ca := testcert.Issued([]string{"box.example.org"}, time.Now().Add(90*24*time.Hour))
	live, _ := certpem.AssemblePEM(append(leaf, ca...), key)
	if _, err := inst.Install(context.Background(), live, "acme", nil); err != nil {
		t.Fatal(err)
	}
	cmds = nil
	st, out, _ = do(t, srv, "PUT", "/api/system/v1/https", `{"redirect_https":true,"hsts":true,"hsts_max_age_days":30}`, nil)
	if st != 200 || out["hsts"] != true || out["hsts_max_age_days"] != float64(30) || out["certificate"].(map[string]any)["managed"] != true {
		t.Fatalf("hsts on: %d %v", st, out)
	}
	if b, err := os.ReadFile(filepath.Join(string(r), system.HSTSMarker)); err != nil || strings.TrimSpace(string(b)) != "2592000" {
		t.Fatalf("hsts marker: %v %q", err, b)
	}
	if len(cmds) != 1 {
		t.Fatalf("%v", cmds)
	}
	// the max-age rule
	if st, out, _ := do(t, srv, "PUT", "/api/system/v1/https", `{"redirect_https":true,"hsts":true,"hsts_max_age_days":9999}`, nil); st != 422 || !strings.Contains(out["message"].(string), "between 1 and") {
		t.Fatalf("max-age: %d %v", st, out)
	}
	// task 96: an explicit 0 is refused, no value at all is the 7-day default
	if st, out, _ := do(t, srv, "PUT", "/api/system/v1/https", `{"redirect_https":true,"hsts":true,"hsts_max_age_days":0}`, nil); st != 422 || !strings.Contains(out["message"].(string), "between 1 and") {
		t.Fatalf("max-age 0: %d %v", st, out)
	}
	for _, body := range []string{`{"redirect_https":true,"hsts":true}`, `{"redirect_https":true,"hsts":true,"hsts_max_age_days":null}`} {
		if st, out, _ := do(t, srv, "PUT", "/api/system/v1/https", body, nil); st != 200 || out["hsts"] != true || out["hsts_max_age_days"] != float64(7) {
			t.Fatalf("%s: %d %v", body, st, out)
		}
		if b, err := os.ReadFile(filepath.Join(string(r), system.HSTSMarker)); err != nil || strings.TrimSpace(string(b)) != "604800" {
			t.Fatalf("a week: %v %q", err, b)
		}
	}
	// a bad body
	if st, out, _ := do(t, srv, "PUT", "/api/system/v1/https", `{"hsts":"yes"}`, nil); st != 422 || out["error"] != "invalid-body" {
		t.Fatalf("bad body: %d %v", st, out)
	}
	// task 96: switched off, the box clears - hsts false, the clearing and its deadline (the
	// previous seven days)
	before := time.Now()
	st, out, _ = do(t, srv, "PUT", "/api/system/v1/https", `{"redirect_https":true,"hsts":false,"hsts_max_age_days":7}`, nil)
	if st != 200 || out["hsts"] != false || out["hsts_clearing"] != true || out["hsts_max_age_days"] != float64(7) {
		t.Fatalf("off: %d %v", st, out)
	}
	if u, err := time.Parse(time.RFC3339, out["hsts_clearing_until"].(string)); err != nil || u.Before(before.Add(7*24*time.Hour-time.Second)) || u.After(time.Now().Add(7*24*time.Hour)) {
		t.Fatalf("deadline: %v %v", out["hsts_clearing_until"], err)
	}
	if b, err := os.ReadFile(filepath.Join(string(r), system.HSTSMarker)); err != nil || strings.TrimSpace(string(b)) != "0" {
		t.Fatalf("clearing marker: %v %q", err, b)
	}
	// a deadline in the past ends at the next GET: the files go, one reload, no clearing reported
	_ = os.WriteFile(filepath.Join(string(r), system.HSTSClearUntilFile), []byte("1000\n"), 0o644)
	cmds = nil
	st, out, _ = do(t, srv, "GET", "/api/system/v1/https", "", nil)
	if st != 200 || out["hsts"] != false || out["hsts_clearing"] != false || out["hsts_clearing_until"] != nil || len(cmds) != 1 {
		t.Fatalf("expired: %d %v %v", st, out, cmds)
	}
	if _, err := os.Stat(filepath.Join(string(r), system.HSTSMarker)); err == nil {
		t.Fatal("the marker is still there after the deadline")
	}
	// the switch back to self-signed takes HSTS with it, into the clearing state, and says so
	if st, out, _ := do(t, srv, "PUT", "/api/system/v1/https", `{"redirect_https":true,"hsts":true,"hsts_max_age_days":30}`, nil); st != 200 || out["hsts"] != true {
		t.Fatalf("on again: %d %v", st, out)
	}
	st, out, _ = do(t, srv, "POST", "/api/system/v1/certificate/self-signed", "", nil)
	if st != 200 || out["hsts_disabled"] != true || out["hsts_clearing_until"] == nil {
		t.Fatalf("self-signed: %d %v", st, out)
	}
	st, out, _ = do(t, srv, "GET", "/api/system/v1/https", "", nil)
	if st != 200 || out["hsts"] != false || out["hsts_clearing"] != true || out["redirect_https"] != true || out["certificate"].(map[string]any)["self_signed"] != true {
		t.Fatalf("after the switch back: %d %v", st, out)
	}
	// and a second switch back has nothing to report
	if st, out, _ := do(t, srv, "POST", "/api/system/v1/certificate/self-signed", "", nil); st != 200 || out["hsts_disabled"] != false {
		t.Fatalf("second self-signed: %d %v", st, out)
	}
}

// TestSystemUpdateWayBackClearsHSTS (task 96): staging a file that may be the way back to OpenCCU
// switches HSTS into the clearing state and says so; an openccu-lite release leaves it on.
func TestSystemUpdateWayBackClearsHSTS(t *testing.T) {
	r := fakeRoot(t)
	_ = os.MkdirAll(filepath.Join(string(r), "usr/local/tmp"), 0o755)
	_ = os.MkdirAll(filepath.Join(string(r), "etc/config"), 0o755)
	_ = os.WriteFile(filepath.Join(string(r), "VERSION"), []byte("VERSION=3.89.8.20260719\nPRODUCT=ova\nPLATFORM=ova\n"), 0o644)
	var cmds []string
	rec := func(_ context.Context, name string, args ...string) ([]byte, error) {
		cmds = append(cmds, name)
		return nil, nil
	}
	mux := http.NewServeMux()
	(&SystemAPI{Root: r, HTTPS: &system.HTTPSConfig{Root: r, Run: rec, Systemd: true}}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	upload := func(name string) map[string]any {
		t.Helper()
		st, out, _ := do(t, srv, "POST", "/api/system/v1/system-update/upload?name="+name, string(firmwareZip(t)), nil)
		if st != 200 {
			t.Fatalf("upload %s: %d %v", name, st, out)
		}
		return out
	}
	hstsOn := func() {
		_ = os.Remove(filepath.Join(string(r), system.HSTSClearUntilFile))
		_ = os.WriteFile(filepath.Join(string(r), system.HSTSMarker), []byte("2592000\n"), 0o644)
	}

	hstsOn()
	out := upload("openccu-lite-x86_64-ova-1.0.0.zip")
	if out["way_back"] != false || out["hsts_cleared"] != nil || len(cmds) != 0 || !r.HTTPSSettings().HSTS {
		t.Fatalf("a lite release: %v %v", out, cmds)
	}
	out = upload("OpenCCU-3.89.8.20260719-ova.zip")
	if out["way_back"] != true || out["hsts_cleared"] != true || out["hsts_clearing_until"] == nil || len(cmds) != 1 {
		t.Fatalf("the way back: %v %v", out, cmds)
	}
	if s := r.HTTPSSettings(); s.HSTS || !s.HSTSClearing {
		t.Fatalf("after the way back: %+v", s)
	}
	// staged again while it clears: the deadline is reported, nothing switched, no reload
	out = upload("OpenCCU-3.89.8.20260719-ova.zip")
	if out["hsts_cleared"] != nil || out["hsts_clearing_until"] == nil || len(cmds) != 1 {
		t.Fatalf("again: %v %v", out, cmds)
	}
	if st, out, _ := do(t, srv, "GET", "/api/system/v1/system-update", "", nil); st != 200 || out["staged"].(map[string]any)["way_back"] != true {
		t.Fatalf("GET: %d %v", st, out)
	}
}

// firmwareZip is the smallest file the staging accepts as a zip: the two EULAs and an image.
func firmwareZip(t *testing.T) []byte {
	t.Helper()
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	for _, n := range []string{"rootfs.img", "EULA.en", "EULA.de"} {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: n, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write(bytes.Repeat([]byte("z"), 300))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return zb.Bytes()
}

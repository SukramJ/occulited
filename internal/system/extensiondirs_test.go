package system

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/priv"
)

const rftypesState = "usr/local/etc/config/extensions/rftypes"

func TestDeviceDescriptionsOverlay(t *testing.T) {
	r := rootWith(t, map[string]string{
		"usr/libexec/occu/lite-extension-dirs": "#!/bin/sh\n",
		"proc/self/mountinfo": "30 20 0:40 / /firmware/rftypes rw,relatime shared:5 - overlay overlay rw,lowerdir=/firmware/rftypes,upperdir=/usr/local/etc/config/extensions/rftypes/upper\n" +
			"31 20 8:3 / /usr/local rw,noatime - ext4 /dev/sda3 rw\n",
		rftypesState + "/image-names":       "replaceMap\nrf_4dis.xml\nrf_cfm_tw.xml\nrf_sec_sc.xml\n",
		rftypesState + "/upper/hb-uni.xml":  "<device/>",
		rftypesState + "/upper/hb-lc.xml":   "<device/>",
		rftypesState + "/upper/rf_4dis.xml": "patched",
		// what rfd sees: the merged view - the test stands in for the kernel
		"firmware/rftypes/rf_4dis.xml":   "patched",
		"firmware/rftypes/rf_cfm_tw.xml": "<device/>",
		"firmware/rftypes/rf_sec_sc.xml": "<device/>",
		"firmware/rftypes/hb-uni.xml":    "<device/>",
		"firmware/rftypes/hb-lc.xml":     "<device/>",
		"firmware/rftypes/replaceMap/x":  "",
	})
	d := r.DeviceDescriptions()
	if !d.Available || d.Mode != "overlay" || d.Path != RFTypesDir {
		t.Errorf("%+v", d)
	}
	if d.Entries != 6 || d.Image != 4 || d.Added != 2 || d.Replaced != 1 || d.Removed != 0 {
		t.Errorf("counts: %+v", d)
	}
	if d.State != "/usr/local/etc/config/extensions/rftypes" {
		t.Errorf("state: %s", d.State)
	}
}

func TestDeviceDescriptionsCopy(t *testing.T) {
	r := rootWith(t, map[string]string{
		"usr/libexec/occu/lite-extension-dirs": "#!/bin/sh\n",
		"proc/self/mountinfo":                  "31 20 8:3 /etc/config/extensions/rftypes/copy /firmware/rftypes rw,noatime shared:6 - ext4 /dev/sda3 rw\n",
		rftypesState + "/image-names":          "replaceMap\nrf_4dis.xml\nrf_cfm_tw.xml\nrf_sec_sc.xml\n",
		rftypesState + "/copy/rf_4dis.xml":     "<device/>",
		rftypesState + "/copy/replaceMap/x":    "",
		rftypesState + "/copy/hb-uni.xml":      "<device/>",
		"firmware/rftypes/rf_4dis.xml":         "<device/>",
		"firmware/rftypes/hb-uni.xml":          "<device/>",
	})
	// an addon's replacement is a link with an image name; rf_cfm_tw.xml is missing altogether
	if err := os.Symlink("/usr/local/addons/hb/x.xml", filepath.Join(string(r), rftypesState, "copy/rf_sec_sc.xml")); err != nil {
		t.Fatal(err)
	}
	d := r.DeviceDescriptions()
	if d.Mode != "copy" || d.Image != 4 || d.Added != 1 || d.Replaced != 1 || d.Removed != 1 {
		t.Errorf("%+v", d)
	}
}

func TestDeviceDescriptionsNone(t *testing.T) {
	// an image without the unit: the read-only directory, nothing else
	r := rootWith(t, map[string]string{"proc/self/mountinfo": "", "firmware/rftypes/rf_4dis.xml": ""})
	d := r.DeviceDescriptions()
	if d.Available || d.Mode != "none" || d.Entries != 1 || d.Image != 0 || d.Added != 0 {
		t.Errorf("%+v", d)
	}
	if _, err := ResetDeviceDescriptions(context.Background(), r, nil); err == nil || !strings.Contains(err.Error(), "no writable device descriptions") {
		t.Errorf("reset without the tool: %v", err)
	}
}

func TestMountedType(t *testing.T) {
	mi := "30 20 0:40 / /firmware/rftypes rw - overlay overlay rw\n31 20 8:3 /x /firmware/rftypes rw,noatime shared:6 - ext4 /dev/sda3 rw\n32 20 0:41 / /firmware rw - squashfs /dev/loop0 ro\n"
	if got := mountedType(mi, "/firmware/rftypes"); got != "ext4" {
		t.Errorf("the topmost mount: %q", got)
	}
	if got := mountedType(mi, "/firmware"); got != "squashfs" {
		t.Errorf("%q", got)
	}
	if got := mountedType(mi, "/usr/local"); got != "" {
		t.Errorf("%q", got)
	}
}

// runPriv answers Run with a fixed result and notes the calls.
type runPriv struct {
	priv.Local
	calls *[]string
	exit  int
	err   error
}

func (p runPriv) Run(_ context.Context, name string, args []string, _ []byte) (priv.Result, error) {
	*p.calls = append(*p.calls, name+" "+strings.Join(args, " "))
	return priv.Result{Exit: p.exit, Stderr: []byte("busy")}, p.err
}

type resetServices struct {
	list    []Service
	control []string
	err     error
}

func (f *resetServices) List() ([]Service, error) { return f.list, nil }
func (f *resetServices) Control(_ context.Context, id, action string) (string, error) {
	f.control = append(f.control, id+" "+action)
	return "", f.err
}

func TestResetDeviceDescriptions(t *testing.T) {
	r := rootWith(t, map[string]string{
		"usr/libexec/occu/lite-extension-dirs": "#!/bin/sh\n",
		"proc/self/mountinfo":                  "30 20 0:40 / /firmware/rftypes rw - overlay overlay rw\n",
		rftypesState + "/image-names":          "rf_4dis.xml\n",
		rftypesState + "/upper/hb.xml":         "",
		"firmware/rftypes/rf_4dis.xml":         "",
		"firmware/rftypes/hb.xml":              "",
	})
	var calls []string
	old := Priv
	t.Cleanup(func() { Priv = old })
	Priv = runPriv{calls: &calls}
	svc := &resetServices{list: []Service{{ID: "rfd", Running: true}}}
	d, err := ResetDeviceDescriptions(context.Background(), r, svc)
	if err != nil || !d.Restarted || d.Added != 1 {
		t.Fatalf("%v %+v", err, d)
	}
	if want := r.join(ExtensionDirsTool) + " reset /firmware/rftypes"; len(calls) != 1 || calls[0] != want {
		t.Errorf("calls: %v", calls)
	}
	if strings.Join(svc.control, ",") != "rfd restart" {
		t.Errorf("control: %v", svc.control)
	}
	// rfd off: reset, no restart
	svc = &resetServices{list: []Service{{ID: "rfd", Running: false}}}
	if d, err := ResetDeviceDescriptions(context.Background(), r, svc); err != nil || d.Restarted || len(svc.control) != 0 {
		t.Errorf("rfd off: %v %+v %v", err, d, svc.control)
	}
	// the script refuses (busy): the error carries its output, nothing is restarted
	Priv = runPriv{calls: &calls, exit: 1}
	svc = &resetServices{list: []Service{{ID: "rfd", Running: true}}}
	if _, err := ResetDeviceDescriptions(context.Background(), r, svc); err == nil || !strings.Contains(err.Error(), "busy") || len(svc.control) != 0 {
		t.Errorf("refused: %v %v", err, svc.control)
	}
	// the restart fails: said so, the reset itself is done
	Priv = runPriv{calls: &calls}
	svc = &resetServices{list: []Service{{ID: "rfd", Running: true}}, err: errors.New("no")}
	if _, err := ResetDeviceDescriptions(context.Background(), r, svc); err == nil || !strings.Contains(err.Error(), "rfd restart") {
		t.Errorf("restart failed: %v", err)
	}
}

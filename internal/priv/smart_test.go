package priv

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// task 69: the SMART read is one narrow operation - a whole disk of a fixed shape, the command line
// built by the helper - and every other way of asking is refused before anything runs

type recordSmart struct {
	Local
	devices []string
}

func (r *recordSmart) Smartctl(_ context.Context, device string) (Result, error) {
	r.devices = append(r.devices, device)
	return Result{Stdout: []byte(`{"smart_status":{"passed":true}}`), Exit: 0}, nil
}

func smartHelper(t *testing.T) (Client, *recordSmart, string) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "h.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ops := &recordSmart{}
	srv := &Server{Policy: DefaultPolicy("/", "/usr/local/etc/occulite"), Ops: ops}
	go func() { _ = srv.Serve(ctx, l) }()
	return Client{Socket: sock}, ops, sock
}

func TestSmartctlDevicePolicy(t *testing.T) {
	c, ops, _ := smartHelper(t)
	for _, dev := range []string{"/dev/sda", "/dev/sdb", "/dev/sdaa", "/dev/nvme0n1", "/dev/nvme10n2"} {
		r, err := c.Smartctl(context.Background(), dev)
		if err != nil || !strings.Contains(string(r.Stdout), "passed") {
			t.Errorf("%s: %v %+v", dev, err, r)
		}
	}
	refused := []struct{ name, dev string }{
		{"empty", ""},
		{"a partition", "/dev/sda1"},
		{"an NVMe partition", "/dev/nvme0n1p1"},
		{"the NVMe controller", "/dev/nvme0"},
		{"an SD card", "/dev/mmcblk0"},
		{"an eMMC boot area", "/dev/mmcblk0boot0"},
		{"a virtio disk", "/dev/vda"},
		{"a relative name", "sda"},
		{"no /dev", "/tmp/sda"},
		{"a dot-dot", "/dev/../dev/sda"},
		{"a by-id link", "/dev/disk/by-id/ata-Samsung_SSD"},
		{"an option instead of a device", "-d"},
		{"an option glued on", "/dev/sda -s on"},
		{"a trailing newline", "/dev/sda\n"},
		{"a trailing space", "/dev/sda "},
		{"upper case", "/dev/SDA"},
		{"three letters", "/dev/sdaaa"},
		{"memory", "/dev/mem"},
		{"a generic SCSI node", "/dev/sg0"},
	}
	for _, r := range refused {
		if _, err := c.Smartctl(context.Background(), r.dev); err == nil || !errors.Is(err, ErrRefused) {
			t.Errorf("%s (%q): %v", r.name, r.dev, err)
		}
	}
	if len(ops.devices) != 5 {
		t.Fatalf("a refused read reached the operation: %q", ops.devices)
	}
}

// the Client sends only the device; a hand-made request that carries anything else is refused,
// so no option can travel over the socket
func TestSmartctlRefusesExtraFields(t *testing.T) {
	_, ops, sock := smartHelper(t)
	raw := func(req request) response {
		t.Helper()
		conn, err := net.Dial("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if err := json.NewEncoder(conn).Encode(req); err != nil {
			t.Fatal(err)
		}
		var res response
		if err := json.NewDecoder(conn).Decode(&res); err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := raw(request{Op: opSmartctl, Path: "/dev/sda"}); !res.OK {
		t.Fatalf("the plain request: %+v", res)
	}
	extras := map[string]request{
		"arguments":        {Op: opSmartctl, Path: "/dev/sda", Args: []string{"-s", "on"}},
		"one argument":     {Op: opSmartctl, Path: "/dev/sda", Args: []string{"-x"}},
		"a program":        {Op: opSmartctl, Path: "/dev/sda", Name: "/bin/sh"},
		"stdin":            {Op: opSmartctl, Path: "/dev/sda", Stdin: []byte("y\n")},
		"an environment":   {Op: opSmartctl, Path: "/dev/sda", Env: []string{"LD_PRELOAD=/tmp/x.so"}},
		"a directory":      {Op: opSmartctl, Path: "/dev/sda", Dir: "/tmp"},
		"a second path":    {Op: opSmartctl, Path: "/dev/sda", Src: "/dev/sdb"},
		"a destination":    {Op: opSmartctl, Path: "/dev/sda", Dst: "/etc/config/x"},
		"a target":         {Op: opSmartctl, Path: "/dev/sda", Target: "/dev/sdb"},
		"data":             {Op: opSmartctl, Path: "/dev/sda", Data: []byte("x")},
		"no device at all": {Op: opSmartctl},
	}
	for name, req := range extras {
		if res := raw(req); res.OK || !strings.HasPrefix(res.Error, "refused:") {
			t.Errorf("%s: %+v", name, res)
		}
	}
	if len(ops.devices) != 1 {
		t.Fatalf("a refused request reached the operation: %q", ops.devices)
	}
	// and smartctl is not a program the generic run operation would start
	pol := DefaultPolicy("/", "/usr/local/etc/occulite")
	for _, name := range []string{"smartctl", "/usr/bin/smartctl", "/usr/sbin/smartctl"} {
		if pol.programAllowed(name, []string{"-s", "on", "/dev/sda"}) {
			t.Errorf("%s is on the program list", name)
		}
	}
}

// Local runs exactly SmartctlArgs, checks the operand itself as well, and passes a non-zero exit
// through as Result.Exit with the JSON on stdout
func TestLocalSmartctl(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "argv")
	fake := filepath.Join(dir, "smartctl")
	script := "#!/bin/sh\necho \"$@\" > " + log + "\necho '{\"smart_status\":{\"passed\":false}}'\nexit 8\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath, oldBlock := smartctlPath, blockDevice
	var checked []string
	smartctlPath = fake
	blockDevice = func(path string) error {
		checked = append(checked, path)
		if path == "/dev/sdz" {
			return errors.New("/dev/sdz: no such device")
		}
		return nil
	}
	t.Cleanup(func() { smartctlPath, blockDevice = oldPath, oldBlock })

	r, err := (Local{}).Smartctl(context.Background(), "/dev/sda")
	if err != nil || r.Exit != 8 || !strings.Contains(string(r.Stdout), `"passed":false`) {
		t.Fatalf("run: %v %+v", err, r)
	}
	argv, _ := os.ReadFile(log)
	if got, want := strings.Fields(string(argv)), SmartctlArgs("/dev/sda"); !reflect.DeepEqual(got, want) {
		t.Fatalf("argv %q, want %q", got, want)
	}
	if !reflect.DeepEqual(SmartctlArgs("/dev/nvme0n1"), []string{"-j", "-H", "-A", "-i", "/dev/nvme0n1"}) {
		t.Fatalf("the argument list changed: %q", SmartctlArgs("/dev/nvme0n1"))
	}
	if _, err := (Local{}).Smartctl(context.Background(), "/dev/sdz"); err == nil {
		t.Error("a device that is not a block device was read")
	}
	if _, err := (Local{}).Smartctl(context.Background(), "/dev/sda1"); err == nil {
		t.Error("a partition was read")
	}
	if len(checked) != 2 {
		t.Errorf("the shape is checked before the node: %q", checked)
	}
	// the real check: a regular file and a character device are not block devices
	if err := oldBlock(fake); err == nil {
		t.Error("a regular file passed the block device check")
	}
	if _, err := os.Stat("/dev/null"); err == nil {
		if err := oldBlock("/dev/null"); err == nil {
			t.Error("a character device passed the block device check")
		}
	}
}

// task 111: a box without smartctl - the VM and the container products - answers "not available",
// locally and over the socket, which the storage panel takes as "the host monitors the disks" and
// never as a failed read
func TestSmartctlNotAvailable(t *testing.T) {
	oldPath, oldBlock := smartctlPath, blockDevice
	smartctlPath = filepath.Join(t.TempDir(), "smartctl")
	nodes := 0
	blockDevice = func(string) error { nodes++; return nil }
	t.Cleanup(func() { smartctlPath, blockDevice = oldPath, oldBlock })

	_, err := (Local{}).Smartctl(context.Background(), "/dev/sda")
	if !errors.Is(err, ErrNotAvailable) || errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "is not installed") {
		t.Fatalf("no smartctl: %v", err)
	}
	if _, err := (Local{}).Smartctl(context.Background(), "/dev/sda1"); err == nil || errors.Is(err, ErrNotAvailable) {
		t.Errorf("a partition is refused for its shape before anything else: %v", err)
	}
	if nodes != 0 {
		t.Errorf("the device node was checked for a program that is not there: %d", nodes)
	}

	sock := filepath.Join(t.TempDir(), "h.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv := &Server{Policy: DefaultPolicy("/", "/usr/local/etc/occulite"), Ops: &Local{}}
	go func() { _ = srv.Serve(ctx, l) }()
	r, err := (Client{Socket: sock}).Smartctl(context.Background(), "/dev/sda")
	if !errors.Is(err, ErrNotAvailable) || errors.Is(err, ErrRefused) || len(r.Stdout) != 0 {
		t.Fatalf("over the socket: %v %+v", err, r)
	}
	if n := strings.Count(err.Error(), ErrNotAvailable.Error()); n != 1 || !strings.Contains(err.Error(), "is not installed") {
		t.Errorf("the reason, once: %q", err)
	}
	// an answer that is neither a refusal nor "not available" stays a plain failure
	for _, res := range []response{{Error: "exec: smartctl: permission denied"}, {Error: "not available"}} {
		if err := res.err(); err == nil || errors.Is(err, ErrNotAvailable) || errors.Is(err, ErrRefused) {
			t.Errorf("%q became %v", res.Error, err)
		}
	}
}

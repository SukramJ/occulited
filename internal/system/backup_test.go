package system

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupCreateCheckRestore(t *testing.T) {
	r := rootWith(t, map[string]string{"VERSION": "VERSION=3.89.8\n", "proc/sys/kernel/hostname": "lite\n", "usr/local/tmp/.keep": ""})
	rec := &recorder{}
	// the recorder does not create the file: CreateBackup must notice
	if _, err := r.CreateBackup(context.Background(), rec.run); err == nil {
		t.Fatal("no file, no error")
	}
	creating := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		_ = os.WriteFile(args[0], []byte("sbk"), 0o644)
		return rec.run(ctx, name, args...)
	}
	path, err := r.CreateBackup(context.Background(), creating)
	if err != nil || !strings.HasSuffix(path, ".sbk") || !strings.Contains(path, "/usr/local/tmp/lite-3.89.8-") {
		t.Fatalf("%v %s", err, path)
	}
	if !strings.HasSuffix(rec.calls[len(rec.calls)-1], "/bin/createBackup.sh "+path) {
		t.Errorf("%v", rec.calls)
	}

	up, err := r.SaveUpload(strings.NewReader("data"), "../../etc/passwd")
	if err != nil || !strings.HasSuffix(up, "/usr/local/tmp/restore-upload.sbk") {
		t.Fatalf("%v %s", err, up)
	}
	up, _ = r.SaveUpload(strings.NewReader("data"), "ccu-3.75.7-2026-01-01-1200.sbk")
	if !strings.HasSuffix(up, "restore-ccu-3.75.7-2026-01-01-1200.sbk") {
		t.Errorf("%s", up)
	}
	// the upload is staged and moved by the helper (B-20): the content arrives whole and the
	// staging directory is left empty
	if b, err := os.ReadFile(up); err != nil || string(b) != "data" {
		t.Errorf("upload content: %v %q", err, b)
	}
	if entries, _ := os.ReadDir(r.join(StagingDir)); len(entries) != 0 {
		t.Errorf("staging not empty: %v", entries)
	}
	if err := r.RemoveBackupFile(filepath.Join(t.TempDir(), "x.sbk")); err == nil {
		t.Error("RemoveBackupFile must refuse a path outside the backup directory")
	}

	// task 296: the check answers the key prompt with an empty line and goes on past it (-f), so a
	// backup with a key is valid and says which side has one; the script's "does NOT match ...,
	// forced restore." about the empty key is not shown as a failed check
	var checkIn string
	var checkArgs []string
	checker := func(_ context.Context, in []byte, name string, args ...string) ([]byte, error) {
		checkIn, checkArgs = string(in), args
		return []byte("1) Checking sbk backup file consistency:\n   generated on 3.75.7.20250101, applying to 3.89.8, OK\n   backup and/or system protected by security key...\n   Enter security key:    system NOT protected with a key, OK\n   backup protected with a key, WARNING: security key does NOT match key in backup, forced restore.\n\nConfig check processing only, exiting."), nil
	}
	c := r.CheckBackup(context.Background(), checker, up)
	if !c.OK || c.BackupVersion != "3.75.7.20250101" || c.RunningVersion != "3.89.8" || !c.NeedsKey || c.HasRega || !c.BackupKey || c.SystemKey {
		t.Errorf("%+v", c)
	}
	if checkIn != "\n" || strings.Join(checkArgs, " ") != "-c -f "+up {
		t.Errorf("check stdin %q args %v", checkIn, checkArgs)
	}
	if strings.Contains(c.Output, "does NOT match") || strings.Contains(c.Output, "forced restore") || !strings.Contains(c.Output, "backup protected with a key, the passphrase is asked for below.") {
		t.Errorf("output: %q", c.Output)
	}
	sysOnly := func(context.Context, []byte, string, ...string) ([]byte, error) {
		return []byte("   backup and/or system protected by security key...\n   system protected with a key, WARNING: security key does NOT match system key, forced restore.\n   backup NOT protected with a key, OK\n"), nil
	}
	if c := r.CheckBackup(context.Background(), sysOnly, up); !c.OK || !c.SystemKey || c.BackupKey || !c.NeedsKey {
		t.Errorf("system key only: %+v", c)
	}
	rec.calls = nil
	var stdin string
	withStdin := func(ctx context.Context, in []byte, name string, args ...string) ([]byte, error) {
		stdin = string(in)
		return rec.run(ctx, name, args...)
	}
	out, err := r.RestoreBackup(context.Background(), withStdin, up, "1234", true)
	if err != nil || out != "" || !strings.HasSuffix(rec.calls[0], "/bin/restoreBackup.sh -f "+up) {
		t.Errorf("%v %q %v", err, out, rec.calls)
	}
	// the key reaches the script's prompt as one line (B-144)
	if stdin != "1234\n" {
		t.Errorf("stdin %q", stdin)
	}
	if _, err := r.RestoreBackup(context.Background(), withStdin, up, "", false); err != nil || stdin != "\n" || !strings.HasSuffix(rec.calls[1], "/bin/restoreBackup.sh "+up) {
		t.Errorf("no key: %v %q %v", err, stdin, rec.calls)
	}
}

// B-144: the firmware's script asks for the key with read under set -e. Without a line on its
// standard input it stops at the prompt; RestoreBackup's default runner gives it the line.
// The reboot countdown's marker of a restore is carried across the restore at boot as a dotfile in
// /usr/local/tmp, like the system's identity (openccu-lite B-193).
func TestCarryBootMarker(t *testing.T) {
	r := rootWith(t, map[string]string{"usr/local/tmp/.keep": ""})
	if err := r.CarryBootMarker([]byte("{\"kind\":\"restore\"}\n")); err != nil {
		t.Fatal(err)
	}
	path := r.join(BootMarkerCarryFile)
	if b, err := os.ReadFile(path); err != nil || string(b) != "{\"kind\":\"restore\"}\n" {
		t.Fatalf("carried marker: %v %q", err, b)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("mode: %v %v", st, err)
	}
	if _, err := os.Stat(r.join(StagingDir + "/.occulite-boot-timing.part")); err == nil {
		t.Error("the staged file stayed")
	}
	if err := r.RemoveCarriedBootMarker(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("the carried marker stayed")
	}
	if err := r.RemoveCarriedBootMarker(); err != nil {
		t.Error("removing a missing marker failed:", err)
	}
}

func TestRestoreBackupKeyReachesScript(t *testing.T) {
	r := rootWith(t, map[string]string{"usr/local/tmp/restore-x.sbk": "sbk"})
	script := r.join("/bin/restoreBackup.sh")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\nset -e\necho \"   backup and/or system protected by security key...\"\nread -r SECURITY_KEY\necho \"key=[$SECURITY_KEY] args=$*\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	up := r.join("/usr/local/tmp/restore-x.sbk")
	// what the API did before: no standard input, the prompt meets EOF
	if _, err := ExecRunner(context.Background(), script, "-r", "-f", up); err == nil {
		t.Fatal("the fake script must fail without stdin, as the firmware's does")
	}
	out, err := r.RestoreBackup(context.Background(), nil, up, "s3cret", true)
	if err != nil || !strings.Contains(out, "key=[s3cret] args=-f "+up) {
		t.Fatalf("%v %q", err, out)
	}
	out, err = r.RestoreBackup(context.Background(), nil, up, "", false)
	if err != nil || !strings.Contains(out, "key=[] args="+up) {
		t.Fatalf("no key: %v %q", err, out)
	}
	if out, err := DryStdinRunner(context.Background(), []byte("s3cret\n"), script, "-r"); err != nil || out != nil {
		t.Errorf("dry: %v %q", err, out)
	}
}

func TestCronBackup(t *testing.T) {
	r := rootWith(t, map[string]string{"etc/config/.keep": "", "media/usb0/backup/a-2026-01-01.sbk": "x", "media/usb0/backup/b-2026-02-01.sbk": "yy", "media/usb0/backup/notes.txt": ""})
	c := r.ReadCronBackup()
	if !c.Enabled || c.Path != "/media/usb0/backup" || c.MaxBackups != 30 || !c.PathExists || len(c.Backups) != 2 {
		t.Fatalf("%+v", c)
	}
	if err := r.SetCronBackup(CronBackupSettings{Enabled: false, Path: "relative", MaxBackups: 5}); err == nil {
		t.Error("relative path accepted")
	}
	if err := r.SetCronBackup(CronBackupSettings{Enabled: false, Path: "/usr/local", MaxBackups: 5}); err == nil {
		t.Error("/usr/local accepted")
	}
	if err := r.SetCronBackup(CronBackupSettings{Enabled: false, Path: "/media/usb1/sbk", MaxBackups: 5}); err != nil {
		t.Fatal(err)
	}
	c = r.ReadCronBackup()
	if c.Enabled || c.Path != "/media/usb1/sbk" || c.MaxBackups != 5 || c.PathExists || len(c.Backups) != 0 {
		t.Errorf("%+v", c)
	}
	if err := r.SetCronBackup(CronBackupSettings{Enabled: true, Path: "/media/usb0/backup", MaxBackups: 0}); err != nil {
		t.Fatal(err)
	}
	if c = r.ReadCronBackup(); !c.Enabled || c.MaxBackups != 0 {
		t.Errorf("%+v", c)
	}
	rec := &recorder{}
	if _, err := r.RunCronBackup(context.Background(), rec.run); err != nil || !strings.HasSuffix(rec.calls[0], "/bin/cronBackup.sh") {
		t.Errorf("%v %v", err, rec.calls)
	}
}

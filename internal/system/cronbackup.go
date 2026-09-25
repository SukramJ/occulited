package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CronBackup is the firmware's scheduled backup as its crontab runs it: /bin/cronBackup.sh at
// 00:07 unless /etc/config/NoCronBackup exists, into CronBackupPath (default /media/usb0/backup),
// keeping CronBackupMaxBackups files (default 30). The script itself refuses a path on the root
// filesystem, on tmpfs or /usr/local, and creates the directory when its parent exists.
type CronBackup struct {
	Enabled    bool         `json:"enabled"`
	Path       string       `json:"path"`
	MaxBackups int          `json:"max_backups"`
	Backups    []BackupFile `json:"backups"`
	PathExists bool         `json:"path_exists"`
	// OnUserfs: the target resolves onto the box's own /usr/local. That is the default on a box
	// with no USB stick - upstream's S62HMServer.script points /media/usb0 at the SD card for the
	// WebUI's diagram data - and cronBackup.sh's own guard does not catch it, because it compares
	// the resolved path against "/usr/local" exactly. A backup there is lost with the box, and 30
	// of them (the default) are ~30 GB on a 29 GB userfs.
	OnUserfs bool `json:"on_userfs,omitempty"`
	// RealPath is what the target resolves to when that differs from Path.
	RealPath string `json:"real_path,omitempty"`
}

// BackupFile is one .sbk in the backup directory.
type BackupFile struct {
	Name string    `json:"name"`
	Size int64     `json:"size"`
	Time time.Time `json:"time"`
}

// ReadCronBackup reads the markers and lists the backups present.
func (r Root) ReadCronBackup() CronBackup {
	c := CronBackup{Enabled: true, Path: "/media/usb0/backup", MaxBackups: 30, Backups: []BackupFile{}}
	if _, err := os.Stat(r.join("/etc/config/NoCronBackup")); err == nil {
		c.Enabled = false
	}
	if p := strings.TrimSpace(readFile(r.join("/etc/config/CronBackupPath"))); p != "" {
		c.Path = p
	}
	if n, err := strconv.Atoi(strings.TrimSpace(readFile(r.join("/etc/config/CronBackupMaxBackups")))); err == nil && n >= 0 {
		c.MaxBackups = n
	}
	entries, err := os.ReadDir(r.join(c.Path))
	if err == nil {
		c.PathExists = true
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".sbk") {
				continue
			}
			if st, err := e.Info(); err == nil {
				c.Backups = append(c.Backups, BackupFile{Name: e.Name(), Size: st.Size(), Time: st.ModTime()})
			}
		}
		sort.Slice(c.Backups, func(i, j int) bool { return c.Backups[i].Time.After(c.Backups[j].Time) })
	}
	if c.PathExists {
		if real, err := filepath.EvalSymlinks(r.join(c.Path)); err == nil {
			rel := real
			if root := string(r); root != "" && root != "/" {
				rel = strings.TrimPrefix(real, root)
			}
			if !strings.HasPrefix(rel, "/") {
				rel = "/" + rel
			}
			if rel != c.Path {
				c.RealPath = rel
			}
			if same, ok := sameFilesystem(real, r.join("/usr/local")); ok {
				c.OnUserfs = same
			}
		}
	}
	return c
}

// CronBackupSettings is the writable part.
type CronBackupSettings struct {
	Enabled    bool   `json:"enabled"`
	Path       string `json:"path"`
	MaxBackups int    `json:"max_backups"`
}

// SetCronBackup writes the markers the crontab and cronBackup.sh read.
func (r Root) SetCronBackup(s CronBackupSettings) error {
	s.Path = strings.TrimSpace(s.Path)
	if s.Path == "" || !filepath.IsAbs(s.Path) || filepath.Clean(s.Path) != s.Path || s.Path == "/" || s.Path == "/usr/local" {
		return errors.New("path: an absolute directory path (a USB stick under /media, a mounted share), not / or /usr/local")
	}
	if s.MaxBackups < 0 || s.MaxBackups > 1000 {
		return errors.New("max_backups: 0 (keep all) to 1000")
	}
	marker := r.join("/etc/config/NoCronBackup")
	if s.Enabled {
		if err := remove(marker); err != nil {
			return err
		}
	} else if err := touch(marker, 0o644); err != nil {
		return err
	}
	if err := writeFileAtomic(r.join("/etc/config/CronBackupPath"), []byte(s.Path+"\n"), 0o644); err != nil {
		return err
	}
	return writeFileAtomic(r.join("/etc/config/CronBackupMaxBackups"), []byte(strconv.Itoa(s.MaxBackups)+"\n"), 0o644)
}

// RunCronBackup runs the scheduled backup now, with the configured path and limit.
func (r Root) RunCronBackup(ctx context.Context, run Runner) (string, error) {
	if run == nil {
		run = ExecRunner
	}
	out, err := run(ctx, r.join("/bin/cronBackup.sh"))
	if err != nil {
		return strings.TrimSpace(string(out)), fmt.Errorf("cronBackup.sh: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

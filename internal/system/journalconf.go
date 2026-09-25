package system

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hobbyquaker/occulited/internal/netmount"
	"github.com/hobbyquaker/occulited/internal/shares"
)

// ---- the journal's knobs (task 23, task 85) --------------------------------------------------
//
// /etc/config/journal on the userfs is read by /usr/libexec/occu/lite-journal-persist at boot
// (occu-persist.service): STORAGE decides where the journal lives, and the sizes become a
// journald drop-in in /run. The file is the maintainer's decision of 2026-09-07 made editable:
// RAM only on the SD-card products, the userfs on the VM and the containers, and every limit
// settable per box. Nothing here talks to journald directly; the script is re-run (a restart of
// the oneshot unit) and does what it does at boot.
//
// Task 85's three modes, with the userfs as the one target:
//
//   - STORAGE=ram: the journal stays in /run/log/journal, bounded by RUNTIME_MAX_USE, and is gone
//     at a reboot.
//   - STORAGE=ram-sync: journald writes RAM only (Storage=volatile), and occu-journal-sync.service
//     copies the closed journal files to /usr/local/var/log/journal every SYNC_INTERVAL (6h) and
//     when it stops - at shutdown, at a reboot, and at "Copy now", which restarts it. The userfs
//     directory is bind-mounted over /var/log/journal as in persistent, so journalctl reads the
//     copies and RAM together without being told two places.
//   - STORAGE=persistent: /usr/local/var/log/journal is bind-mounted over /var/log/journal and
//     journald writes there from then on.
//
// What a switch does, which the Journal panel says too:
//
//   - into persistent applies at once: the script bind-mounts and has journald flush.
//   - into ram-sync applies at once: the script bind-mounts, restarts journald into RAM and starts
//     the copies.
//   - into ram applies at the next reboot: a mounted journal is not unmounted. From ram-sync the
//     copies stop at once, with a last one.
//   - the sizes and the interval apply at once.
//
// A USB stick as the target (task 216, the maintainer's decision of 2026-09-24): TARGET=usb:<label>/<dir>,
// for ram-sync only. The stick is named by udev's ID_FS_LABEL (USBStick.LabelID), never by
// /media/usbN, whose number follows the plug order. journald stays in RAM and nothing is mounted
// over /var/log/journal: the fork's lite-journal-sync copies to the stick while it is plugged in
// (occu-usb-mount@'s start switches to it and copies at once, its stop makes a last copy before
// the unmount) and skips the copy without it, which is the journal-target warning. The copies are
// read from the stick with journalctl --file (JournalLog.sourceArgs). Persistent on a stick is
// refused: journald would write the removable stick itself and hold its files open when it is
// pulled.
//
// PERSIST=1|0 is the older key. Read: a valid STORAGE wins, otherwise PERSIST=1 is persistent and
// PERSIST=0 is ram. Written: kept in step with STORAGE, so an older image still finds what the box
// chose after a downgrade (ram-sync is PERSIST=0 there: RAM, which is what it runs in).

const journalConfig = "/etc/config/journal"

// journalStateDir is where the boot script and the copy leave their results: fallback (why the
// chosen mode could not be set up), sync.state (the last copy), sync.next (the next one, while the
// copy unit runs, on the wall clock) and sync.due (the same on the boot's clock, /proc/uptime).
const journalStateDir = "/run/occu-journal"

// JournalSyncUnit is the ram-sync copy; a restart of it is a copy now.
const JournalSyncUnit = "occu-journal-sync"

// The journal's storage modes and its targets: the userfs, or a USB stick (usb:<label>/<dir>).
const (
	JournalRAM        = "ram"
	JournalRAMSync    = "ram-sync"
	JournalPersistent = "persistent"
	JournalUserfs     = "userfs"
	JournalUSBPrefix  = "usb:"
	// JournalSharePrefix is task 228's network share: share:<name>/<dir>, ram-sync only, like a
	// stick - the copies go to /media/net/<name>/<dir> through its automount, nothing is mounted
	// over /var/log/journal, and a share that cannot be reached skips the copy (RAM keeps the
	// journal, the journal-target warning says so).
	JournalSharePrefix = "share:"
)

// journalUserfsDir is where ram-sync and persistent keep the journal on the userfs.
const journalUserfsDir = "/usr/local/var/log/journal"

// The defaults lite-journal-sync applies to an empty setting.
const (
	JournalDefaultSyncInterval = "6h"
	JournalDefaultTargetMaxUse = "64M"
)

// JournalConfig is the file plus what is in effect right now.
type JournalConfig struct {
	Storage        string `json:"storage"`          // "" = the product's default, "ram", "ram-sync", "persistent"
	Target         string `json:"target"`           // "userfs" or "usb:<label>/<dir>" (ram-sync only)
	RuntimeMaxUse  string `json:"runtime_max_use"`  // journald RuntimeMaxUse, the RAM limit; "" = the image's
	SystemMaxUse   string `json:"system_max_use"`   // journald SystemMaxUse, e.g. 32M; "" = journald's default
	SystemMaxFile  string `json:"system_max_file"`  // SystemMaxFileSize
	RateLimitBurst string `json:"rate_limit_burst"` // RateLimitBurst, a count; "" = default
	// ram-sync: how often the copy runs (30min, 6h, 1d; "" = 6h), and how much and how old the copies
	// on the userfs may get ("" = 64M, "" = no age limit)
	SyncInterval string `json:"sync_interval"`
	TargetMaxUse string `json:"target_max_use"`
	TargetMaxAge string `json:"target_max_age"`
	// TargetLabel and TargetDir are a USB stick target's two halves; TargetPath is where the copies
	// go now - the userfs directory, or the directory on the stick while it is plugged in ("" while
	// it is not).
	TargetLabel string `json:"target_label,omitempty"`
	// TargetShare is a network share target's name (task 228); TargetDir its directory there.
	TargetShare string `json:"target_share,omitempty"`
	TargetDir   string `json:"target_dir,omitempty"`
	TargetPath  string `json:"target_path,omitempty"`
	// Persist is deprecated: derived from Storage ("1" persistent, "0" ram and ram-sync, "" the
	// default). The API reads it from a PUT only when the body has no storage key at all.
	Persist string `json:"persist"`
	// what is in effect
	Platform          string `json:"platform"`           // PLATFORM= in /VERSION
	DefaultStorage    string `json:"default_storage"`    // what "" means on this product
	DefaultPersistent bool   `json:"default_persistent"` // DefaultStorage == persistent, for older clients
	// Persistent: /var/log/journal is mounted from the userfs now - in persistent and in ram-sync,
	// the two modes in which the journal holds earlier boots.
	Persistent bool `json:"persistent"`
	// Effective is where journald writes now: "ram" (nothing mounted), "ram-sync" (mounted, and
	// journald in RAM) or "persistent" (mounted, and journald on it).
	Effective string `json:"effective"`
	// RebootPending: the setting says RAM but the userfs is still mounted, which only a reboot
	// changes - journald still writing it (from persistent) or the copies still readable (from
	// ram-sync).
	RebootPending bool `json:"reboot_pending"`
	// Fallback is the boot script's reason when the chosen ram-sync or persistent could not be set
	// up and the journal stayed in RAM; "" otherwise.
	Fallback string `json:"fallback,omitempty"`
	// The figures are null when they could not be measured (a directory that exists but cannot be
	// read, a failed statfs) and 0 when there is nothing: a missing journal directory is an empty
	// one, and a free space of 0 would read as a full disk.
	RAMUsage    *int64 `json:"ram_usage"`    // bytes of the files under /run/log/journal
	TargetUsage *int64 `json:"target_usage"` // bytes of the journal on the target (null: a stick not plugged in)
	TargetFree  *int64 `json:"target_free"`  // bytes free on the target's filesystem
	TargetOK    bool   `json:"target_ok"`    // the target can take the journal: the userfs mounted and writable, the stick plugged in and writable
	// TargetUnreadable (B-223): the copies are written on the share (root writes them), and the
	// system's own user, which reads them for the Log page, cannot - a root-squashed NFS export.
	// From a look while the share is mounted, or the last write test of the target.
	TargetUnreadable bool   `json:"target_unreadable,omitempty"`
	Usage            string `json:"usage,omitempty"` // journalctl --disk-usage, filled by the caller
	// ram-sync's copies: the last one (null before the first of this boot), its result "ok" or
	// "failed", the files it copied and why it failed; the next one while the copy unit runs.
	LastSync       *time.Time `json:"last_sync"`
	LastSyncResult string     `json:"last_sync_result,omitempty"`
	LastSyncCopied int        `json:"last_sync_copied"`
	LastSyncError  string     `json:"last_sync_error,omitempty"`
	// LastSyncReason is why the last copy ran (task 85, D-67): "interval", "early" (the RAM journal
	// at 80 % of its limit), "shutdown", "manual" (Copy now), "plug" (the stick came, task 216) or
	// "unplug" (the last copy before its unmount); "" when the script did not say
	LastSyncReason string     `json:"last_sync_reason,omitempty"`
	NextSync       *time.Time `json:"next_sync"`
}

// journalSyncStateName is the state lite-journal-sync also leaves beside the copies, so the last
// copy of the previous boot - usually the one at its shutdown - is known after /run was emptied.
const journalSyncStateName = ".occu-sync.state"

var (
	journalSizeRe  = regexp.MustCompile(`^[0-9]{1,9}[KMGT]?$`)
	journalCountRe = regexp.MustCompile(`^[0-9]{1,9}$`)
	// SYNC_INTERVAL as lite-journal-sync reads it: minutes, hours or days
	journalIntervalRe = regexp.MustCompile(`^([1-9][0-9]{0,4})(min|h|d)$`)
	// TARGET_MAX_AGE: a span journalctl --vacuum-time takes
	journalAgeRe = regexp.MustCompile(`^[1-9][0-9]{0,4}(h|d|w)$`)
	// a stick's label as udev's ID_FS_LABEL has it: letters, digits (any script), #+-.:=@_ - which is
	// also safe unquoted in the file the boot script sources
	journalLabelRe = regexp.MustCompile(`^[\p{L}\p{N}#+.:=@_-]{1,64}$`)
	// one directory of the stick target's path
	journalDirSegRe = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]{0,63}$`)
)

// ParseJournalUSBTarget splits usb:<label>/<dir>; ok is false for anything else, a label udev would
// not write, or a directory that climbs out (.., an empty or hidden part, more than four levels).
func ParseJournalUSBTarget(t string) (label, dir string, ok bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(t), JournalUSBPrefix)
	if !found {
		return "", "", false
	}
	label, dir, found = strings.Cut(rest, "/")
	if !found || !journalLabelRe.MatchString(label) || dir == "" {
		return "", "", false
	}
	parts := strings.Split(dir, "/")
	if len(parts) > 4 {
		return "", "", false
	}
	for _, p := range parts {
		if !journalDirSegRe.MatchString(p) {
			return "", "", false
		}
	}
	return label, dir, true
}

// ParseJournalShareTarget splits share:<name>/<dir> (task 228); ok is false for anything else: a
// name that is not a share's, a directory that climbs out or is deeper than four levels.
func ParseJournalShareTarget(t string) (name, dir string, ok bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(t), JournalSharePrefix)
	if !found {
		return "", "", false
	}
	name, dir, found = strings.Cut(rest, "/")
	if !found || !netmount.ValidID(name) || dir == "" {
		return "", "", false
	}
	parts := strings.Split(dir, "/")
	if len(parts) > 4 {
		return "", "", false
	}
	for _, p := range parts {
		if !journalDirSegRe.MatchString(p) {
			return "", "", false
		}
	}
	return name, dir, true
}

// journalShareProbe keeps the reads of a journal share's copies bounded: a share whose server is
// gone answers stale after the probe's deadline instead of hanging the Journal panel.
var journalShareProbe shares.Prober

// journalShareMounted: the share is mounted now (the mount table only, which neither mounts an idle
// share nor hangs on a dead one), and read-only or not.
func (r Root) journalShareMounted(mountinfo, name string) (mounted, readOnly bool) {
	mi, ok := netmount.ParseMountinfo(strings.NewReader(mountinfo), netmount.Base+"/"+name)
	if !ok {
		return false, false
	}
	return true, hasMountOption(mi.Options, "ro")
}

// journalDefaultStorage is what an empty STORAGE means, as lite-journal-persist decides it: the VM
// and the LXC container products live on the host's disk; everything else is an SD card (or an
// eMMC), whose biggest writer journald would be.
func journalDefaultStorage(platform string) string {
	switch platform {
	case "ova", "lxc":
		return JournalPersistent
	}
	return JournalRAM
}

// JournalStorageFromPersist maps the legacy persist value of a PUT onto a storage mode.
func JournalStorageFromPersist(p string) (string, error) {
	switch strings.TrimSpace(p) {
	case "":
		return "", nil
	case "1":
		return JournalPersistent, nil
	case "0":
		return JournalRAM, nil
	}
	return "", fmt.Errorf("persist is 1, 0 or empty for the product's default")
}

// journalPersistFromStorage is the PERSIST value that goes with a storage mode.
func journalPersistFromStorage(s string) string {
	switch s {
	case JournalPersistent:
		return "1"
	case JournalRAM, JournalRAMSync:
		return "0"
	}
	return ""
}

// JournalWanted is the mode the setting asks for: the storage, or the product's default.
func (c JournalConfig) JournalWanted() string {
	if c.Storage == "" {
		return c.DefaultStorage
	}
	return c.Storage
}

// ReadJournalConfig reads the file, looks at the mount table and measures the journal; the usage
// line is the API's (it needs journalctl, which runs unprivileged as the daemon's own user).
func (r Root) ReadJournalConfig() JournalConfig { return r.readJournalConfig(true) }

// ReadJournalSetting is ReadJournalConfig without the figures of a share target (its usage and
// free space): for the Status page's warnings and the other readers that run on a timer or want
// the setting only. Measuring a share walks its mount point, which resets the automount's idle
// timer - a timer that did so kept the share mounted for good (B-215).
func (r Root) ReadJournalSetting() JournalConfig { return r.readJournalConfig(false) }

func (r Root) readJournalConfig(shareFigures bool) JournalConfig {
	c := JournalConfig{Target: JournalUserfs}
	var storage, persist, target string
	for k, v := range parseSyslogConfig(readFile(r.join(journalConfig))) {
		switch k {
		case "STORAGE":
			storage = v
		case "PERSIST":
			persist = v
		case "TARGET":
			target = v
		case "RUNTIME_MAX_USE":
			c.RuntimeMaxUse = v
		case "SYSTEM_MAX_USE":
			c.SystemMaxUse = v
		case "SYSTEM_MAX_FILE":
			c.SystemMaxFile = v
		case "RATE_LIMIT_BURST":
			c.RateLimitBurst = v
		case "SYNC_INTERVAL":
			c.SyncInterval = v
		case "TARGET_MAX_USE":
			c.TargetMaxUse = v
		case "TARGET_MAX_AGE":
			c.TargetMaxAge = v
		}
	}
	switch storage {
	case JournalRAM, JournalRAMSync, JournalPersistent:
		c.Storage = storage
	default:
		// no STORAGE, or one this version does not know: the older key decides
		c.Storage, _ = JournalStorageFromPersist(persist)
	}
	c.Persist = journalPersistFromStorage(c.Storage)
	c.Platform = r.ReadVersion().Platform
	c.DefaultStorage = journalDefaultStorage(c.Platform)
	c.DefaultPersistent = c.DefaultStorage == JournalPersistent
	var stick *USBStick
	if label, dir, ok := ParseJournalUSBTarget(target); ok {
		c.Target, c.TargetLabel, c.TargetDir = JournalUSBPrefix+label+"/"+dir, label, dir
		if s, found := r.USBStickByLabel(label); found {
			stick = &s
		}
	} else if name, dir, ok := ParseJournalShareTarget(target); ok {
		c.Target, c.TargetShare, c.TargetDir = JournalSharePrefix+name+"/"+dir, name, dir
	} else if t := strings.TrimSpace(target); t != "" && t != JournalUserfs {
		// a value the boot script does not take either (it keeps the journal in RAM): shown as it is
		c.Target = t
	}
	mountinfo := readFile(r.join("/proc/self/mountinfo"))
	c.Persistent = journalMounted(mountinfo)
	// journald removes the runtime journal's files once it has flushed onto the mount, but not
	// /run/log/journal itself (B-138): the files decide, not the directory - an empty one read as
	// ram-sync on every persistent box. In ram-sync journald keeps writing there.
	inRAM := runtimeJournalLeft(r.join("/run/log/journal"))
	fallbackNow := strings.TrimSpace(readFile(r.join(journalStateDir + "/fallback")))
	switch {
	case c.TargetShare != "" && inRAM:
		// a share: RAM with copies, unless the last copy found it unreachable (the script's
		// fallback, cleared by the next copy that reaches it)
		c.Effective = JournalRAM
		if c.JournalWanted() == JournalRAMSync && fallbackNow == "" {
			c.Effective = JournalRAMSync
		}
	case c.TargetLabel != "" && inRAM:
		// a stick: RAM with copies while it is plugged in; a userfs journal still mounted from an
		// earlier setting changes nothing about that
		c.Effective = JournalRAM
		if stick != nil && c.JournalWanted() == JournalRAMSync {
			c.Effective = JournalRAMSync
		}
	case !c.Persistent:
		c.Effective = JournalRAM
	case inRAM:
		c.Effective = JournalRAMSync
	default:
		c.Effective = JournalPersistent
	}
	want := c.JournalWanted()
	// a switch to persistent or ram-sync is mounted by the script at once; one to RAM waits for the
	// reboot
	c.RebootPending = want == JournalRAM && c.Persistent
	if want != JournalRAM && c.Effective != want && !(c.TargetLabel == "" && c.TargetShare == "" && c.Persistent) {
		// the script writes the file only when it tried and failed - or, for a stick, while the
		// stick is not plugged in; before it ran there is none
		c.Fallback = strings.TrimSpace(readFile(r.join(journalStateDir + "/fallback")))
	}
	if n, ok := dirUsage(r.join("/run/log/journal")); ok {
		c.RAMUsage = &n
	}
	switch {
	case c.TargetShare != "":
		c.TargetPath = netmount.Base + "/" + c.TargetShare + "/" + c.TargetDir
		mounted, ro := r.journalShareMounted(mountinfo, c.TargetShare)
		c.TargetOK = !ro
		if mounted && shareFigures {
			// the figures only while it is mounted anyway, and through the probe
			var used, free int64
			var okUsed, okFree bool
			var unreadable bool
			err := journalShareProbe.Do("journal:"+c.TargetShare, func() error {
				used, okUsed = dirUsage(r.join(c.TargetPath))
				free, okFree = freeBytes(r.join(netmount.Base + "/" + c.TargetShare))
				unreadable = journalReadBack(r.join(c.TargetPath)) != nil
				return nil
			})
			c.TargetUnreadable = err == nil && unreadable
			// read only when the probe finished: one that timed out may still write them
			if err == nil && okUsed {
				c.TargetUsage = &used
			}
			if err == nil && okFree {
				c.TargetFree = &free
			}
		}
	case c.TargetLabel != "":
		if stick != nil {
			c.TargetPath = stick.Mount + "/" + c.TargetDir
			if n, ok := dirUsage(r.join(c.TargetPath)); ok {
				c.TargetUsage = &n
			}
			free := stick.Free
			c.TargetFree = &free
			c.TargetOK = !stick.ReadOnly
		}
	default:
		c.TargetPath = journalUserfsDir
		dir := journalUserfsDir
		if c.Persistent {
			dir = "/var/log/journal"
		}
		if n, ok := dirUsage(r.join(dir)); ok {
			c.TargetUsage = &n
		}
		if n, ok := freeBytes(r.join("/usr/local")); ok {
			c.TargetFree = &n
		}
		c.TargetOK = r.journalUserfsOK(mountinfo)
	}
	r.readJournalSync(&c)
	return c
}

// runtimeJournalLeft: a journal file is in RAM, as lite-journal-persist's wait for the flush asks.
func runtimeJournalLeft(dir string) bool {
	for _, pattern := range []string{"/*/*.journal", "/*.journal"} {
		if m, _ := filepath.Glob(dir + pattern); len(m) > 0 {
			return true
		}
	}
	return false
}

// JournalStickDir is where the journal's copies on a USB stick are now: ram-sync with a stick
// target and the stick plugged in; "" otherwise. The Log page reads them from there (sourceArgs).
func (r Root) JournalStickDir() string {
	var storage, persist, target string
	for k, v := range parseSyslogConfig(readFile(r.join(journalConfig))) {
		switch k {
		case "STORAGE":
			storage = v
		case "PERSIST":
			persist = v
		case "TARGET":
			target = v
		}
	}
	label, dir, ok := ParseJournalUSBTarget(target)
	share, sdir, isShare := ParseJournalShareTarget(target)
	if !ok && !isShare {
		return ""
	}
	switch storage {
	case JournalRAM, JournalRAMSync, JournalPersistent:
	default:
		storage, _ = JournalStorageFromPersist(persist)
		if storage == "" {
			storage = journalDefaultStorage(r.ReadVersion().Platform)
		}
	}
	if storage != JournalRAMSync {
		return ""
	}
	if isShare {
		// the copies on a share are read while it is mounted; an idle share is not mounted for a
		// read of the Log page (the next copy mounts it)
		if mounted, _ := r.journalShareMounted(readFile(r.join("/proc/self/mountinfo")), share); mounted {
			return netmount.Base + "/" + share + "/" + sdir
		}
		return ""
	}
	s, found := r.USBStickByLabel(label)
	if !found {
		return ""
	}
	return s.Mount + "/" + dir
}

// journalReadBack is shares.ReadBack; a variable for the tests, which run as a user a chmod does
// not stop when it is root.
var journalReadBack = shares.ReadBack

// JournalCopiesUnreadable (B-223): the journal's copies are on a share or a stick now (ram-sync,
// mounted) and the system's own user cannot read them - on an NFS export that maps root to nobody
// the folders root made belong to nobody. The Log page then says so instead of showing the RAM
// journal alone. "" when there are no such copies or they can be read.
func (r Root) JournalCopiesUnreadable() (dir string, err error) {
	if r == "" {
		return "", nil // as sourceArgs: no root, no copies read
	}
	dir = r.JournalStickDir()
	if dir == "" {
		return "", nil
	}
	// through the probe: a share that hangs is not a permission error (and the read of the log
	// itself says what it found)
	var rerr error
	if perr := journalShareProbe.Do("journal-read:"+dir, func() error { rerr = journalReadBack(r.join(dir)); return nil }); perr == nil && rerr != nil {
		return dir, rerr
	}
	return "", nil
}

// readJournalSync reads what lite-journal-sync left: the last copy, and the next one while the unit
// runs - which is only in ram-sync. The last copy is this boot's, from /run; before the first copy of
// this boot it is the one the script left beside the copies on the userfs, the previous boot's last.
func (r Root) readJournalSync(c *JournalConfig) {
	st := parseSyslogConfig(readFile(r.join(journalStateDir + "/sync.state")))
	sec, err := strconv.ParseInt(st["LAST_SYNC"], 10, 64)
	if (err != nil || sec <= 0) && c.TargetPath != "" {
		st = parseSyslogConfig(readFile(r.join(c.TargetPath + "/" + journalSyncStateName)))
		sec, err = strconv.ParseInt(st["LAST_SYNC"], 10, 64)
	}
	if err == nil && sec > 0 {
		t := time.Unix(sec, 0).UTC()
		c.LastSync = &t
		c.LastSyncResult = st["LAST_RESULT"]
		c.LastSyncCopied, _ = strconv.Atoi(st["LAST_COPIED"])
		c.LastSyncError = st["LAST_ERROR"]
		switch reason := st["LAST_REASON"]; reason {
		case "interval", "early", "shutdown", "manual", "plug", "unplug":
			c.LastSyncReason = reason
		}
	}
	if c.Effective != JournalRAMSync {
		return
	}
	sec, err = strconv.ParseInt(strings.TrimSpace(readFile(r.join(journalStateDir+"/sync.next"))), 10, 64)
	if err != nil || sec <= 0 {
		return
	}
	t := time.Unix(sec, 0).UTC()
	// B-205: the script stamps sync.next on the wall clock once a minute, which is stale for up to
	// a minute after the clock steps (NTP after a boot without a real-time clock). sync.due is the
	// same time on the boot's clock, and the date is computed from it at every read.
	if due, err := strconv.ParseInt(strings.TrimSpace(readFile(r.join(journalStateDir+"/sync.due"))), 10, 64); err == nil && due > 0 {
		if up, ok := r.Uptime(); ok {
			t = time.Now().Add(time.Duration(due)*time.Second - up).Truncate(time.Second).UTC()
		}
	}
	c.NextSync = &t
}

// journalUserfsOK: can the userfs take the journal. On a box that is /usr/local mounted from a
// writable filesystem; on a development or test root there is no mount table worth the name, and
// the directory is enough.
// journalMounted: the script bind-mounts the userfs directory over /var/log/journal, and a mount
// there is the one reliable sign that the journal is on the userfs now, whatever the file says.
func journalMounted(mountinfo string) bool {
	for _, line := range strings.Split(mountinfo, "\n") {
		if f := strings.Fields(line); len(f) > 4 && f[4] == "/var/log/journal" {
			return true
		}
	}
	return false
}

// JournalPersistent tells whether the journal holds earlier boots now (task 93's boot selector: in
// RAM the journal holds this boot only; in ram-sync the copies hold the earlier ones, on the userfs
// or on a stick that is plugged in). The cheap half of ReadJournalConfig.
func (r Root) JournalPersistent() bool {
	return journalMounted(readFile(r.join("/proc/self/mountinfo"))) || r.JournalStickDir() != ""
}

func (r Root) journalUserfsOK(mountinfo string) bool {
	st, err := os.Stat(r.join("/usr/local"))
	if err != nil || !st.IsDir() {
		return false
	}
	if root := string(r); root != "" && root != "/" {
		return true
	}
	return userfsWritable(mountinfo, r.Container() != "")
}

// userfsWritable reads the mount that holds /usr/local out of a mountinfo table. The filesystem's
// own options decide whether it is writable, not the mount's: occulited's unit runs with
// ProtectSystem=strict, which remounts /usr/local read-only in the daemon's namespace while the
// boot script, outside it, writes the userfs as usual. A read-only filesystem (ext4 remounted ro
// after an error) is what this catches. On the SD card and the VM /usr/local has to be a mount of
// its own - without it the userfs did not come up and the directory is the root filesystem's; in
// a container the userfs is the container's own disk and needs no mount.
func userfsWritable(mountinfo string, container bool) bool {
	const p = "/usr/local"
	best, rw := "", false
	for _, line := range strings.Split(mountinfo, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		mp := unescapeMount(f[4])
		if mp != p && mp != "/" && !strings.HasPrefix(p, mp+"/") {
			continue
		}
		if len(mp) < len(best) {
			continue
		}
		// the fields after the "-" separator: fstype, source, super options
		super := ""
		for i := 5; i < len(f); i++ {
			if f[i] == "-" && i+3 < len(f) {
				super = f[i+3]
				break
			}
		}
		best, rw = mp, !hasMountOption(super, "ro")
	}
	if best == "" || !rw {
		return false
	}
	return best == p || container
}

func hasMountOption(opts, o string) bool {
	for _, x := range strings.Split(opts, ",") {
		if x == o {
			return true
		}
	}
	return false
}

// dirUsage sums what the regular files under dir take on disk - allocated blocks, as journalctl
// --disk-usage and journald's limits count them, not their preallocated lengths (allocatedBytes).
// A missing directory is 0; one that exists but cannot be listed is not a figure (false). A
// subdirectory that cannot be read is left out - the journal's directories are the systemd-journal
// group's, which the daemon is in.
func dirUsage(dir string) (int64, bool) {
	st, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, true
	}
	if err != nil || !st.IsDir() {
		return 0, false
	}
	var n int64
	ok := true
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir {
				ok = false
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += allocatedBytes(info)
			}
		}
		return nil
	})
	return n, ok
}

// SetJournalConfig writes the file. What follows - re-running the script - is the caller's,
// through the service manager: a switch to persistent or ram-sync is mounted at once, the sizes
// and the interval reach journald and the copy at once, and a switch to RAM only shows at the
// next boot - the script cannot unmount a journal that is in use, and does not try.
func (r Root) SetJournalConfig(c JournalConfig) (JournalConfig, error) {
	if err := checkJournalConfig(c); err != nil {
		return JournalConfig{}, err
	}
	storage := strings.TrimSpace(c.Storage)
	if _, _, stick := ParseJournalUSBTarget(c.Target); stick && storage == "" && journalDefaultStorage(r.ReadVersion().Platform) == JournalPersistent {
		return JournalConfig{}, fmt.Errorf("this product's default is persistent, which a USB stick cannot be: choose ram-sync (RAM, copied to the stick)")
	}
	if _, _, share := ParseJournalShareTarget(c.Target); share && storage == "" && journalDefaultStorage(r.ReadVersion().Platform) == JournalPersistent {
		return JournalConfig{}, fmt.Errorf("this product's default is persistent, which a network share cannot be: choose ram-sync (RAM, copied to the share)")
	}
	set := map[string]string{
		"STORAGE": storage,
		// PERSIST in step, for an older image after a downgrade; no line for the default
		"PERSIST":          journalPersistFromStorage(storage),
		"TARGET":           strings.TrimSpace(c.Target),
		"RUNTIME_MAX_USE":  strings.ToUpper(strings.TrimSpace(c.RuntimeMaxUse)),
		"SYSTEM_MAX_USE":   strings.ToUpper(strings.TrimSpace(c.SystemMaxUse)),
		"SYSTEM_MAX_FILE":  strings.ToUpper(strings.TrimSpace(c.SystemMaxFile)),
		"RATE_LIMIT_BURST": strings.TrimSpace(c.RateLimitBurst),
		"SYNC_INTERVAL":    strings.TrimSpace(c.SyncInterval),
		"TARGET_MAX_USE":   strings.ToUpper(strings.TrimSpace(c.TargetMaxUse)),
		"TARGET_MAX_AGE":   strings.TrimSpace(c.TargetMaxAge),
	}
	body := renderSyslogConfig(readFile(r.join(journalConfig)), set)
	// an empty value is "the default": leave no KEY= behind, the script tests -n
	var kept []string
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok && v == "" {
			if _, ours := set[strings.TrimSpace(k)]; ours {
				continue
			}
		}
		kept = append(kept, line)
	}
	out := strings.Join(kept, "\n")
	if out != "" {
		out += "\n"
	}
	if err := writeFileAtomic(r.join(journalConfig), []byte(out), 0o644); err != nil {
		return JournalConfig{}, err
	}
	return r.ReadJournalConfig(), nil
}

// checkJournalConfig validates the writable part. Persist is not looked at: it is derived, and
// the API maps a legacy one onto Storage before this.
func checkJournalConfig(c JournalConfig) error {
	switch strings.TrimSpace(c.Storage) {
	case "", JournalRAM, JournalRAMSync, JournalPersistent:
	default:
		return fmt.Errorf("storage is ram, ram-sync, persistent or empty for the product's default")
	}
	if t := strings.TrimSpace(c.Target); strings.HasPrefix(t, JournalSharePrefix) {
		if _, _, ok := ParseJournalShareTarget(t); !ok {
			return fmt.Errorf("target %q is not a network share as share:<name>/<directory> - the share's name, the directory up to four levels of letters, digits, . _ -", t)
		}
		if strings.TrimSpace(c.Storage) == JournalPersistent {
			return fmt.Errorf("persistent on a network share is not possible: journald would write the share itself and hang when its server does not answer - choose ram-sync, which copies to the share at the interval and at shutdown")
		}
	} else if t != "" && t != JournalUserfs {
		if _, _, ok := ParseJournalUSBTarget(t); !ok {
			return fmt.Errorf("target %q is neither userfs nor a USB stick as usb:<label>/<directory> - the label as udev has it (letters, digits, # + - . : = @ _), the directory up to four levels of letters, digits, . _ -", t)
		}
		if strings.TrimSpace(c.Storage) == JournalPersistent {
			return fmt.Errorf("persistent on a USB stick is not possible: journald would write the stick itself and keep its files open when it is pulled - choose ram-sync, which copies to the stick at the interval, at shutdown and before the stick is unmounted")
		}
	}
	for _, s := range []string{c.RuntimeMaxUse, c.SystemMaxUse, c.SystemMaxFile, c.TargetMaxUse} {
		if s = strings.ToUpper(strings.TrimSpace(s)); s != "" && !journalSizeRe.MatchString(s) {
			return fmt.Errorf("a size is a number with K, M, G or T, e.g. 32M")
		}
	}
	if s := strings.TrimSpace(c.RateLimitBurst); s != "" && !journalCountRe.MatchString(s) {
		return fmt.Errorf("the rate limit burst is a count of messages")
	}
	if s := strings.TrimSpace(c.SyncInterval); s != "" {
		secs, ok := JournalIntervalSeconds(s)
		if !ok {
			return fmt.Errorf("the copy interval is minutes, hours or days, e.g. 30min, 6h or 1d")
		}
		if secs < 15*60 || secs > 7*24*3600 {
			return fmt.Errorf("the copy interval is 15min to 7d")
		}
	}
	if s := strings.TrimSpace(c.TargetMaxAge); s != "" && !journalAgeRe.MatchString(s) {
		return fmt.Errorf("the age limit is hours, days or weeks, e.g. 30d")
	}
	return nil
}

// JournalIntervalSeconds reads a SYNC_INTERVAL: 30min, 6h, 1d.
func JournalIntervalSeconds(s string) (int64, bool) {
	m := journalIntervalRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	n, _ := strconv.ParseInt(m[1], 10, 64)
	unit := map[string]int64{"min": 60, "h": 3600, "d": 86400}[m[2]]
	return n * unit, true
}

// DiskUsage is journalctl's one line about the journal's size, or "".
func (j JournalLog) DiskUsage(ctx context.Context) string {
	var out []byte
	var err error
	args := append([]string{"--disk-usage"}, j.sourceArgs()...)
	if j.Run != nil {
		out, err = j.Run(ctx, "journalctl", args...)
	} else {
		out, err = exec.CommandContext(ctx, "journalctl", args...).Output()
	}
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

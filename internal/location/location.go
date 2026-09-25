// Package location is openccu-lite task 228's one way of naming where something is kept: a
// location id and a folder, never a raw mount path - so a USB stick in another port or a share
// mounted again still resolves.
//
//	userfs:<folder>          the system's own storage, /usr/local/<folder>
//	usb:<label>/<folder>     the USB stick with that filesystem label (udev's ID_FS_LABEL), wherever
//	                         it is mounted (/media/usb1…8)
//	share:<name>/<folder>    a network share of System → Storage, mounted at /media/net/<name>
//
// Which uses may take which location is decided here too (the maintainer, 2026-09-24): the
// journal's copies may go to the userfs, a stick or a share; the backups to the userfs, a stick or
// a share (SFTP is a target kind of their own, not a location); occulited's database only to the
// userfs or a stick - never a share: bbolt maps its file into memory and needs locking and fsync
// that NFS and SMB do not guarantee, so a share that drops would corrupt it or hang the daemon.
package location

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/hobbyquaker/occulited/internal/netmount"
)

// The kinds of location.
const (
	KindUserfs = "userfs"
	KindUSB    = "usb"
	KindShare  = "share"
)

// The uses that pick a location.
const (
	UseJournal = "journal"
	UseBackup  = "backup"
	UseStore   = "store"
)

// UserfsRoot is where a userfs folder is.
const UserfsRoot = "/usr/local"

// Location is a parsed location: the kind, the stick's label or the share's name, the folder.
type Location struct {
	Kind   string `json:"kind"`
	Name   string `json:"name,omitempty"` // the label (usb) or the share's name (share); "" for the userfs
	Folder string `json:"folder"`
}

// ID is the location without the folder: userfs, usb:<label>, share:<name>.
func (l Location) ID() string {
	if l.Kind == KindUserfs {
		return KindUserfs
	}
	return l.Kind + ":" + l.Name
}

// String is the stored form.
func (l Location) String() string {
	if l.Kind == KindUserfs {
		return KindUserfs + ":" + l.Folder
	}
	return l.ID() + "/" + l.Folder
}

var (
	// a stick's label as udev's ID_FS_LABEL has it: letters, digits (any script), #+-.:=@_ - also
	// safe unquoted in the files the fork's scripts source (the journal's TARGET)
	labelRe = regexp.MustCompile(`^[\p{L}\p{N}#+.:=@_-]{1,64}$`)
	// one folder segment: letters, digits, . _ -, not hidden
	segRe = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]{0,63}$`)
)

// MaxDepth is how many folder levels a location may name.
const MaxDepth = 4

// ErrInvalid wraps every parse error.
var ErrInvalid = errors.New("invalid location")

// ValidFolder checks a folder: up to MaxDepth segments of letters, digits, . _ -, none hidden, no
// "..". Empty is allowed only where allowEmpty says (a share's root).
func ValidFolder(f string, allowEmpty bool) bool {
	if f == "" {
		return allowEmpty
	}
	parts := strings.Split(f, "/")
	if len(parts) > MaxDepth {
		return false
	}
	for _, p := range parts {
		if !segRe.MatchString(p) || p == "." || p == ".." {
			return false
		}
	}
	return true
}

// Parse reads a stored location. A folder is required for a stick and the userfs, optional for a
// share (its root).
func Parse(s string) (Location, error) {
	s = strings.TrimSpace(s)
	kind, rest, ok := strings.Cut(s, ":")
	if !ok {
		return Location{}, fmt.Errorf("%w: %q is not kind:…", ErrInvalid, s)
	}
	switch kind {
	case KindUserfs:
		if !ValidFolder(rest, false) {
			return Location{}, fmt.Errorf("%w: userfs:<folder>, up to %d levels of letters, digits, . _ -", ErrInvalid, MaxDepth)
		}
		return Location{Kind: KindUserfs, Folder: rest}, nil
	case KindUSB:
		label, folder, _ := strings.Cut(rest, "/")
		if !labelRe.MatchString(label) || !ValidFolder(folder, false) {
			return Location{}, fmt.Errorf("%w: usb:<label>/<folder> - the label as udev has it (letters, digits, # + - . : = @ _), the folder up to %d levels of letters, digits, . _ -", ErrInvalid, MaxDepth)
		}
		return Location{Kind: KindUSB, Name: label, Folder: folder}, nil
	case KindShare:
		name, folder, _ := strings.Cut(rest, "/")
		if !netmount.ValidID(name) || !ValidFolder(folder, true) {
			return Location{}, fmt.Errorf("%w: share:<name>/<folder> - the share's name, the folder up to %d levels of letters, digits, . _ -", ErrInvalid, MaxDepth)
		}
		return Location{Kind: KindShare, Name: name, Folder: folder}, nil
	}
	return Location{}, fmt.Errorf("%w: the kind is userfs, usb or share, not %q", ErrInvalid, kind)
}

// Rule is what a use allows of a kind: whether, why not, and for the userfs the folders it may
// name (a prefix; "" = any) and the one it suggests.
type Rule struct {
	Allowed bool
	// Reason says why not (Allowed false), or what applies (Allowed true), in English; the UI has
	// its own words for the known ones by Code.
	Code   string
	Reason string
	// UserfsPrefix: a userfs folder must be this or below it; Fixed: it must be exactly this.
	UserfsPrefix string
	Fixed        bool
	// Default is the folder suggested for a new choice.
	Default string
}

// RuleFor answers what use may do with kind.
func RuleFor(use, kind string) Rule {
	switch use {
	case UseJournal:
		switch kind {
		case KindUserfs:
			return Rule{Allowed: true, UserfsPrefix: "var/log/journal", Fixed: true, Default: "var/log/journal"}
		case KindUSB, KindShare:
			return Rule{Allowed: true, Code: "ram-sync-only", Reason: "takes the copies only: the journal stays in RAM and is copied there (ram-sync)", Default: "journal"}
		}
	case UseBackup:
		switch kind {
		case KindUserfs:
			return Rule{Allowed: true, Code: "lost-with-system", Reason: "a backup on the system's own storage is lost with the system", UserfsPrefix: "backup", Default: "backup"}
		case KindUSB:
			return Rule{Allowed: true, Default: "backup"}
		case KindShare:
			return Rule{Allowed: true, Default: ""}
		}
	case UseStore:
		switch kind {
		case KindUserfs:
			return Rule{Allowed: true, UserfsPrefix: "etc/occulite", Default: "etc/occulite/data"}
		case KindUSB:
			// task 229: a snapshot of ram-sync's file, never the live file
			return Rule{Allowed: true, Code: "copy-only", Reason: "takes a copy only: the database stays on the userfs and a copy goes to the stick at every write (ram-sync)", Default: "occulited"}
		case KindShare:
			return Rule{Allowed: false, Code: "no-share-for-store", Reason: "the database maps its file into memory and needs file locking and fsync that NFS and SMB do not guarantee: a share that drops would corrupt it or hang the system"}
		}
	}
	return Rule{Allowed: false, Code: "unknown", Reason: "not a location for " + use}
}

// Check answers whether l may be used for use (the kind's rule and, for the userfs, the folder).
func Check(use string, l Location) error {
	r := RuleFor(use, l.Kind)
	if !r.Allowed {
		return fmt.Errorf("%w: %s", ErrInvalid, r.Reason)
	}
	if l.Kind == KindUserfs && r.UserfsPrefix != "" {
		if r.Fixed && l.Folder != r.UserfsPrefix {
			return fmt.Errorf("%w: the %s on the userfs is always %s", ErrInvalid, use, r.UserfsPrefix)
		}
		if l.Folder != r.UserfsPrefix && !strings.HasPrefix(l.Folder, r.UserfsPrefix+"/") {
			return fmt.Errorf("%w: a folder on the userfs for the %s is %s or below it", ErrInvalid, use, r.UserfsPrefix)
		}
	}
	return nil
}

// UserfsPath is a userfs folder's path.
func UserfsPath(folder string) string { return UserfsRoot + "/" + folder }

// SharePath is a share folder's path (the share's mount point for its root).
func SharePath(name, folder string) string {
	if folder == "" {
		return netmount.Base + "/" + name
	}
	return netmount.Base + "/" + name + "/" + folder
}

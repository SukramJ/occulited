package store

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// The file's location in the shape of task 228's location picker (the maintainer, 2026-09-24): a
// location id and a folder - userfs:<folder>, usb:<label>/<folder>, share:<name>/<folder> - never
// a raw mount path, so a later picker plugs in without a migration. Empty is the default,
// DefaultLocation.
//
// bbolt may live on local storage only: the userfs, or (task 229) a USB stick. Never on an SMB or
// NFS share - bbolt maps its file and needs locking and fsync semantics a share does not
// guarantee; a dropped share would corrupt the file or hang occulited.
const (
	// DefaultLocation is <state>/data on the userfs (/usr/local/etc/occulite/data).
	DefaultLocation = "userfs:etc/occulite/data"
	// UserfsRoot is where a userfs: folder is.
	UserfsRoot = "/usr/local"
	// userfsAllowed is the part of the userfs occulited's unit may write (ReadWritePaths).
	userfsAllowed = "etc/occulite"
)

// ValidateLocation checks a location as the setting takes it: empty, a userfs: folder inside the
// part of the userfs occulited may write, or a USB stick for the snapshot (task 229); a share never.
func ValidateLocation(loc string) error {
	loc = strings.TrimSpace(loc)
	if loc == "" {
		return nil
	}
	id, folder, ok := strings.Cut(loc, ":")
	if !ok {
		return fmt.Errorf("the location is a location id and a folder, e.g. %s", DefaultLocation)
	}
	switch id {
	case "share":
		return fmt.Errorf("the database cannot live on a network share: it maps its file and needs locking and fsync that SMB and NFS do not guarantee - choose the userfs")
	case "usb":
		_, _, err := ParseUSB(loc)
		return err
	case "userfs":
	default:
		return fmt.Errorf("unknown location %q: the database lives on the userfs", id)
	}
	if folder == "" || strings.HasPrefix(folder, "/") || strings.Contains(folder, "\\") {
		return fmt.Errorf("the folder is relative to the userfs, e.g. %s", strings.TrimPrefix(DefaultLocation, "userfs:"))
	}
	clean := path.Clean(folder)
	if clean != folder || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("the folder %q is not a plain path on the userfs", folder)
	}
	if clean != userfsAllowed && !strings.HasPrefix(clean, userfsAllowed+"/") {
		return fmt.Errorf("the folder must be inside %s, the part of the userfs the system service may write", userfsAllowed)
	}
	return nil
}

// USB stick (task 229, the maintainer's Q&A of 2026-09-25): a copy only. The live file stays on the
// userfs (the default place) and a consistent snapshot of it goes to the stick, found by its label,
// at every ram-sync write and at a clean stop; at the open a snapshot on the stick newer than the
// local file is loaded. No live bbolt file on a removable device - it maps the file and would be
// torn from under it when the stick is pulled.

var (
	usbLabelRe  = regexp.MustCompile(`^[\p{L}\p{N}#+.:=@_-]{1,64}$`)
	usbFolderRe = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]{0,63}$`)
)

// ParseUSB splits usb:<label>/<folder> (the label as udev's ID_FS_LABEL has it, the folder up to
// four levels of letters, digits, . _ -).
func ParseUSB(loc string) (label, folder string, err error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(loc), "usb:")
	if !ok {
		return "", "", fmt.Errorf("not a USB stick location")
	}
	label, folder, _ = strings.Cut(rest, "/")
	if !usbLabelRe.MatchString(label) {
		return "", "", fmt.Errorf("the stick is named by its label, as usb:<label>/<folder>")
	}
	parts := strings.Split(folder, "/")
	if folder == "" || len(parts) > 4 {
		return "", "", fmt.Errorf("the folder on the stick is one to four levels, e.g. usb:%s/occulited", label)
	}
	for _, p := range parts {
		if !usbFolderRe.MatchString(p) {
			return "", "", fmt.Errorf("a folder on the stick is letters, digits, . _ and -, not starting with a dot")
		}
	}
	return label, folder, nil
}

// IsUSB says whether loc is a USB stick location.
func IsUSB(loc string) bool { return strings.HasPrefix(strings.TrimSpace(loc), "usb:") }

// SnapshotName is the snapshot's file name in the stick's folder.
const SnapshotName = "occulited-store.db"

// FilePath is the database file for a location; empty (the default) is the file under the
// state directory, whatever that is (a development state directory included).
func FilePath(loc, stateDir string) string {
	loc = strings.TrimSpace(loc)
	if loc == "" || loc == DefaultLocation || IsUSB(loc) {
		// a stick takes a snapshot; the live file stays in the default place (task 229)
		return Path(stateDir)
	}
	_, folder, _ := strings.Cut(loc, ":")
	return filepath.Join(UserfsRoot, filepath.FromSlash(folder), FileName)
}

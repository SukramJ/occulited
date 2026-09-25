package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The writable extension directories of the image (D-66): /firmware/rftypes, where addons link
// the device descriptions rfd needs for devices the image does not know, is made writable at boot
// by the image's occu-extension-dirs.service (lite-extension-dirs) - an overlay whose upper layer
// is on the userfs, or, on a kernel without overlayfs, a copy of the image's files bound over the
// directory. The script records the image's names in <state>/image-names before the mount hides
// the lower layer; this reads that, the writable layer and the mount table, so the Interfaces page
// can say what is the image's, what addons added, and what shadows an image file. The reset is the
// script's (root, through the helper), followed by an rfd restart.

// RFTypesDir is rfd's device description directory (rfd.conf's Device Description Dir).
const RFTypesDir = "/firmware/rftypes"

// ExtensionStateDir is where lite-extension-dirs keeps a directory's layers, by its base name.
const ExtensionStateDir = "/usr/local/etc/config/extensions"

// ExtensionDirsTool is the image's script; its absence means an image from before the unit.
const ExtensionDirsTool = "/usr/libexec/occu/lite-extension-dirs"

// DeviceDescriptions is the state of /firmware/rftypes as the Interfaces page shows it.
type DeviceDescriptions struct {
	Path string `json:"path"`
	// Available: the image has the unit (the script is there); without it the directory is the
	// read-only image's and nothing below applies.
	Available bool `json:"available"`
	// Mode is how the directory is writable: "overlay", "copy" (a copy of the image's files on
	// the userfs, bound over it) or "none" (not mounted: an older image, or the unit failed).
	Mode string `json:"mode"`
	// Entries is what rfd sees: every entry of the directory as mounted.
	Entries int `json:"entries"`
	// Image is how many entries the image's directory had when the unit mounted it.
	Image int `json:"image"`
	// Added are entries in the writable layer with names the image does not have: the addons'.
	Added int `json:"added"`
	// Replaced are image names the writable layer shadows with a file or link of its own.
	Replaced int `json:"replaced"`
	// Removed are image names the writable layer hides (an overlay whiteout) or the copy lacks;
	// the next boot puts them back, the reset at once.
	Removed int `json:"removed"`
	// State is the layers' directory on the userfs.
	State string `json:"state"`
	// Restarted (the reset's answer only): rfd was restarted afterwards.
	Restarted bool `json:"restarted,omitempty"`
}

// DeviceDescriptions reads the state of RFTypesDir. Nothing needs root: the layers are root's but
// world-readable, the mount table is /proc/self/mountinfo.
func (r Root) DeviceDescriptions() DeviceDescriptions {
	return r.extensionDir(RFTypesDir)
}

func (r Root) extensionDir(dir string) DeviceDescriptions {
	state := filepath.Join(ExtensionStateDir, filepath.Base(dir))
	d := DeviceDescriptions{Path: dir, State: state, Mode: "none"}
	if _, err := os.Stat(r.join(ExtensionDirsTool)); err == nil {
		d.Available = true
	}
	switch t := mountedType(readFile(r.join("/proc/self/mountinfo")), dir); {
	case t == "overlay":
		d.Mode = "overlay"
	case t != "":
		d.Mode = "copy"
	}
	if entries, err := os.ReadDir(r.join(dir)); err == nil {
		d.Entries = len(entries)
	}
	names := map[string]bool{}
	for _, n := range strings.Split(readFile(r.join(filepath.Join(state, "image-names"))), "\n") {
		if n = strings.TrimSpace(n); n != "" {
			names[n] = true
		}
	}
	d.Image = len(names)
	if len(names) == 0 {
		return d
	}
	layer, upper := r.extensionLayer(state, d.Mode)
	if layer == "" {
		return d
	}
	entries, err := os.ReadDir(layer)
	if err != nil {
		return d
	}
	present := map[string]bool{}
	for _, e := range entries {
		present[e.Name()] = true
		if !names[e.Name()] {
			d.Added++
			continue
		}
		fi, lerr := os.Lstat(filepath.Join(layer, e.Name()))
		if lerr != nil {
			continue
		}
		if upper {
			if fi.Mode()&os.ModeCharDevice != 0 {
				d.Removed++ // a whiteout hides the image's file
			} else {
				d.Replaced++
			}
		} else if fi.Mode()&os.ModeSymlink != 0 {
			d.Replaced++ // in the copy only a link stands for an addon's replacement
		}
	}
	if !upper {
		for n := range names {
			if !present[n] {
				d.Removed++
			}
		}
	}
	return d
}

// extensionLayer is the writable layer to read - the overlay's upper or the copy - as the
// script chooses it: whichever exists alone, else what the mount says. upper says which kind.
func (r Root) extensionLayer(state, mode string) (layer string, upper bool) {
	up, cp := r.join(filepath.Join(state, "upper")), r.join(filepath.Join(state, "copy"))
	_, upErr := os.Stat(up)
	_, cpErr := os.Stat(cp)
	switch {
	case upErr == nil && cpErr != nil:
		return up, true
	case cpErr == nil && upErr != nil:
		return cp, false
	case upErr != nil && cpErr != nil:
		return "", false
	case mode == "overlay":
		return up, true
	default:
		return cp, false
	}
}

// mountedType is the file system type of the topmost mount on dir in a /proc/self/mountinfo
// text, "" when nothing is mounted there. Field 5 is the mount point; the type follows the "-".
func mountedType(mountinfo, dir string) string {
	t := ""
	for _, line := range strings.Split(mountinfo, "\n") {
		f := strings.Fields(line)
		if len(f) < 7 || f[4] != dir {
			continue
		}
		for i := 6; i < len(f)-1; i++ {
			if f[i] == "-" {
				t = f[i+1]
				break
			}
		}
	}
	return t
}

// ResetDeviceDescriptions makes the image's files win again in RFTypesDir's writable layer
// (`lite-extension-dirs reset`, root through the helper: unmount, drop the whiteouts and the
// replacements of image names, keep the additions, mount again) and restarts rfd when it runs,
// because rfd reads the directory at its start only. The answer is the state afterwards.
func ResetDeviceDescriptions(ctx context.Context, root Root, svc ServiceManager) (DeviceDescriptions, error) {
	tool := root.join(ExtensionDirsTool)
	if _, err := os.Stat(tool); err != nil {
		return root.DeviceDescriptions(), errors.New("this image has no writable device descriptions (occu-extension-dirs.service)")
	}
	res, err := Priv.Run(ctx, tool, []string{"reset", RFTypesDir}, nil)
	if err != nil {
		return root.DeviceDescriptions(), fmt.Errorf("reset: %w", err)
	}
	if res.Exit != 0 {
		// the script's own words (an unmount refused as busy, say) are the message
		return root.DeviceDescriptions(), fmt.Errorf("reset: %s (exit %d)", strings.TrimSpace(string(res.Combined())), res.Exit)
	}
	d := root.DeviceDescriptions()
	if svc != nil {
		if list, lerr := svc.List(); lerr == nil {
			for _, s := range list {
				if s.ID == "rfd" && s.Running {
					if _, cerr := svc.Control(ctx, "rfd", "restart"); cerr != nil {
						return d, fmt.Errorf("reset done, rfd restart: %w", cerr)
					}
					d.Restarted = true
				}
			}
		}
	}
	return d, nil
}

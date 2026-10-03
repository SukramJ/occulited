package system

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// AddonsFingerprint changes whenever what the addon list and the shell's menu are made of may have
// changed (openccu-lite B-297): the rc.d entries (an install, an uninstall, the executable bit that
// is enabled/disabled), the nav.d drop-ins, the addons' lighttpd drop-ins (a frontend) and
// hm_addons.cfg (the names and settings pages addons register). Only names, modes, sizes and times
// are read - no rc.d script is run - so it is cheap enough to look at every few seconds.
func (r Root) AddonsFingerprint() string {
	h := sha256.New()
	for _, dir := range []string{"/usr/local/etc/config/rc.d", "/usr/local/etc/config/nav.d", lighttpdDropinDir} {
		entries, _ := os.ReadDir(r.join(dir))
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		fmt.Fprintf(h, "%s\n", dir)
		for _, n := range names {
			p := filepath.Join(r.join(dir), n)
			fmt.Fprintf(h, "%s %s\n", n, statLine(p, os.Lstat))
			// a drop-in is usually a link into the addon's own directory: its target too
			fmt.Fprintf(h, " %s\n", statLine(p, os.Stat))
		}
	}
	fmt.Fprintf(h, "hm_addons.cfg %s\n", statLine(r.join("/usr/local/etc/config/hm_addons.cfg"), os.Stat))
	return hex.EncodeToString(h.Sum(nil))
}

func statLine(p string, stat func(string) (os.FileInfo, error)) string {
	st, err := stat(p)
	if err != nil {
		return "-"
	}
	return fmt.Sprintf("%v %d %d", st.Mode(), st.Size(), st.ModTime().UnixNano())
}

package radio

import (
	"regexp"
	"strconv"
	"strings"
)

// rfd.conf is a user file: upstream's S61rfd edits it in place at every start so that its local
// module section matches the hardware, and lite's lite-rfd-listen puts rfd on the loopback. This
// file does the same edits on a string, in the same order, with the deviations marked.

var (
	listenIPLine   = regexp.MustCompile(`^[[:space:]]*Listen IP[[:space:]]*=`)
	listenIPLoop   = regexp.MustCompile(`^[[:space:]]*Listen IP[[:space:]]*=[[:space:]]*127\.0\.0\.1[[:space:]]*$`)
	listenPortLine = regexp.MustCompile(`^[[:space:]]*Listen Port[[:space:]]*=`)
	listenPortAny  = regexp.MustCompile(`^Listen\s+Port\s*=.*$`)
	comPortLine    = regexp.MustCompile(`^ComPortFile =.*$`)
	accessLine     = regexp.MustCompile(`^#*AccessFile =.*$`)
	resetLine      = regexp.MustCompile(`^#*ResetFile =.*$`)
	blankLine      = regexp.MustCompile(`^\s*$`)
	commentedBlank = regexp.MustCompile(`^#\s*$`)
	usbNameLine    = regexp.MustCompile(`^Name.*HM-CFG-USB`)
	// usbCommentedName is the adapter's section switched off (deviation 5)
	usbCommentedName = regexp.MustCompile(`^#Name.*HM-CFG-USB`)
	interfaceHdr     = regexp.MustCompile(`^\[Interface ([0-9]+)\]`)
)

// LoopbackListen is lite-rfd-listen: the Listen IP line set to the loopback, in place, only when
// the file needs it. A file without one gets it before Listen Port, or at the top.
func LoopbackListen(conf string) string {
	lines := strings.Split(conf, "\n")
	for _, l := range lines {
		if listenIPLoop.MatchString(l) {
			return conf
		}
	}
	const line = "Listen IP = 127.0.0.1"
	mode := "top"
	for _, l := range lines {
		if listenIPLine.MatchString(l) {
			mode = "replace"
			break
		}
		if listenPortLine.MatchString(l) {
			mode = "beforeport"
		}
	}
	var out []string
	done := false
	for i, l := range lines {
		switch {
		case mode == "top" && i == 0:
			out = append(out, line, l)
		case mode == "replace" && listenIPLine.MatchString(l):
			if !done {
				out = append(out, line)
			}
			done = true
		case mode == "beforeport" && !done && listenPortLine.MatchString(l):
			out = append(out, line, l)
			done = true
		default:
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// collapseBlankLines is sed '/^$/N;/^\n$/D': a run of empty lines becomes one.
func collapseBlankLines(conf string) string {
	lines := strings.Split(conf, "\n")
	var out []string
	for i, l := range lines {
		if l == "" && i > 0 && lines[i-1] == "" && i < len(lines)-1 {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// commentBlock comments the lines from the first line matching start up to and including the
// first blank line after it (sed '/start/,/^\s*$/ s/^/#/'), or to the end.
func commentBlock(lines []string, start *regexp.Regexp) {
	in := false
	for i, l := range lines {
		if !in && start.MatchString(l) {
			in = true
		}
		if in {
			lines[i] = "#" + l
			if blankLine.MatchString(l) {
				in = false
			}
		}
	}
}

// uncommentBlock strips one # from the lines of a commented block: from the header up to and
// including the first "#" line that is a commented blank (sed '/^#\[Interface 0\]/,/^#\s*$/ s/^#//').
func uncommentBlock(lines []string, start *regexp.Regexp) {
	in := false
	for i, l := range lines {
		if !in && start.MatchString(l) {
			in = true
		}
		if in {
			end := commentedBlank.MatchString(l)
			lines[i] = strings.TrimPrefix(l, "#")
			if end {
				in = false
			}
		}
	}
}

// templateSection is sed -n '/^\[Interface 0\]/,/^\s*$/p' on the template: the header through
// the first blank line, or to the end.
func templateSection(tmpl string) string {
	var out []string
	in := false
	for _, l := range strings.Split(strings.TrimSuffix(tmpl, "\n"), "\n") {
		if !in && strings.HasPrefix(l, "[Interface 0]") {
			in = true
		}
		if in {
			out = append(out, l)
			if blankLine.MatchString(l) {
				break
			}
		}
	}
	return strings.Join(out, "\n")
}

// EditRFDConf is S61rfd's init on the userfs file (lite-rfd-listen first): the template when the
// file is missing, the key-file repair, the blank-line cleanup, the local section switched on or
// off, its three parameters, the "Improved Coprocessor Initialization" line, and the HM-CFG-USB-2
// section added or switched off. Returns the file, whether the local section is active and
// whether a USB adapter section is.
func EditRFDConf(conf string, exists bool, tmpl string, hmrf *Role, mmdBidcos bool) (string, bool, bool) {
	if !exists {
		conf = tmpl
	} else {
		conf = LoopbackListen(conf)
	}
	// deviation 7 (D-96): upstream resets a file that names /etc/config/rfd/keys to the template,
	// which loses every LAN gateway section; the one line is corrected instead
	if strings.Contains(conf, "/etc/config/rfd/keys") {
		conf = strings.ReplaceAll(conf, "/etc/config/rfd/keys", "/etc/config/keys")
	}
	conf = collapseBlankLines(conf)
	// the lines without the file's final newline: sed sees no line after it
	hadNL := strings.HasSuffix(conf, "\n")
	lines := strings.Split(strings.TrimSuffix(conf, "\n"), "\n")
	realModule := hmrf != nil && (hmrf.Hardware == "HM-MOD-RPI-PCB" || hmrf.Hardware == "RPI-RF-MOD" || hmrf.Hardware == "HMIP-RFUSB")
	local := false
	if realModule && mmdBidcos {
		local = true
		hasCommented, hasActive := false, false
		for _, l := range lines {
			if strings.HasPrefix(l, "#[Interface 0]") {
				hasCommented = true
			}
			if strings.HasPrefix(l, "[Interface 0]") {
				hasActive = true
			}
		}
		switch {
		case hasCommented:
			uncommentBlock(lines, regexp.MustCompile(`^#\[Interface 0\]`))
		case !hasActive:
			// upstream appends the template's section to the file as it is (no separator)
			lines = append(lines, strings.Split(templateSection(tmpl), "\n")...)
		}
		for i, l := range lines {
			switch {
			case comPortLine.MatchString(l):
				lines[i] = "ComPortFile = /dev/mmd_bidcos"
			case accessLine.MatchString(l):
				lines[i] = "AccessFile = /dev/null"
			case resetLine.MatchString(l):
				lines[i] = "ResetFile = /dev/null"
			}
		}
		if !strings.Contains(strings.Join(lines, "\n"), "Improved Coprocessor Initialization") {
			for i, l := range lines {
				if strings.Contains(l, "[Interface 0]") {
					lines[i] = strings.Replace(l, "[Interface 0]", "Improved Coprocessor Initialization = true\n\n[Interface 0]", 1)
				}
			}
			lines = strings.Split(strings.Join(lines, "\n"), "\n")
		}
	} else {
		commentBlock(lines, regexp.MustCompile(`^\[Interface 0\]`))
	}

	usb := false
	hasUSBName := false
	for _, l := range lines {
		if usbNameLine.MatchString(l) {
			hasUSBName = true
		}
	}
	if hmrf != nil && hmrf.Hardware == "HM-CFG-USB-2" {
		usb = true
		if !hasUSBName {
			// deviation 4: the section takes the next free number (upstream's ${inum+1} always
			// wrote "[Interface 1]", a second section 1 beside a LAN gateway)
			n := 1
			for _, l := range lines {
				if m := interfaceHdr.FindStringSubmatch(l); m != nil {
					if v, _ := strconv.Atoi(m[1]); v+1 > n {
						n = v + 1
					}
				}
			}
			// deviation 14: the adapter's own section switched off earlier (deviation 5) is
			// switched on again, not appended a second time beside the commented one
			if !reactivateUSBSection(lines, hmrf.Serial, n) {
				lines = append(lines, "", "[Interface "+strconv.Itoa(n)+"]", "Type = USB Interface", "Name = HM-CFG-USB", "Serial Number = "+hmrf.Serial, "Encryption Key =")
			}
		}
	} else if hasUSBName {
		// deviation 5: the adapter's own section is switched off (upstream commented the last
		// section out, a LAN gateway after the adapter's)
		hdr := -1
		for i, l := range lines {
			if interfaceHdr.MatchString(l) {
				hdr = i
			}
			if usbNameLine.MatchString(l) {
				break
			}
		}
		if hdr >= 0 {
			for i := hdr; i < len(lines); i++ {
				lines[i] = "#" + lines[i]
				if blankLine.MatchString(lines[i][1:]) {
					break
				}
			}
		}
	} else {
		// no adapter, no section: a stray "usb" verdict cannot arise
		usb = false
	}
	if hasUSBName && (hmrf != nil && hmrf.Hardware == "HM-CFG-USB-2") {
		usb = true
	}
	out := strings.Join(lines, "\n")
	if hadNL || (hmrf != nil && hmrf.Hardware == "HM-CFG-USB-2" && !hasUSBName) {
		out += "\n"
	}
	return out, local, usb
}

// reactivateUSBSection uncomments the commented section of the HM-CFG-USB-2 with this serial,
// from its "#[Interface n]" line to the commented blank line after it. The section gets the
// number next when n is taken by an active section by now. False when there is none.
func reactivateUSBSection(lines []string, serial string, next int) bool {
	serialLine := regexp.MustCompile(`^#Serial Number\s*=\s*` + regexp.QuoteMeta(serial) + `\s*$`)
	hdrRe := regexp.MustCompile(`^#\[Interface ([0-9]+)\]`)
	active := map[string]bool{}
	for _, l := range lines {
		if m := interfaceHdr.FindStringSubmatch(l); m != nil {
			active[m[1]] = true
		}
	}
	for j, l := range lines {
		if !usbCommentedName.MatchString(l) {
			continue
		}
		h := -1
		for i := j; i >= 0; i-- {
			if hdrRe.MatchString(lines[i]) {
				h = i
				break
			}
		}
		if h < 0 {
			continue
		}
		end, ok := h, false
		for i := h; i < len(lines); i++ {
			if i > h && hdrRe.MatchString(lines[i]) {
				break
			}
			if serialLine.MatchString(lines[i]) {
				ok = true
			}
			end = i
			if i > h && commentedBlank.MatchString(lines[i]) {
				break
			}
		}
		if !ok {
			continue
		}
		for i := h; i <= end; i++ {
			lines[i] = strings.TrimPrefix(lines[i], "#")
		}
		if m := interfaceHdr.FindStringSubmatch(lines[h]); m != nil && active[m[1]] {
			lines[h] = "[Interface " + strconv.Itoa(next) + "]"
		}
		return true
	}
	return false
}

// VarRFDConf is /var/etc/rfd.conf: the userfs file with the port forced, and the loopback line
// carried over from /etc/config (EditRFDConf puts it there; B-164 keeps the render honest for a
// file that reached /etc/config another way).
func VarRFDConf(conf string) string {
	lines := strings.Split(LoopbackListen(conf), "\n")
	for i, l := range lines {
		if listenPortAny.MatchString(l) {
			lines[i] = "Listen Port = 32001"
		}
	}
	return strings.Join(lines, "\n")
}

// VarHS485DConf is /var/etc/hs485d.conf: the userfs file with the port forced and the daemon on
// the loopback.
//
// B-164: hs485d listened on 0.0.0.0:32000 where rfd listens on 127.0.0.1:32001, which D-29 does
// not allow - nothing but lighttpd faces the LAN. rfd gets its "Listen IP" from the image's
// template and from LoopbackListen; hs485d has no template at all (the file is written when the
// first wired gateway is added), so the render is where it belongs. Measured on the Charly on
// 2026-09-22: hs485d honours the line, exactly as rfd does, and its LAN gateway stays connected.
func VarHS485DConf(conf string) string {
	lines := strings.Split(LoopbackListen(conf), "\n")
	for i, l := range lines {
		if listenPortAny.MatchString(l) {
			lines[i] = "Listen Port = 32000"
		}
	}
	return strings.Join(lines, "\n")
}

package regaimport

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The ReGa database (/etc/config/homematic.regadom) is plain XML, declared ISO-8859-1 and actually
// mixed (see fixMojibake): one <obj> per DOM object (id, name, type), grouped in maps. What the
// metadata import needs:
//
//   <interfacemap> <ifc><obj>…<name>BidCos-RF</name></obj></ifc>          interface names
//   <devicemap>    <device><obj>… DEVDESC=[ADDRESS:"…",CHILDREN:{…}]</obj>  devices
//   <channelmap>   <channel><obj>… DEVDESC=[ADDRESS:"…:1",PARENT:"…"]</obj> channels
//   <hssdpmap>     <dp><obj><name>BidCos-RF.JEQ0230153:1.HUMIDITY</name>    datapoints - the only
//                                                                           place a channel's
//                                                                           interface *name* appears
//   <enummap>      <enum><obj>…<name>Rooms</name></obj><enel><oid>…</enel>  the Rooms/Functions
//                  <enum><obj>…<name>Wohnzimmer</name></obj><enel><oid>1575</oid>… containers and
//                                                                           the rooms/functions
//                                                                           with their channel ids
//
// D-35: this is read, never written.

var (
	devdescAddress = regexp.MustCompile(`ADDRESS:"([^"]*)"`)
	devdescParent  = regexp.MustCompile(`PARENT:"([^"]*)"`)
)

// latin1Reader turns ISO-8859-1 bytes into UTF-8 for encoding/xml.
type latin1Reader struct {
	r *bufio.Reader
}

func (l *latin1Reader) Read(p []byte) (int, error) {
	n := 0
	for n < len(p)-3 {
		b, err := l.r.ReadByte()
		if err != nil {
			if n > 0 {
				return n, nil
			}
			return 0, err
		}
		if b < 0x80 {
			p[n] = b
			n++
		} else {
			p[n] = 0xC0 | b>>6
			p[n+1] = 0x80 | b&0x3F
			n += 2
		}
	}
	return n, nil
}

type regaObj struct {
	kind    string // ifc, device, channel, dp, enum
	id      int
	name    string
	otype   int
	props   map[string]string
	members []int // enum: the enel oids
}

// ParseRegadom reads a regadom file and produces the same Dump the live script does.
func ParseRegadom(r io.Reader) (*Dump, error) {
	dec := xml.NewDecoder(r)
	dec.CharsetReader = func(charset string, in io.Reader) (io.Reader, error) {
		switch strings.ToLower(charset) {
		case "iso-8859-1", "latin1", "iso8859-1":
			return &latin1Reader{r: bufio.NewReader(in)}, nil
		}
		return in, nil
	}
	dec.Strict = false
	var (
		stack   []string
		objs    []regaObj
		cur     *regaObj
		text    strings.Builder
		prop    string
		inEnel  bool
		curKind string
	)
	containerOf := func() string {
		// the innermost of the elements that wrap an <obj> we care about
		for i := len(stack) - 1; i >= 0; i-- {
			switch stack[i] {
			case "ifc", "device", "channel", "dp", "enum", "user":
				return stack[i]
			}
		}
		return ""
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("regadom: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			stack = append(stack, t.Name.Local)
			text.Reset()
			switch t.Name.Local {
			case "obj":
				curKind = containerOf()
				if curKind != "" {
					objs = append(objs, regaObj{kind: curKind, props: map[string]string{}})
					cur = &objs[len(objs)-1]
				} else {
					cur = nil
				}
			case "enel":
				inEnel = true
			}
		case xml.CharData:
			text.Write(t)
		case xml.EndElement:
			name := t.Name.Local
			val := strings.TrimSpace(text.String())
			text.Reset()
			if cur != nil {
				switch name {
				case "id":
					if len(stack) >= 2 && stack[len(stack)-2] == "obj" {
						cur.id, _ = strconv.Atoi(val)
					}
				case "name":
					if len(stack) >= 2 && stack[len(stack)-2] == "obj" {
						cur.name = val
					}
				case "type":
					if len(stack) >= 2 && stack[len(stack)-2] == "obj" {
						cur.otype, _ = strconv.Atoi(val)
					}
				case "property":
					prop = val
				case "value":
					if prop != "" {
						cur.props[prop] = val
						prop = ""
					}
				case "oid":
					if inEnel && cur.kind == "enum" {
						if n, err := strconv.Atoi(val); err == nil {
							cur.members = append(cur.members, n)
						}
					}
				case "enel":
					inEnel = false
				case "ifc", "device", "channel", "dp", "enum", "user":
					cur = nil
				}
			}
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	return dumpFromObjs(objs)
}

// fixMojibake undoes the ISO-8859-1 decode for a string that was UTF-8 to begin with.
//
// The regadom declares `encoding="iso-8859-1"` and is **mixed**: eQ-3's own strings really are
// Latin-1 (a unit is the single byte 0xB0 for "°C"), while a name a user typed on a current
// firmware is written as UTF-8 into the same file. Decoding the whole stream as Latin-1 - which is
// what the declaration asks for, and what the units need - turns "Büro" into "BÃ¼ro". Measured on
// a real CCU3 database on 2026-09-07: it had thirteen rooms where the WebUI shows eleven, because
// "Büro" and "Küche" each existed twice, once in each encoding.
//
// The test is exact rather than heuristic in the direction that matters: only a string whose runes
// are all below U+0100 can have come out of a Latin-1 decode, and only if the bytes they stand for
// are valid UTF-8 *with a multi-byte sequence in them* was the source UTF-8. A genuinely Latin-1
// "°C" fails that test (a lone 0xB0 is not valid UTF-8) and is left alone, and so is anything
// ASCII.
func fixMojibake(s string) string {
	b := make([]byte, 0, len(s))
	high := false
	for _, r := range s {
		if r > 0xFF {
			return s // not something a Latin-1 decode produced
		}
		if r >= 0x80 {
			high = true
		}
		b = append(b, byte(r))
	}
	if !high || !utf8.Valid(b) || utf8.RuneCount(b) == len(b) {
		return s
	}
	return string(b)
}

// cleanName is what every name coming out of a CCU goes through: the encoding repair above, and
// then the control characters out. B-38 made a control character in a name a validation error,
// which is right for a name a person types here - but an import must not lose a whole CCU's
// names because one of them carries a stray tab that nobody here can go and fix. Strict on the
// way in through the API, forgiving on the way in from a foreign system.
func cleanName(s string) string {
	s = fixMojibake(s)
	if strings.IndexFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) < 0 {
		return s
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
}

func dumpFromObjs(objs []regaObj) (*Dump, error) {
	for i := range objs {
		objs[i].name = cleanName(objs[i].name)
	}
	d := &Dump{}
	ifaceOfAddr := map[string]string{} // channel address -> interface name
	known := map[string]bool{}
	for _, o := range objs {
		switch o.kind {
		case "ifc":
			if o.name != "" {
				known[o.name] = true
			}
		case "dp":
			// <interface>.<address>.<param>; the address holds the channel's ':'
			if i := strings.IndexByte(o.name, '.'); i > 0 {
				rest := o.name[i+1:]
				if j := strings.LastIndexByte(rest, '.'); j > 0 {
					ifaceOfAddr[rest[:j]] = o.name[:i]
				}
			}
		}
	}
	byID := map[int]*regaObj{}
	for i := range objs {
		byID[objs[i].id] = &objs[i]
	}
	deviceIface := map[string]string{} // device address -> interface, from its channels
	for _, o := range objs {
		if o.kind != "channel" {
			continue
		}
		addr := match(devdescAddress, o.props["DEVDESC"])
		parent := match(devdescParent, o.props["DEVDESC"])
		if iface := ifaceOfAddr[addr]; iface != "" && parent != "" {
			deviceIface[parent] = iface
		}
	}
	for _, o := range objs {
		switch o.kind {
		case "device":
			addr := match(devdescAddress, o.props["DEVDESC"])
			iface := deviceIface[addr]
			if addr == "" || iface == "" {
				continue
			}
			d.Objects = append(d.Objects, DumpObject{ID: o.id, Interface: iface, Address: addr, Name: o.name})
		case "channel":
			addr := match(devdescAddress, o.props["DEVDESC"])
			iface := ifaceOfAddr[addr]
			if iface == "" {
				if parent := match(devdescParent, o.props["DEVDESC"]); parent != "" {
					iface = deviceIface[parent]
				}
			}
			if addr == "" || iface == "" {
				continue
			}
			d.Objects = append(d.Objects, DumpObject{ID: o.id, Interface: iface, Address: addr, Name: o.name})
		}
	}
	// the containers named Rooms / Functions list the enum ids; each enum lists channel ids
	var roomIDs, funcIDs []int
	for _, o := range objs {
		if o.kind != "enum" {
			continue
		}
		switch o.name {
		case "Rooms", "Räume":
			roomIDs = append(roomIDs, o.members...)
		case "Functions", "Gewerke":
			funcIDs = append(funcIDs, o.members...)
		}
	}
	collect := func(ids []int) []DumpEnum {
		var out []DumpEnum
		for _, id := range ids {
			if o := byID[id]; o != nil && o.kind == "enum" {
				out = append(out, DumpEnum{ID: o.id, Name: o.name, Members: append([]int{}, o.members...)})
			}
		}
		return out
	}
	d.Rooms, d.Functions = collect(roomIDs), collect(funcIDs)
	// task 193: the favorites - the container named Favoriten lists the pages; a page named
	// _USER<id> is a user's own (the user object of that id gives the name), any other page is a
	// shared one under its own name; an empty page (the CCU's factory PC, PDA, Zentrale) is left
	users := map[int]string{}
	for _, o := range objs {
		if o.kind == "user" && o.name != "" {
			users[o.id] = o.name
		}
	}
	for _, o := range objs {
		if o.kind != "enum" || (o.name != "Favoriten" && o.name != "Favorites") {
			continue
		}
		for _, id := range o.members {
			p := byID[id]
			if p == nil || p.kind != "enum" || len(p.members) == 0 {
				continue
			}
			name := p.name
			if strings.HasPrefix(name, "_USER") {
				uid, err := strconv.Atoi(strings.TrimPrefix(name, "_USER"))
				if err != nil || users[uid] == "" {
					continue
				}
				name = users[uid]
			}
			d.Favorites = append(d.Favorites, DumpEnum{ID: p.id, Name: name, Members: append([]int{}, p.members...)})
		}
	}
	if len(d.Objects) == 0 {
		return d, errors.New("no devices found in the ReGa database")
	}
	return d, nil
}

func match(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

// ParseRegadomFile reads /etc/config/homematic.regadom or any copy of it.
func ParseRegadomFile(path string) (*Dump, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseRegadom(f)
}

// RegadomFromSBK pulls the ReGa database out of a CCU backup: the .sbk is a tar holding
// usr_local.tar.gz, which holds usr/local/etc/config/homematic.regadom. Pure Go, no extraction
// to disk, and the archive is not restored.
func RegadomFromSBK(path string) (*Dump, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	outer := tar.NewReader(f)
	for {
		h, err := outer.Next()
		if err == io.EOF {
			return nil, errors.New("no usr_local.tar.gz in the backup")
		}
		if err != nil {
			return nil, fmt.Errorf("backup: %w", err)
		}
		if h.Name != "usr_local.tar.gz" && h.Name != "./usr_local.tar.gz" {
			continue
		}
		gz, err := gzip.NewReader(outer)
		if err != nil {
			return nil, fmt.Errorf("usr_local.tar.gz: %w", err)
		}
		inner := tar.NewReader(gz)
		for {
			ih, err := inner.Next()
			if err == io.EOF {
				return nil, errors.New("the backup has no ReGa database (homematic.regadom)")
			}
			if err != nil {
				return nil, fmt.Errorf("usr_local.tar.gz: %w", err)
			}
			if strings.HasSuffix(ih.Name, "etc/config/homematic.regadom") {
				return ParseRegadom(io.LimitReader(inner, 256<<20))
			}
		}
	}
}

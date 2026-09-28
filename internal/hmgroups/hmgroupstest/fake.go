// Package hmgroupstest is a fake of hmipserver's group pages for tests (task 180): the commands
// of POST /pages/jpages/group/<command>?sid=… with the answer shapes measured on the OVA on
// 2026-09-22 - the envelope {isSuccessful, errorCode, content} for refusals and for create, edit,
// save and delete; the JSON itself for list, suitableGroupMembers and configureDevices - and the
// fork's JSON templates' field names.
package hmgroupstest

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Device is one device the pages could put into a group.
type Device struct {
	ID, Serial, Type string
}

// Group is one group as the fake stores it.
type Group struct {
	ID           int
	Name         string
	Type         string
	ForbidSingle bool
	DeviceName   string
	Members      []string // device ids
}

// Fake is the server. Fields are read under its lock by the handler; set them before the calls.
type Fake struct {
	mu sync.Mutex
	// SID is the one session the fake accepts; anything else is errorCode 42.
	SID string
	// HTML makes the page commands render the WebUI's HTML instead of the JSON templates (an
	// image without the fork's templates).
	HTML bool
	// Broken makes every command answer HTTP 500.
	Broken bool
	// Devices are the devices the pages offer; Types the group types (id → label).
	Devices []Device
	Types   [][2]string
	Groups  map[int]*Group
	nextID  int
	// Calls records "<command> <body>" in order.
	Calls []string
	// Pending are the devices configureDevices reports for a serial.
	Pending map[string][]Device
	// SaveDies is the number of saves to come that die in hmipserver's worker after the group
	// and its name were stored and before its members were: no answer at all, as a member list it
	// cannot parse does. SaveUnanswered the number that store everything and still do not answer.
	SaveDies, SaveUnanswered int
	// Conns counts the connections the server accepted.
	Conns int

	Server *httptest.Server
}

// New starts the fake with one accepted sid and the two types hmipserver offers.
func New(sid string) *Fake {
	f := &Fake{SID: sid, Groups: map[int]*Group{}, nextID: 1, Pending: map[string][]Device{},
		Types: [][2]string{{"HomeMatic.heating", "Heating_Control"}, {"hmip.heating.group", "HmIP-Heizungssteuerung"}}}
	f.Server = httptest.NewUnstartedServer(http.HandlerFunc(f.serve))
	f.Server.Config.ConnState = func(_ net.Conn, st http.ConnState) {
		if st == http.StateNew {
			f.mu.Lock()
			f.Conns++
			f.mu.Unlock()
		}
	}
	f.Server.Start()
	return f
}

// Connections is the number of connections accepted so far.
func (f *Fake) Connections() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Conns
}

// Set changes the fake's state under its lock, while a handler may run.
func (f *Fake) Set(fn func(f *Fake)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

// die is hmipserver's worker dying in an exception: the request is never answered; the handler
// waits for the client to give up. The lock is released meanwhile, so other commands go on.
func (f *Fake) die(r *http.Request) {
	f.mu.Unlock()
	<-r.Context().Done()
	f.mu.Lock()
}

// Close stops the server.
func (f *Fake) Close() { f.Server.Close() }

// URL is the base the client takes.
func (f *Fake) URL() string { return f.Server.URL }

func (f *Fake) envelope(w http.ResponseWriter, ok bool, code, content string) {
	writeJSON(w, map[string]any{"isSuccessful": ok, "errorCode": code, "content": content})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Broken {
		http.Error(w, "boom", 500)
		return
	}
	if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/pages/jpages/group/") {
		http.NotFound(w, r)
		return
	}
	cmd := strings.TrimPrefix(r.URL.Path, "/pages/jpages/group/")
	raw, _ := io.ReadAll(r.Body)
	f.Calls = append(f.Calls, cmd+" "+strings.TrimSpace(string(raw)))
	if r.URL.Query().Get("sid") != f.SID {
		f.envelope(w, false, "42", "")
		return
	}
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	switch cmd {
	case "list":
		groups := []map[string]any{}
		for _, g := range f.sorted() {
			groups = append(groups, f.row(g))
		}
		if f.HTML {
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, "<!DOCTYPE html><html><body>knockout</body></html>")
			return
		}
		writeJSON(w, map[string]any{"groups": groups, "devicesToConfigure": body["devicesToConfigure"]})
	case "create":
		// hmipserver's new group is id 0 until save gives it one
		g := &Group{ID: 0, Type: f.Types[0][0]}
		f.envelope(w, true, "", f.page(g, true))
	case "edit":
		g, ok := f.Groups[intOf(body["groupId"])]
		if !ok {
			f.envelope(w, false, "1", "no such group")
			return
		}
		// edit stores the device name it is given (hmipserver's changeGroupDeviceName)
		g.DeviceName = str(body["groupDeviceName"])
		f.envelope(w, true, "", f.page(g, false))
	case "suitableGroupMembers":
		assignable, leftover := []map[string]any{}, []map[string]any{}
		for _, d := range f.Devices {
			row := map[string]any{"id": d.ID, "serialNumber": d.Serial, "type": d.Type}
			if f.inGroup(d.ID, 0) {
				leftover = append(leftover, row)
			} else {
				assignable = append(assignable, row)
			}
		}
		writeJSON(w, map[string]any{"assignableGroupMembers": assignable, "leftoverGroupMembers": leftover})
	case "save":
		id := intOf(body["groupId"])
		isNew, _ := body["isNewGroup"].(bool)
		g := f.Groups[id]
		if isNew || g == nil {
			id = f.nextID
			f.nextID++
			g = &Group{ID: id}
			f.Groups[id] = g
		}
		g.Name = unescape(str(body["groupName"]))
		g.Type = str(body["groupTypeId"])
		g.ForbidSingle, _ = body["forbidSingleOperation"].(bool)
		g.DeviceName = unescape(str(body["groupDeviceName"]))
		// hmipserver has stored the group now; the members come after
		ids, ok := memberIDs(body["assignedDevicesIds"])
		if !ok || f.SaveDies > 0 {
			if ok {
				f.SaveDies--
			}
			f.die(r)
			return
		}
		g.Members = ids
		if f.SaveUnanswered > 0 {
			f.SaveUnanswered--
			f.die(r)
			return
		}
		f.envelope(w, true, "", fmt.Sprint(id))
	case "delete":
		id := intOf(body["groupId"])
		g, ok := f.Groups[id]
		if !ok {
			f.envelope(w, false, "1", "no such group")
			return
		}
		former := []map[string]any{}
		for _, m := range g.Members {
			d := f.device(m)
			former = append(former, map[string]any{"id": d.ID, "name": d.Serial, "type": d.Type})
		}
		delete(f.Groups, id)
		b, _ := json.Marshal(former)
		f.envelope(w, true, "", string(b))
	case "configureDevices":
		rows := []map[string]any{}
		for _, d := range f.Pending[str(body["virtualDeviceSerialNumber"])] {
			rows = append(rows, map[string]any{"id": d.ID, "serial": d.Serial, "type": d.Type})
		}
		writeJSON(w, map[string]any{"devices": rows})
	default:
		f.envelope(w, false, "9", "unknown command "+cmd)
	}
}

func (f *Fake) sorted() []*Group {
	out := make([]*Group, 0, len(f.Groups))
	for _, g := range f.Groups {
		out = append(out, g)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	return out
}

func (f *Fake) label(typeID string) string {
	for _, t := range f.Types {
		if t[0] == typeID {
			return t[1]
		}
	}
	return typeID
}

func (f *Fake) row(g *Group) map[string]any {
	return map[string]any{"id": fmt.Sprint(g.ID), "name": g.Name, "type": g.Type, "typeLabel": f.label(g.Type)}
}

func (f *Fake) device(id string) Device {
	for _, d := range f.Devices {
		if d.ID == id {
			return d
		}
	}
	return Device{ID: id, Serial: id}
}

// inGroup says whether the device is in a group other than except.
func (f *Fake) inGroup(id string, except int) bool {
	for _, g := range f.Groups {
		if g.ID == except {
			continue
		}
		for _, m := range g.Members {
			if m == id {
				return true
			}
		}
	}
	return false
}

func member(d Device) map[string]any {
	return map[string]any{"id": d.ID, "serial": d.Serial, "type": d.Type}
}

// page renders the editor's JSON as the fork's GroupEditPage.ftl does.
func (f *Fake) page(g *Group, isNew bool) string {
	if f.HTML {
		return "<!DOCTYPE html><html><body>editor</body></html>"
	}
	types := []map[string]any{}
	for _, t := range f.Types {
		types = append(types, map[string]any{"id": t[0], "label": t[1]})
	}
	assigned, assignable, leftover := []map[string]any{}, []map[string]any{}, []map[string]any{}
	for _, m := range g.Members {
		assigned = append(assigned, member(f.device(m)))
	}
	for _, d := range f.Devices {
		switch {
		case f.inGroup(d.ID, g.ID) && !contains(g.Members, d.ID):
			leftover = append(leftover, member(d))
		case !contains(g.Members, d.ID):
			assignable = append(assignable, member(d))
		}
	}
	b, _ := json.Marshal(map[string]any{
		"id": g.ID, "isNew": isNew, "executeDeviceRefresh": false, "regaId": "", "name": g.Name,
		"groupDeviceName": g.DeviceName, "forbidSingleOperation": g.ForbidSingle, "types": types, "type": g.Type,
		"assignable": assignable, "assigned": assigned, "leftover": leftover,
	})
	return string(b)
}

// lenientLiteral is an id hmipserver reads when it arrives unquoted inside a list.
var lenientLiteral = regexp.MustCompile(`^[^\s,:=;#\[\]{}/\\'"]+$`)

// memberIDs reads assignedDevicesIds as hmipserver does (measured on a lab system): the field's
// text, parsed again as a JSON list. A string is the list itself; an array arrives as "[a, b]"
// without quotes, which it reads only while every id is a plain word - an HmIP channel address's
// colon ends it ("Unterminated array" in its log), and the command is never answered.
func memberIDs(v any) ([]string, bool) {
	switch x := v.(type) {
	case string:
		var ids []string
		if err := json.Unmarshal([]byte(x), &ids); err != nil {
			return nil, false
		}
		return ids, true
	case []any:
		var ids []string
		for _, e := range x {
			s, ok := e.(string)
			if !ok || !lenientLiteral.MatchString(s) {
				return nil, false
			}
			ids = append(ids, s)
		}
		return ids, true
	}
	return nil, false
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func intOf(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case string:
		var i int
		_, _ = fmt.Sscan(n, &i)
		return i
	}
	return 0
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// unescape undoes the WebUI's escape() the client applies to the name (%XX and %uXXXX).
func unescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+6 <= len(s) && s[i+1] == 'u' {
			var r rune
			if _, err := fmt.Sscanf(s[i+2:i+6], "%04X", &r); err == nil {
				b.WriteRune(r)
				i += 5
				continue
			}
		}
		if s[i] == '%' && i+3 <= len(s) {
			var r rune
			if _, err := fmt.Sscanf(s[i+1:i+3], "%02X", &r); err == nil {
				b.WriteRune(r)
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

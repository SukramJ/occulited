package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/hmgroups"
	"github.com/hobbyquaker/occulited/internal/meta"
)

// ---- task 180: heating groups through occulited's API ---------------------------------------
//
// hmipserver's group pages do the work (internal/hmgroups); occulited is their only client, with
// the logins it already has, and answers hmipserver's session check on the loopback. The ReGa
// side effects the WebUI did from the browser afterwards are occulited's now, in the same
// request: the members' inHeatingGroup metadata, and the group device's name.

// GroupsAPI is what the routes need: the client, the session it answers for, and the metadata
// store for the side effects.
type GroupsAPI struct {
	Client  *hmgroups.Client
	Session *hmgroups.Session
	Meta    *meta.Store
	Log     *slog.Logger
	// Interface is the group devices' interface in the metadata refs (VirtualDevices).
	Interface string
}

// MetaNamespace is the objects' meta namespace the group membership lives in; the WebUI wrote
// ReGa's inHeatingGroup, the CCU's clients read it there.
const groupsMetaNamespace = "hmip"

var groupIDRe = regexp.MustCompile(`^[0-9]{1,9}$`)

func (a *SystemAPI) groupsUnavailable(w http.ResponseWriter) bool {
	if a.Groups == nil || a.Groups.Client == nil {
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "unsupported", Message: "heating groups are not available on this system"})
		return true
	}
	return false
}

func (g *GroupsAPI) log() *slog.Logger {
	if g.Log != nil {
		return g.Log
	}
	return slog.Default()
}

// groupView is a group as the API lists it.
type groupView struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	TypeLabel string `json:"type_label"`
	// Device is the group device's address on VirtualDevices, and Ref its metadata ref.
	Device string `json:"device"`
	Ref    string `json:"ref"`
}

func (g *GroupsAPI) view(x hmgroups.Group) groupView {
	id, _ := strconv.Atoi(x.ID)
	serial := hmgroups.Serial(id)
	return groupView{ID: id, Name: x.Name, Type: x.Type, TypeLabel: x.TypeLabel, Device: serial, Ref: g.Interface + "." + serial}
}

// memberView is a device as the group pages know it, with its metadata ref when the serial is a
// device address.
type memberView struct {
	ID     string `json:"id"`
	Serial string `json:"serial"`
	Type   string `json:"type"`
}

func members(list []hmgroups.Member) []memberView {
	out := make([]memberView, 0, len(list))
	for _, m := range list {
		out = append(out, memberView{ID: m.ID, Serial: m.Serial, Type: m.Type})
	}
	return out
}

// GET /groups: the groups, and the devices whose configuration is still pending from the last change.
func (a *SystemAPI) groupsList(w http.ResponseWriter, r *http.Request) {
	if a.groupsUnavailable(w) {
		return
	}
	v, err := a.Groups.Client.List(r.Context())
	if err != nil {
		groupsErr(w, err)
		return
	}
	out := make([]groupView, 0, len(v.Groups))
	for _, x := range v.Groups {
		out = append(out, a.Groups.view(x))
	}
	writeJSON(w, 200, map[string]any{"groups": out, "devices_to_configure": members(v.DevicesToConfigure)})
}

// GET /groups/types: the group types the editor offers, each with what a group of it could take
// now and what fits no group any more.
func (a *SystemAPI) groupsTypes(w http.ResponseWriter, r *http.Request) {
	if a.groupsUnavailable(w) {
		return
	}
	ev, err := a.Groups.Client.Create(r.Context())
	if err != nil {
		groupsErr(w, err)
		return
	}
	type typeView struct {
		ID         string       `json:"id"`
		Label      string       `json:"label"`
		Assignable []memberView `json:"assignable"`
		Leftover   []memberView `json:"leftover"`
	}
	out := make([]typeView, 0, len(ev.Types))
	for _, t := range ev.Types {
		assignable, leftover, err := a.Groups.Client.SuitableMembers(r.Context(), t.ID)
		if err != nil {
			groupsErr(w, err)
			return
		}
		out = append(out, typeView{ID: t.ID, Label: t.Label, Assignable: members(assignable), Leftover: members(leftover)})
	}
	writeJSON(w, 200, map[string]any{"types": out})
}

// GET /groups/{id}: one group with its members, what it could take, and what fits no more.
func (a *SystemAPI) groupsGet(w http.ResponseWriter, r *http.Request) {
	if a.groupsUnavailable(w) {
		return
	}
	id, ok := groupID(w, r)
	if !ok || !a.groupKnown(w, r, id) {
		return
	}
	ev, err := a.Groups.Client.Edit(r.Context(), id)
	if err != nil {
		groupsErr(w, err)
		return
	}
	writeJSON(w, 200, editView(a.Groups, ev))
}

func editView(g *GroupsAPI, ev hmgroups.EditView) map[string]any {
	serial := hmgroups.Serial(ev.ID)
	return map[string]any{
		"id": ev.ID, "name": ev.Name, "type": ev.Type, "device": serial, "ref": g.Interface + "." + serial,
		"device_name": ev.GroupDeviceName, "forbid_single_operation": ev.ForbidSingle,
		"members": members(ev.Assigned), "assignable": members(ev.Assignable), "leftover": members(ev.Leftover),
		"types": ev.Types,
	}
}

type groupBody struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Members []string `json:"members"`
	// ForbidSingleOperation is the WebUI's "operate as group only" (nil = leave as it is)
	ForbidSingleOperation *bool `json:"forbid_single_operation"`
}

var groupNameRe = regexp.MustCompile(`^[^\x00-\x1f]{1,64}$`)

func (b groupBody) check(needType bool) error {
	if !groupNameRe.MatchString(strings.TrimSpace(b.Name)) {
		return errors.New("name: 1 to 64 characters on one line")
	}
	if needType && b.Type == "" {
		return errors.New("type: the group type, e.g. HomeMatic.heating")
	}
	for _, m := range b.Members {
		if m == "" || len(m) > 64 || strings.ContainsAny(m, "\r\n\"") {
			return errors.New("members: hmipserver's device ids, as GET /groups/types lists them")
		}
	}
	return nil
}

// POST /groups {name, type, members}: a new group - the editor's create, then its save with the
// members; the answer names the new group and the devices whose configuration is pending.
func (a *SystemAPI) groupsCreate(w http.ResponseWriter, r *http.Request) {
	if a.groupsUnavailable(w) {
		return
	}
	var b groupBody
	if err := readJSON(r, &b); err != nil {
		badBody(w, err)
		return
	}
	if err := b.check(true); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: err.Error()})
		return
	}
	ctx := r.Context()
	ev, err := a.Groups.Client.Create(ctx)
	if err != nil {
		groupsErr(w, err)
		return
	}
	if !typeOffered(ev, b.Type) {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: "type: not one hmipserver offers: " + typeIDs(ev)})
		return
	}
	forbid := false
	if b.ForbidSingleOperation != nil {
		forbid = *b.ForbidSingleOperation
	}
	name := strings.TrimSpace(b.Name)
	res, err := a.Groups.Client.Save(ctx, hmgroups.SaveBody{ID: ev.ID, Name: name, TypeID: b.Type, ForbidSingle: forbid, MemberIDs: b.Members, IsNew: true, GroupDeviceName: name + " " + hmgroups.Serial(ev.ID)})
	if err != nil {
		groupsErr(w, err)
		return
	}
	a.Groups.afterSave(ctx, res.ID, name, b.Members, nil)
	a.groupsAnswer(w, r, res.ID)
}

// PUT /groups/{id} {name, members}: the name and the members as a whole - adding and removing
// members is one save with the new list, as the WebUI did it.
func (a *SystemAPI) groupsUpdate(w http.ResponseWriter, r *http.Request) {
	if a.groupsUnavailable(w) {
		return
	}
	id, ok := groupID(w, r)
	if !ok {
		return
	}
	var b groupBody
	if err := readJSON(r, &b); err != nil {
		badBody(w, err)
		return
	}
	if !a.groupKnown(w, r, id) {
		return
	}
	ctx := r.Context()
	ev, err := a.Groups.Client.Edit(ctx, id)
	if err != nil {
		groupsErr(w, err)
		return
	}
	if b.Name == "" {
		b.Name = ev.Name
	}
	if b.Type == "" {
		b.Type = ev.Type
	}
	if b.Members == nil {
		for _, m := range ev.Assigned {
			b.Members = append(b.Members, m.ID)
		}
	}
	if err := b.check(true); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: err.Error()})
		return
	}
	forbid := ev.ForbidSingle
	if b.ForbidSingleOperation != nil {
		forbid = *b.ForbidSingleOperation
	}
	name := strings.TrimSpace(b.Name)
	before := make([]string, 0, len(ev.Assigned))
	for _, m := range ev.Assigned {
		before = append(before, m.ID)
	}
	res, err := a.Groups.Client.Save(ctx, hmgroups.SaveBody{ID: ev.ID, Name: name, TypeID: b.Type, ForbidSingle: forbid, MemberIDs: b.Members, IsNew: false, GroupDeviceName: name + " " + hmgroups.Serial(ev.ID)})
	if err != nil {
		groupsErr(w, err)
		return
	}
	a.Groups.afterSave(ctx, res.ID, name, b.Members, before)
	a.groupsAnswer(w, r, res.ID)
}

// DELETE /groups/{id}: the group goes; its former members lose the group membership.
func (a *SystemAPI) groupsDelete(w http.ResponseWriter, r *http.Request) {
	if a.groupsUnavailable(w) {
		return
	}
	id, ok := groupID(w, r)
	if !ok || !a.groupKnown(w, r, id) {
		return
	}
	former, err := a.Groups.Client.Delete(r.Context(), id)
	if err != nil {
		groupsErr(w, err)
		return
	}
	a.Groups.afterDelete(r.Context(), id, former)
	writeJSON(w, 200, map[string]any{"deleted": id, "former_members": members(former)})
}

// groupsAnswer is what a create or an update ends with: the group as it stands, and the devices
// whose configuration is pending.
func (a *SystemAPI) groupsAnswer(w http.ResponseWriter, r *http.Request, id int) {
	ev, err := a.Groups.Client.Edit(r.Context(), id)
	if err != nil {
		groupsErr(w, err)
		return
	}
	pending, perr := a.Groups.Client.ConfigureDevices(r.Context(), hmgroups.Serial(id))
	if perr != nil {
		a.Groups.log().Warn("groups: the pending configuration could not be read", "group", id, "err", perr)
		pending = []hmgroups.Member{}
	}
	out := editView(a.Groups, ev)
	out["devices_to_configure"] = members(pending)
	writeJSON(w, 200, out)
}

// groupKnown says whether hmipserver lists the group: its edit and delete of an unknown id end
// in a NullPointerException in its worker and no answer at all (measured 2026-09-22 on the
// OVA), so the API asks the list first and answers 404 itself.
func (a *SystemAPI) groupKnown(w http.ResponseWriter, r *http.Request, id int) bool {
	v, err := a.Groups.Client.List(r.Context())
	if err != nil {
		groupsErr(w, err)
		return false
	}
	want := strconv.Itoa(id)
	for _, g := range v.Groups {
		if g.ID == want {
			return true
		}
	}
	writeJSON(w, http.StatusNotFound, apiError{Error: "unknown-group", Message: fmt.Sprintf("there is no group %d", id)})
	return false
}

func groupID(w http.ResponseWriter, r *http.Request) (int, bool) {
	s := r.PathValue("id")
	if !groupIDRe.MatchString(s) {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid", Message: "the group id is a number"})
		return 0, false
	}
	id, _ := strconv.Atoi(s)
	return id, true
}

func typeOffered(ev hmgroups.EditView, id string) bool {
	for _, t := range ev.Types {
		if t.ID == id {
			return true
		}
	}
	return false
}

func typeIDs(ev hmgroups.EditView) string {
	ids := make([]string, 0, len(ev.Types))
	for _, t := range ev.Types {
		ids = append(ids, t.ID)
	}
	return strings.Join(ids, ", ")
}

func groupsErr(w http.ResponseWriter, err error) {
	if errors.Is(err, hmgroups.ErrSession) {
		writeJSON(w, http.StatusBadGateway, apiError{Error: "hmipserver", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusBadGateway, apiError{Error: "hmipserver", Message: err.Error()})
}

// ---- the side effects in the metadata store ---------------------------------------------------

// afterSave: the members' inHeatingGroup (true for the members, false for the ones that left),
// and the group device's name.
func (g *GroupsAPI) afterSave(ctx context.Context, id int, name string, memberIDs, before []string) {
	if g.Meta == nil {
		return
	}
	now := map[string]bool{}
	for _, m := range memberIDs {
		now[m] = true
	}
	for _, m := range memberIDs {
		g.setInGroup(m, true)
	}
	for _, m := range before {
		if !now[m] {
			g.setInGroup(m, false)
		}
	}
	ref := g.Interface + "." + hmgroups.Serial(id)
	full := name + " " + hmgroups.Serial(id)
	if _, _, err := g.Meta.SetObject(nil, ref, meta.ObjectPatch{Name: &full}); err != nil {
		// an object the store does not have yet (the group device is new): put it
		if _, _, perr := g.Meta.PutObject(nil, ref, full, nil, nil); perr != nil {
			g.log().Warn("groups: the group device's name", "ref", ref, "err", perr)
		}
	}
}

// afterDelete: the former members' inHeatingGroup off; the group device's object goes.
func (g *GroupsAPI) afterDelete(ctx context.Context, id int, former []hmgroups.Member) {
	if g.Meta == nil {
		return
	}
	for _, m := range former {
		g.setInGroup(m.ID, false)
	}
	ref := g.Interface + "." + hmgroups.Serial(id)
	if _, _, err := g.Meta.DeleteObject(nil, ref); err != nil {
		g.log().Debug("groups: the group device's object", "ref", ref, "err", err)
	}
}

// setInGroup writes {"inHeatingGroup": …} into the device's hmip meta namespace. The member id
// is hmipserver's; the ref is the device's on its interface, which the id names when it is a
// serial (hmipserver hands the serial as the id where no ReGa numbers the devices).
func (g *GroupsAPI) setInGroup(memberID string, in bool) {
	ref := g.refOf(memberID)
	if ref == "" {
		return
	}
	cur := map[string]any{}
	if o, err := g.Meta.GetObject(ref); err == nil {
		if raw, ok := o.Meta[groupsMetaNamespace]; ok {
			_ = json.Unmarshal(raw, &cur)
		}
	}
	cur["inHeatingGroup"] = in
	raw, _ := json.Marshal(cur)
	if _, _, err := g.Meta.SetObject(nil, ref, meta.ObjectPatch{Meta: map[string]json.RawMessage{groupsMetaNamespace: raw}}); err != nil {
		if _, _, perr := g.Meta.PutObject(nil, ref, "", nil, map[string]json.RawMessage{groupsMetaNamespace: raw}); perr != nil {
			g.log().Debug("groups: the member's metadata", "ref", ref, "err", perr)
		}
	}
}

// refOf turns a member id into a metadata ref: an id that is a device serial of a paired
// interface is looked up among the store's objects by its address part; anything else is left.
func (g *GroupsAPI) refOf(memberID string) string {
	id := strings.TrimSpace(memberID)
	if id == "" {
		return ""
	}
	for ref := range g.Meta.Snapshot().Objects {
		if strings.HasSuffix(ref, "."+id) {
			return ref
		}
	}
	return ""
}

// ---- hmipserver's session check ----------------------------------------------------------------

// homematicCGI answers POST /api/homematic.cgi for hmipserver's SessionVerifier: the JSON-RPC
// Event.poll with {_session_id_} - the CCU JSON-API call it makes before every group command -
// answered as a CCU does for a live session ({"version":"1.1","result":[],"error":null}) when
// the sid is occulited's own, and with the CCU's "access denied" otherwise. Loopback only: the
// request comes through lighttpd from 127.0.0.1, and nothing else is a CCU JSON-API here (D-1).
func (a *SystemAPI) homematicCGI(w http.ResponseWriter, r *http.Request) {
	if !loopbackOnly(r) {
		writeJSON(w, http.StatusNotFound, apiError{Error: "not-found", Message: "not found"})
		return
	}
	var b struct {
		Method string `json:"method"`
		Params struct {
			SID string `json:"_session_id_"`
		} `json:"params"`
	}
	_ = readJSON(r, &b)
	if a.Groups != nil && a.Groups.Session != nil && b.Method == "Event.poll" && a.Groups.Session.Verify(b.Params.SID) {
		writeJSON(w, 200, map[string]any{"version": "1.1", "result": []any{}, "error": nil})
		return
	}
	writeJSON(w, 200, map[string]any{"version": "1.1", "result": nil, "error": map[string]any{"name": "JSONRPCError", "code": 400, "message": fmt.Sprintf("access denied: %s", b.Method)}})
}

// loopbackOnly says the request came from this system: the address lighttpd forwarded, or the
// connection's own.
func loopbackOnly(r *http.Request) bool {
	ip := net.ParseIP(remote(r))
	return ip != nil && ip.IsLoopback()
}

// GroupsScopes documents the routes' scopes: reads system:read, changes system:write.
var _ = auth.ScopeSystemWrite

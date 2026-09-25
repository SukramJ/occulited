// Package hmgroups drives hmipserver's group pages (task 180): the heating groups - the
// VirtualDevices group devices INT000000N - are created, changed and deleted through
// POST /pages/jpages/group/<command>?sid=… on hmipserver's HTTP port, the way the CCU WebUI did
// it. On openccu-lite the four FreeMarker pages render JSON instead of the WebUI's HTML (the
// fork's overlay), so every answer is data.
//
// The session: each command checks its sid first with a POST of the JSON-RPC Event.poll to
// http://127.0.0.1/api/homematic.cgi. occulited answers that call on the loopback for one sid it
// keeps in memory (Session), and with the CCU's 400 "access denied" for everything else - the one
// answer hmipserver's check needs, not a CCU JSON-API (D-1, D-6).
package hmgroups

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Session is the sid occulited uses towards hmipserver: random at start, never stored, never
// answered to anyone but hmipserver's loopback check.
type Session struct {
	sid string
}

// NewSession makes the sid, in the CCU's shape (26 characters of the base32 alphabet).
func NewSession() *Session {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	b := make([]byte, 26)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return &Session{sid: string(b)}
}

// SID is the session id.
func (s *Session) SID() string { return s.sid }

// Verify says whether sid is the session's, in constant time.
func (s *Session) Verify(sid string) bool {
	return s != nil && sid != "" && subtle.ConstantTimeCompare([]byte(sid), []byte(s.sid)) == 1
}

// ErrSession is hmipserver's errorCode 42: it did not accept the session - its check did not
// reach occulited, or answered no.
var ErrSession = errors.New("hmipserver did not accept the session: its check of it on the loopback failed")

// Client calls the group pages.
type Client struct {
	// Base is hmipserver's HTTP base, http://127.0.0.1:39292.
	Base    string
	Session *Session
	HTTP    *http.Client
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// envelope is every group page's answer.
type envelope struct {
	IsSuccessful bool   `json:"isSuccessful"`
	ErrorCode    string `json:"errorCode"`
	Content      string `json:"content"`
}

// call posts one command and returns its content. A command whose page renders JSON hands the
// content back as is; a refusal is an error naming the code.
func (c *Client) call(ctx context.Context, command string, body any) (string, error) {
	if c.Session == nil {
		return "", errors.New("no session")
	}
	b, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	url := strings.TrimRight(c.Base, "/") + "/pages/jpages/group/" + command + "?sid=" + c.Session.SID()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http().Do(req)
	if err != nil {
		return "", fmt.Errorf("hmipserver: %w", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return "", err
	}
	if res.StatusCode != 200 {
		return "", fmt.Errorf("hmipserver: %s answered HTTP %d", command, res.StatusCode)
	}
	// two shapes: a refusal, and the commands that hand a page over (create, edit, save, delete),
	// come as the envelope {isSuccessful, errorCode, content}; a data command that succeeded
	// (list, suitableGroupMembers, configureDevices) answers its JSON itself
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		// a JSON array (a data command), or the WebUI's HTML page (an image without the lite
		// templates renders it for list too): the caller's decode names the latter
		if t := strings.TrimSpace(string(raw)); strings.HasPrefix(t, "[") || strings.HasPrefix(t, "<") {
			return string(raw), nil
		}
		return "", fmt.Errorf("hmipserver: %s: not JSON: %s", command, excerpt(raw))
	}
	if _, ok := probe["isSuccessful"]; !ok {
		return string(raw), nil
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", fmt.Errorf("hmipserver: %s: not the envelope: %s", command, excerpt(raw))
	}
	if !env.IsSuccessful {
		if env.ErrorCode == "42" {
			return "", ErrSession
		}
		return "", fmt.Errorf("hmipserver: %s refused: code %s: %s", command, env.ErrorCode, excerpt([]byte(env.Content)))
	}
	return env.Content, nil
}

func excerpt(b []byte) string {
	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) > 160 {
		return s[:157] + "..."
	}
	return s
}

// decode parses a page's JSON content; the WebUI's HTML page (the fork's templates not in place)
// is named as such.
func decode(content string, v any) error {
	t := strings.TrimSpace(content)
	if !strings.HasPrefix(t, "{") && !strings.HasPrefix(t, "[") {
		return fmt.Errorf("hmipserver rendered the WebUI's page, not JSON: this image has no lite group templates (%s)", excerpt([]byte(t)))
	}
	if err := json.Unmarshal([]byte(t), v); err != nil {
		return fmt.Errorf("hmipserver's page is not the JSON expected: %w: %s", err, excerpt([]byte(t)))
	}
	return nil
}

// ---- what the pages render (the fork's templates) ------------------------------------------

// Group is one row of the list page.
type Group struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	TypeLabel string `json:"typeLabel"`
}

// Member is a device as the group pages know it: hmipserver's id for it, its serial (the label)
// and its type.
type Member struct {
	ID     string `json:"id"`
	Serial string `json:"serial"`
	Type   string `json:"type"`
}

// ListView is the list page: the groups, and the devices whose configuration is pending after
// the last change (the request's own list, echoed).
type ListView struct {
	Groups             []Group  `json:"groups"`
	DevicesToConfigure []Member `json:"devicesToConfigure"`
}

// GroupType is one of the types the editor offers.
type GroupType struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// EditView is the editor page of one group (or of a new one): its members, the devices it could
// take and the ones that fit no more, and the types.
type EditView struct {
	ID                   int         `json:"id"`
	IsNew                bool        `json:"isNew"`
	ExecuteDeviceRefresh bool        `json:"executeDeviceRefresh"`
	RegaID               string      `json:"regaId"`
	Name                 string      `json:"name"`
	GroupDeviceName      string      `json:"groupDeviceName"`
	ForbidSingle         bool        `json:"forbidSingleOperation"`
	Types                []GroupType `json:"types"`
	Type                 string      `json:"type"`
	Assignable           []Member    `json:"assignable"`
	Assigned             []Member    `json:"assigned"`
	Leftover             []Member    `json:"leftover"`
}

// SuitableView is suitableGroupMembers' answer (JSON from hmipserver's code, not a template).
type SuitableView struct {
	Assignable []suitable `json:"assignableGroupMembers"`
	Leftover   []suitable `json:"leftoverGroupMembers"`
}

type suitable struct {
	ID     string `json:"id"`
	Serial string `json:"serialNumber"`
	Type   string `json:"type"`
}

func (s suitable) member() Member { return Member{ID: s.ID, Serial: s.Serial, Type: s.Type} }

// ConfigureView is the configure-devices dialog: the members whose configuration is pending.
type ConfigureView struct {
	Devices []Member `json:"devices"`
}

// ---- the commands ---------------------------------------------------------------------------

// List is the group list.
func (c *Client) List(ctx context.Context) (ListView, error) {
	var v ListView
	content, err := c.call(ctx, "list", map[string]any{"devicesToConfigure": []any{}})
	if err != nil {
		return v, err
	}
	err = decode(content, &v)
	if v.Groups == nil {
		v.Groups = []Group{}
	}
	if v.DevicesToConfigure == nil {
		v.DevicesToConfigure = []Member{}
	}
	return v, err
}

// Create opens the editor of a new group: its id and the members a group could take.
func (c *Client) Create(ctx context.Context) (EditView, error) {
	var v EditView
	content, err := c.call(ctx, "create", map[string]any{})
	if err != nil {
		return v, err
	}
	err = decode(content, &v)
	return v.normalized(), err
}

// Edit opens the editor of a group.
func (c *Client) Edit(ctx context.Context, id int) (EditView, error) {
	var v EditView
	content, err := c.call(ctx, "edit", map[string]any{"groupId": id, "groupDeviceName": ""})
	if err != nil {
		return v, err
	}
	err = decode(content, &v)
	return v.normalized(), err
}

func (v EditView) normalized() EditView {
	if v.Types == nil {
		v.Types = []GroupType{}
	}
	if v.Assignable == nil {
		v.Assignable = []Member{}
	}
	if v.Assigned == nil {
		v.Assigned = []Member{}
	}
	if v.Leftover == nil {
		v.Leftover = []Member{}
	}
	return v
}

// SuitableMembers is what a group of the type could take, and what fits no group any more.
func (c *Client) SuitableMembers(ctx context.Context, typeID string) (assignable, leftover []Member, err error) {
	content, err := c.call(ctx, "suitableGroupMembers", map[string]any{"groupTypeId": typeID})
	if err != nil {
		return nil, nil, err
	}
	var v SuitableView
	if err := json.Unmarshal([]byte(content), &v); err != nil {
		return nil, nil, fmt.Errorf("hmipserver: suitableGroupMembers: %w: %s", err, excerpt([]byte(content)))
	}
	assignable, leftover = []Member{}, []Member{}
	for _, s := range v.Assignable {
		assignable = append(assignable, s.member())
	}
	for _, s := range v.Leftover {
		leftover = append(leftover, s.member())
	}
	return assignable, leftover, nil
}

// SaveBody is what the editor's Save posts.
type SaveBody struct {
	ID              int
	Name            string
	TypeID          string
	ForbidSingle    bool
	MemberIDs       []string
	IsNew           bool
	GroupDeviceName string
}

// SaveResult is save's answer: the group's id (a new group's, given now) as hmipserver reports it.
type SaveResult struct {
	ID int
}

var digitsRe = regexp.MustCompile(`\d+`)

// Save writes a group's name and members; a new group gets its id.
func (c *Client) Save(ctx context.Context, b SaveBody) (SaveResult, error) {
	body := map[string]any{
		"groupId": b.ID, "groupName": jsEscape(b.Name), "groupTypeId": b.TypeID, "forbidSingleOperation": b.ForbidSingle,
		"assignedDevicesIds": nonNil(b.MemberIDs), "isNewGroup": b.IsNew, "groupDeviceName": b.GroupDeviceName,
	}
	content, err := c.call(ctx, "save", body)
	if err != nil {
		return SaveResult{}, err
	}
	// the WebUI took the id out of the content as a number; the content is the id itself, or a
	// JSON with it - either way the first number in it
	res := SaveResult{ID: b.ID}
	if m := digitsRe.FindString(content); m != "" {
		if n, err := strconv.Atoi(m); err == nil {
			res.ID = n
		}
	}
	return res, nil
}

// Delete removes a group and answers its former members.
func (c *Client) Delete(ctx context.Context, id int) ([]Member, error) {
	content, err := c.call(ctx, "delete", map[string]any{"groupId": id})
	if err != nil {
		return nil, err
	}
	var former []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if strings.TrimSpace(content) == "" {
		return []Member{}, nil
	}
	if err := json.Unmarshal([]byte(content), &former); err != nil {
		return nil, fmt.Errorf("hmipserver: delete: %w: %s", err, excerpt([]byte(content)))
	}
	out := []Member{}
	for _, f := range former {
		out = append(out, Member{ID: f.ID, Serial: f.Name, Type: f.Type})
	}
	return out, nil
}

// ConfigureDevices is the pending-configuration dialog of a group device.
func (c *Client) ConfigureDevices(ctx context.Context, serial string) ([]Member, error) {
	content, err := c.call(ctx, "configureDevices", map[string]any{"virtualDeviceSerialNumber": serial})
	if err != nil {
		return nil, err
	}
	var v ConfigureView
	if err := decode(content, &v); err != nil {
		return nil, err
	}
	if v.Devices == nil {
		v.Devices = []Member{}
	}
	return v.Devices, nil
}

// Serial is the group device's address for a group id: INT plus the id, seven digits.
func Serial(id int) string { return fmt.Sprintf("INT%07d", id) }

// jsEscape is what the WebUI's `escape()` did to the name before posting it: hmipserver unescapes
// it. Letters, digits and @*_+-./ stay; everything else is %XX or %uXXXX.
func jsEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 128 && (('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9') || strings.ContainsRune("@*_+-./", r)):
			b.WriteRune(r)
		case r < 256:
			fmt.Fprintf(&b, "%%%02X", r)
		default:
			fmt.Fprintf(&b, "%%u%04X", r)
		}
	}
	return b.String()
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

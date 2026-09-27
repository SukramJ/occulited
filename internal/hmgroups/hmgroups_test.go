package hmgroups

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/hmgroups/hmgroupstest"
)

func rig(t *testing.T) (*hmgroupstest.Fake, *Client) {
	t.Helper()
	s := NewSession()
	f := hmgroupstest.New(s.SID())
	t.Cleanup(f.Close)
	f.Devices = []hmgroupstest.Device{{ID: "KEQ9000003", Serial: "KEQ9000003", Type: "HM-Sec-SC"}, {ID: "00010000000A10:1", Serial: "00010000000A10:1", Type: "HmIP-eTRV"}}
	return f, &Client{Base: f.URL() + "/", Session: s}
}

func TestSession(t *testing.T) {
	s := NewSession()
	if len(s.SID()) != 26 || strings.Trim(s.SID(), "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567") != "" {
		t.Fatalf("sid %q is not the CCU's shape", s.SID())
	}
	if NewSession().SID() == s.SID() {
		t.Fatal("two sessions with the same sid")
	}
	if !s.Verify(s.SID()) || s.Verify("") || s.Verify(s.SID()[1:]) || (*Session)(nil).Verify("x") {
		t.Fatal("Verify")
	}
}

func TestRoundTrip(t *testing.T) {
	f, c := rig(t)
	ctx := context.Background()
	lv, err := c.List(ctx)
	if err != nil || len(lv.Groups) != 0 || lv.DevicesToConfigure == nil {
		t.Fatalf("list: %v %+v", err, lv)
	}
	ev, err := c.Create(ctx)
	if err != nil || !ev.IsNew || ev.ID != 1 || len(ev.Types) != 2 || len(ev.Assignable) != 2 || ev.Assigned == nil || ev.Leftover == nil {
		t.Fatalf("create: %v %+v", err, ev)
	}
	assignable, leftover, err := c.SuitableMembers(ctx, "HomeMatic.heating")
	if err != nil || len(assignable) != 2 || len(leftover) != 0 || assignable[0].Serial != "KEQ9000003" {
		t.Fatalf("suitable: %v %+v %+v", err, assignable, leftover)
	}
	res, err := c.Save(ctx, SaveBody{ID: ev.ID, Name: "Bad äöü/1", TypeID: "HomeMatic.heating", MemberIDs: []string{"KEQ9000003"}, IsNew: true, GroupDeviceName: "Bad INT0000001"})
	if err != nil || res.ID != 1 {
		t.Fatalf("save: %v %+v", err, res)
	}
	if g := f.Groups[1]; g == nil || g.Name != "Bad äöü/1" || len(g.Members) != 1 || g.DeviceName != "Bad INT0000001" {
		t.Fatalf("stored: %+v", f.Groups[1])
	}
	// the name went escaped the WebUI's way
	if !strings.Contains(f.Calls[len(f.Calls)-1], `"groupName":"Bad%20%E4%F6%FC/1"`) {
		t.Fatalf("save call: %s", f.Calls[len(f.Calls)-1])
	}
	ev, err = c.Edit(ctx, 1)
	if err != nil || ev.IsNew || ev.Name != "Bad äöü/1" || len(ev.Assigned) != 1 || len(ev.Assignable) != 1 {
		t.Fatalf("edit: %v %+v", err, ev)
	}
	// the second device fits a second group; after that it is nobody's assignable
	res, err = c.Save(ctx, SaveBody{ID: 0, Name: "Two", TypeID: "HomeMatic.heating", MemberIDs: []string{"00010000000A10:1"}, IsNew: true})
	if err != nil || res.ID != 2 {
		t.Fatalf("save 2: %v %+v", err, res)
	}
	assignable, leftover, err = c.SuitableMembers(ctx, "HomeMatic.heating")
	if err != nil || len(assignable) != 0 || len(leftover) != 2 {
		t.Fatalf("suitable after: %v %+v %+v", err, assignable, leftover)
	}
	lv, err = c.List(ctx)
	if err != nil || len(lv.Groups) != 2 || lv.Groups[1].Name != "Two" || lv.Groups[0].TypeLabel != "Heating_Control" {
		t.Fatalf("list: %v %+v", err, lv)
	}
	f.Pending[Serial(1)] = []hmgroupstest.Device{{ID: "KEQ9000003", Serial: "KEQ9000003", Type: "HM-Sec-SC"}}
	pending, err := c.ConfigureDevices(ctx, Serial(1))
	if err != nil || len(pending) != 1 || pending[0].Type != "HM-Sec-SC" {
		t.Fatalf("configure: %v %+v", err, pending)
	}
	if pending, err = c.ConfigureDevices(ctx, Serial(2)); err != nil || len(pending) != 0 {
		t.Fatalf("configure 2: %v %+v", err, pending)
	}
	former, err := c.Delete(ctx, 1)
	if err != nil || len(former) != 1 || former[0].Serial != "KEQ9000003" || former[0].Type != "HM-Sec-SC" {
		t.Fatalf("delete: %v %+v", err, former)
	}
	if _, err := c.Delete(ctx, 1); err == nil || !strings.Contains(err.Error(), "code 1") {
		t.Fatalf("delete again: %v", err)
	}
	if _, err := c.Edit(ctx, 7); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("edit unknown: %v", err)
	}
}

func TestRefusals(t *testing.T) {
	f, c := rig(t)
	ctx := context.Background()
	// the wrong session: hmipserver's 42
	other := &Client{Base: f.URL(), Session: NewSession()}
	if _, err := other.List(ctx); !errors.Is(err, ErrSession) {
		t.Fatalf("wrong sid: %v", err)
	}
	if _, err := (&Client{Base: f.URL()}).List(ctx); err == nil {
		t.Fatal("no session must fail")
	}
	// the WebUI's HTML: no templates on this image
	f.HTML = true
	if _, err := c.List(ctx); err == nil || !strings.Contains(err.Error(), "no lite group templates") {
		t.Fatalf("html list: %v", err)
	}
	if _, err := c.Create(ctx); err == nil || !strings.Contains(err.Error(), "no lite group templates") {
		t.Fatalf("html create: %v", err)
	}
	f.HTML = false
	f.Broken = true
	if _, err := c.List(ctx); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("broken: %v", err)
	}
	f.Broken = false
	f.Close()
	if _, err := c.List(ctx); err == nil || !strings.HasPrefix(err.Error(), "hmipserver: ") {
		t.Fatalf("down: %v", err)
	}
}

func TestHelpers(t *testing.T) {
	if Serial(1) != "INT0000001" || Serial(1234567) != "INT1234567" {
		t.Fatal(Serial(1))
	}
	for in, want := range map[string]string{
		"Wohnzimmer": "Wohnzimmer", "a b": "a%20b", "äöü": "%E4%F6%FC", "€": "%u20AC", "@*_+-./": "@*_+-./", "\"x\"": "%22x%22",
	} {
		if got := jsEscape(in); got != want {
			t.Errorf("jsEscape(%q) = %q, want %q", in, got, want)
		}
	}
	if s := excerpt([]byte("  a   b\n" + strings.Repeat("x", 200))); len(s) != 160 || !strings.HasPrefix(s, "a b x") || !strings.HasSuffix(s, "...") {
		t.Errorf("excerpt: %q", s)
	}
	var v struct{ A int }
	if err := decode("<html>", &v); err == nil || !strings.Contains(err.Error(), "no lite group templates") {
		t.Error(err)
	}
	if err := decode(`{"A":"x"}`, &v); err == nil || !strings.Contains(err.Error(), "not the JSON expected") {
		t.Error(err)
	}
	if n := nonNil(nil); n == nil || len(n) != 0 {
		t.Error("nonNil")
	}
}

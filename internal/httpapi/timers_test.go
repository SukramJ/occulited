package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/system"
)

// fakeTimers is a timer lister that keeps own timers in memory (systemd's shape, task 50).
type fakeTimers struct {
	own  map[string]system.LocalTimer
	runs []string
}

func (f *fakeTimers) Timers(context.Context) ([]system.Timer, error) { return []system.Timer{}, nil }
func (f *fakeTimers) ListLocalTimers() []system.LocalTimer {
	out := []system.LocalTimer{}
	for _, t := range f.own {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func (f *fakeTimers) ReadLocalTimer(id string) (system.LocalTimer, error) {
	t, ok := f.own[strings.TrimSuffix(strings.TrimPrefix(id, "local-"), ".timer")]
	if !ok {
		return t, fmt.Errorf("no own timer named %s: %w", id, system.ErrLocalTimerNotFound)
	}
	return t, nil
}
func (f *fakeTimers) CreateLocalTimer(_ context.Context, name, timer, service string) (system.LocalTimer, error) {
	if !system.ValidLocalTimerName(name) {
		return system.LocalTimer{}, errors.New("a timer name is 1 to 32 letters")
	}
	if _, ok := f.own[name]; ok {
		return system.LocalTimer{}, fmt.Errorf("an own timer named %s exists already: %w", name, system.ErrLocalTimerExists)
	}
	t := system.LocalTimer{Name: name, Unit: "local-" + name + ".timer", Service: "local-" + name + ".service", TimerFile: timer, ServiceFile: service, Enabled: true}
	f.own[name] = t
	return t, nil
}
func (f *fakeTimers) UpdateLocalTimer(_ context.Context, id, timer, service string) (system.LocalTimer, error) {
	t, err := f.ReadLocalTimer(id)
	if err != nil {
		return t, err
	}
	if !strings.Contains(timer, "[Timer]") {
		return system.LocalTimer{}, errors.New("the timer file has no [Timer] section")
	}
	t.TimerFile, t.ServiceFile = timer, service
	f.own[t.Name] = t
	return t, nil
}
func (f *fakeTimers) DeleteLocalTimer(_ context.Context, id string) error {
	t, err := f.ReadLocalTimer(id)
	if err != nil {
		return err
	}
	delete(f.own, t.Name)
	return nil
}
func (f *fakeTimers) RunLocalTimer(_ context.Context, id string) (string, error) {
	t, err := f.ReadLocalTimer(id)
	if err != nil {
		return "", err
	}
	f.runs = append(f.runs, t.Service)
	return "", nil
}
func (f *fakeTimers) CheckCalendar(_ context.Context, expr string) (system.CalendarCheck, error) {
	res := system.CalendarCheck{Expression: expr, Available: true}
	if expr == "bogus" {
		return res, errors.New("systemd-analyze calendar: Failed to parse calendar specification 'bogus': Invalid argument")
	}
	res.Normalized, res.Next = "*-*-* 00:00:00", []string{"Sat 2026-09-12 00:00:00 CEST", "Sun 2026-09-13 00:00:00 CEST", "Mon 2026-09-14 00:00:00 CEST"}
	return res, nil
}
func (f *fakeTimers) AnalyzeAvailable() bool { return false }

func TestLocalTimerRoutes(t *testing.T) {
	r := fakeRoot(t)
	ft := &fakeTimers{own: map[string]system.LocalTimer{}}
	svc := &fakeServices{}
	mux := http.NewServeMux()
	(&SystemAPI{Root: r, Services: svc, Timers: ft}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	const base = "/api/system/v1/timers/own"

	st, out, _ := do(t, srv, "POST", base, `{"name":"backup","timer":"[Timer]\nOnCalendar=daily\n","service":"[Service]\nExecStart=/usr/bin/logger hello\n"}`, nil)
	if st != 201 || out["unit"] != "local-backup.timer" || out["service"] != "local-backup.service" || out["timer_file"] != "[Timer]\nOnCalendar=daily\n" || out["enabled"] != true {
		t.Fatalf("create: %d %v", st, out)
	}
	if st, out, _ := do(t, srv, "POST", base, `{"name":"backup","timer":"[Timer]\n","service":"[Service]\n"}`, nil); st != 409 || out["error"] != "exists" {
		t.Errorf("duplicate: %d %v", st, out)
	}
	if st, out, _ := do(t, srv, "POST", base, `{"name":"bad name","timer":"[Timer]\n","service":"[Service]\n"}`, nil); st != 422 || out["error"] != "invalid" {
		t.Errorf("bad name: %d %v", st, out)
	}
	st, out, _ = do(t, srv, "GET", base, "", nil)
	if list, _ := out["timers"].([]any); st != 200 || len(list) != 1 || out["analyze"] != false {
		t.Errorf("list: %d %v", st, out)
	}
	// by short name and by unit name
	for _, id := range []string{"backup", "local-backup.timer"} {
		if st, out, _ := do(t, srv, "GET", base+"/"+id, "", nil); st != 200 || out["name"] != "backup" {
			t.Errorf("get %s: %d %v", id, st, out)
		}
	}
	if st, out, _ := do(t, srv, "GET", base+"/nope", "", nil); st != 404 || out["error"] != "not-found" {
		t.Errorf("get missing: %d %v", st, out)
	}
	if st, out, _ := do(t, srv, "PUT", base+"/backup", `{"timer":"[Timer]\nOnCalendar=hourly\n","service":"[Service]\nExecStart=/bin/true\n"}`, nil); st != 200 || out["timer_file"] != "[Timer]\nOnCalendar=hourly\n" {
		t.Errorf("put: %d %v", st, out)
	}
	if st, out, _ := do(t, srv, "PUT", base+"/backup", `{"timer":"[Service]\n","service":"[Service]\n"}`, nil); st != 422 || !strings.Contains(fmt.Sprint(out["message"]), "[Timer]") {
		t.Errorf("put refused: %d %v", st, out)
	}
	if st, out, _ := do(t, srv, "POST", base+"/backup/run", "", nil); st != 200 || out["ok"] != true || fmt.Sprint(ft.runs) != "[local-backup.service]" {
		t.Errorf("run: %d %v %v", st, out, ft.runs)
	}
	// the switch of an own timer is the services route with the timer's unit name
	if st, _, _ := do(t, srv, "POST", "/api/system/v1/services/local-backup.timer/disable", "", nil); st != 200 || fmt.Sprint(svc.calls) != "[local-backup.timer disable]" {
		t.Errorf("switch: %d %v", st, svc.calls)
	}
	if st, out, _ := do(t, srv, "DELETE", base+"/backup", "", nil); st != 200 || out["ok"] != true || len(ft.own) != 0 {
		t.Errorf("delete: %d %v", st, out)
	}
	if st, _, _ := do(t, srv, "DELETE", base+"/backup", "", nil); st != 404 {
		t.Errorf("delete twice: %d", st)
	}

	// the calendar check
	st, out, _ = do(t, srv, "POST", "/api/system/v1/timers/calendar", `{"expression":"daily"}`, nil)
	if next, _ := out["next"].([]any); st != 200 || out["available"] != true || out["normalized"] != "*-*-* 00:00:00" || len(next) != 3 {
		t.Errorf("calendar: %d %v", st, out)
	}
	if st, out, _ := do(t, srv, "POST", "/api/system/v1/timers/calendar", `{"expression":"bogus"}`, nil); st != 422 || out["error"] != "invalid" || !strings.Contains(fmt.Sprint(out["message"]), "Failed to parse") {
		t.Errorf("calendar bogus: %d %v", st, out)
	}

	// busybox: no own timers
	mux2 := http.NewServeMux()
	(&SystemAPI{Root: r, Services: svc}).Register(mux2)
	srv2 := httptest.NewServer(mux2)
	t.Cleanup(srv2.Close)
	for _, c := range []struct{ method, path string }{{"GET", base}, {"POST", base}, {"POST", "/api/system/v1/timers/calendar"}, {"DELETE", base + "/x"}} {
		if st, out, _ := do(t, srv2, c.method, c.path, `{}`, nil); st != 501 || out["error"] != "not-systemd" {
			t.Errorf("busybox %s %s: %d %v", c.method, c.path, st, out)
		}
	}
}

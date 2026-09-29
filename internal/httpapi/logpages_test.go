package httpapi

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/system"
)

// task 178: /log's pages. The handler asks the reader for one entry more than the page and cuts
// it, so the answer can say whether the log goes on past the page's far end - at the old end for
// the tail and a page before a cursor, at the new end for the head and a page after one.
func TestLogPages(t *testing.T) {
	journal := &recordingLog{lines: func(q system.LogQuery) []system.LogLine {
		// a boot of 5 entries, c1 (oldest) to c5; the reader answers what the query asks, up to
		// its limit, oldest first
		var all []system.LogLine
		for i := 1; i <= 5; i++ {
			all = append(all, system.LogLine{Cursor: fmt.Sprintf("s=1;i=%d", i), Message: fmt.Sprintf("line %d", i)})
		}
		idx := func(c string) int {
			for i, l := range all {
				if l.Cursor == c {
					return i
				}
			}
			return -1
		}
		switch {
		case q.Head:
			return all[:min(q.Limit, len(all))]
		case q.After != "":
			rest := all[idx(q.After)+1:]
			return rest[:min(q.Limit, len(rest))]
		case q.Before != "":
			rest := all[:idx(q.Before)]
			return rest[max(0, len(rest)-q.Limit):]
		}
		return all[max(0, len(all)-q.Limit):]
	}}
	srv := logDownloadServer(t, &SystemAPI{Root: logRoot(t), Log: journal, Journal: &system.JournalLog{}})
	// the cursors' semicolons are encoded, as URLSearchParams sends them; a raw one is dropped by
	// the query parser
	esc := url.QueryEscape
	page := func(query string) (out map[string]any, cursors string) {
		t.Helper()
		st, out, body := do(t, srv, "GET", "/api/system/v1/log?"+query, "", nil)
		if st != 200 {
			t.Fatalf("%s: %d %s", query, st, body)
		}
		var cs []string
		for _, l := range out["lines"].([]any) {
			cs = append(cs, l.(map[string]any)["cursor"].(string))
		}
		return out, strings.Join(cs, " ")
	}
	// the tail: the newest two, and the log goes on before them
	out, cs := page("limit=2")
	if cs != "s=1;i=4 s=1;i=5" || out["older"] != true || out["newer"] != false {
		t.Errorf("tail: %s %v %v", cs, out["older"], out["newer"])
	}
	if q := journal.queries()[0]; q.Limit != 3 || q.Before != "" || q.After != "" || q.Head {
		t.Errorf("tail query: %+v", q)
	}
	// before the tail: two more, older ones still there
	if out, cs = page("limit=2&before=" + esc("s=1;i=4")); cs != "s=1;i=2 s=1;i=3" || out["older"] != true || out["newer"] != true {
		t.Errorf("before: %s %v %v", cs, out["older"], out["newer"])
	}
	if q := journal.queries()[1]; q.Limit != 3 || q.Before != "s=1;i=4" {
		t.Errorf("before query: %+v", q)
	}
	// the last page before: the start of the boot
	if out, cs = page("limit=2&before=" + esc("s=1;i=2")); cs != "s=1;i=1" || out["older"] != false || out["newer"] != true {
		t.Errorf("start: %s %v %v", cs, out["older"], out["newer"])
	}
	// the head, then after it, then the end
	if out, cs = page("limit=2&head=1"); cs != "s=1;i=1 s=1;i=2" || out["older"] != false || out["newer"] != true {
		t.Errorf("head: %s %v %v", cs, out["older"], out["newer"])
	}
	if q := journal.queries()[3]; q.Limit != 3 || !q.Head {
		t.Errorf("head query: %+v", q)
	}
	if out, cs = page("limit=2&after=" + esc("s=1;i=2")); cs != "s=1;i=3 s=1;i=4" || out["older"] != true || out["newer"] != true {
		t.Errorf("after: %s %v %v", cs, out["older"], out["newer"])
	}
	if out, cs = page("limit=2&after=" + esc("s=1;i=4")); cs != "s=1;i=5" || out["older"] != true || out["newer"] != false {
		t.Errorf("end: %s %v %v", cs, out["older"], out["newer"])
	}
	// without a limit the page is 500, and the reader is asked for 501
	page("")
	if q := journal.queries()[6]; q.Limit != 501 {
		t.Errorf("default page: %+v", q)
	}
	// two page forms at once, a cursor that is not one, and a stream asked for a page backwards
	for _, bad := range []string{"/api/system/v1/log?before=" + esc("s=1;i=2") + "&head=1", "/api/system/v1/log?after=s%3D1%20%3Bi%3D2", "/api/system/v1/log?before=" + strings.Repeat("a", 256), "/api/system/v1/log/stream?before=" + esc("s=1;i=2"), "/api/system/v1/log/stream?head=1"} {
		if st, _, body := do(t, srv, "GET", bad, "", nil); st != 422 {
			t.Errorf("%s: %d %s", bad, st, body)
		}
	}
}

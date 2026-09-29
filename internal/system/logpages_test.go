package system

import (
	"context"
	"strings"
	"testing"
)

// task 178: the Log page's pages - before a cursor, after one, and the head of the boot - as
// journalctl arguments, and a page read backwards comes out oldest first like every other.
func TestJournalPageArgs(t *testing.T) {
	const c = "s=9458690b39c1457da2aa4bd978f42ffc;i=1bce;b=86da27f4a9904b46ae125e7a8a13bcd0;m=1326307da;t=65ca3835ab563;x=a20fc6b404ad791d"
	j := JournalLog{}
	for _, tc := range []struct {
		q    LogQuery
		want string
	}{
		{LogQuery{Limit: 1001, Before: c, Unit: "rfd"}, "-o json --no-pager -q -n 1001 -r --after-cursor=" + c + " -u rfd.service"},
		{LogQuery{Limit: 1001, After: c}, "-o json --no-pager -q -n 1001 --after-cursor=" + c},
		{LogQuery{Limit: 1001, Head: true, Boot: "0"}, "-o json --no-pager -q -n +1001 --boot=0"},
		// following after a cursor replays the entries since it; -n 0 stays, so nothing else is
		{LogQuery{Limit: 0, Follow: true, After: c}, "-o json --no-pager -q -n 0 --after-cursor=" + c + " -f"},
		// following takes neither a page backwards nor the head
		{LogQuery{Limit: 0, Follow: true, Before: c, Head: true}, "-o json --no-pager -q -n 0 -f"},
	} {
		if got := strings.Join(j.args(tc.q), " "); got != tc.want {
			t.Errorf("%+v:\n got %s\nwant %s", tc.q, got, tc.want)
		}
	}
}

func TestJournalPageBeforeIsOldestFirst(t *testing.T) {
	// journalctl -r prints the newest first; the page must come out oldest first
	j := JournalLog{Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		return []byte(`{"__CURSOR":"s=1;i=3","__REALTIME_TIMESTAMP":"1757350803000000","MESSAGE":"third","SYSLOG_IDENTIFIER":"rfd","PRIORITY":"6"}
{"__CURSOR":"s=1;i=2","__REALTIME_TIMESTAMP":"1757350802000000","MESSAGE":"second","SYSLOG_IDENTIFIER":"rfd","PRIORITY":"6"}
`), nil
	}}
	lines, err := j.Read(LogQuery{Limit: 2, Before: "s=1;i=4"})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0].Cursor != "s=1;i=2" || lines[1].Cursor != "s=1;i=3" {
		t.Errorf("%+v", lines)
	}
}

package system

import (
	"strings"
	"testing"
)

// task 102: a run's lines are found by OCCULITE_RUN_ID - a journalctl match ANDed with the other
// filters - and on busybox syslog by the run_id the fallback writes into the message.
func TestJournalArgsRun(t *testing.T) {
	j := JournalLog{}
	for _, tc := range []struct {
		q    LogQuery
		want string
	}{
		{LogQuery{Run: "20260912T221500-0badcafe", Limit: 1000}, "-o json --no-pager -q -n 1000 OCCULITE_RUN_ID=20260912T221500-0badcafe"},
		{LogQuery{Run: "20260912T221500-0badcafe", Severity: "err", Follow: true}, "-o json --no-pager -q -n 0 -p 3 -f OCCULITE_RUN_ID=20260912T221500-0badcafe"},
		{LogQuery{Run: "20260912T221500-0badcafe", NoKernel: true, Limit: 5}, "-o json --no-pager -q -n 5 OCCULITE_RUN_ID=20260912T221500-0badcafe _TRANSPORT=journal _TRANSPORT=stdout _TRANSPORT=syslog _TRANSPORT=driver _TRANSPORT=audit"},
		{LogQuery{Limit: 5}, "-o json --no-pager -q -n 5"},
		// task 186: one of occulited's areas, a field match like the run
		{LogQuery{Area: "acme", Tag: "occulited", Limit: 5}, "-o json --no-pager -q -n 5 -t occulited OCCULITED_AREA=acme"},
	} {
		if got := strings.Join(j.args(tc.q), " "); got != tc.want {
			t.Errorf("%+v:\n got %s\nwant %s", tc.q, got, tc.want)
		}
	}
}

// task 186: an entry's OCCULITED_AREA becomes the line's area
func TestJournalLineArea(t *testing.T) {
	l, ok := parseJournalLine([]byte(`{"MESSAGE":"order finalized","PRIORITY":"7","SYSLOG_IDENTIFIER":"occulited","OCCULITED_AREA":"acme","__REALTIME_TIMESTAMP":"1789840000000000"}`), nil)
	if !ok || l.Area != "acme" || l.Severity != "debug" || l.Tag != "occulited" || l.Message != "order finalized" {
		t.Fatalf("%v %+v", ok, l)
	}
	if l, _ := parseJournalLine([]byte(`{"MESSAGE":"x"}`), nil); l.Area != "" {
		t.Errorf("an area from nowhere: %+v", l)
	}
}

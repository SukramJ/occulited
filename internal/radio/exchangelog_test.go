package radio

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// openccu-lite task 301: the local record of adapter exchanges. The prep step notes the exchange a
// start will attempt (another module's identity on the system, none of the module in use), the
// ready step writes the entry with the outcome - the marker's cause for a rejection, the files for
// a success - and a move that does not show yet is closed by the stop after it or the next prep.
// Whether the key server took part is read from hmipserver's own lines (a local swap onto a module
// whose firmware cannot take the key, B-289, is no server exchange), local key mode from its
// configuration.
func TestAdapterExchangeRecord(t *testing.T) {
	fakeUsers(t)
	root, rec := boxRoot(t, map[string]string{"raw-uart": "GPIO@3f201000.serial"})
	d := Detector{Root: root, Run: rec.run, Sleep: func(time.Duration) {}}
	logf := func(string, ...any) {}
	ctx := context.Background()
	r, err := Run(ctx, root, d, logf)
	if err != nil {
		t.Fatal(err)
	}
	p := r.Render.Plan
	const inUse, previous = "3014F711A0001F0000000A03", "3014F711A0001F0000000A04"
	if p.HmIP == nil || p.HmIP.SGTIN != inUse {
		t.Fatalf("plan: %+v", p.HmIP)
	}
	data := filepath.Join(root, "etc/config/crRFD/data")
	fixture, _ := os.ReadFile("testdata/accesspoint.ap")
	write := func(rel string, b []byte) {
		t.Helper()
		_ = os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755)
		if err := os.WriteFile(filepath.Join(root, rel), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pending := func() *exchangePending { return readExchangePending(d) }
	write("dev/mmd_hmip", nil)
	write("dev/mmd_bidcos", nil)

	// no identity at all, or the module's own: nothing to note
	if err := Prep(ctx, d, "hmipserver", p, logf); err != nil {
		t.Fatal(err)
	}
	if pending() != nil {
		t.Fatal("a note without a previous identity")
	}
	write("etc/config/crRFD/data/"+inUse+".ap", fixture)
	if err := Prep(ctx, d, "hmipserver", p, logf); err != nil {
		t.Fatal(err)
	}
	if pending() != nil || len(ReadHmIPExchanges(root)) != 0 {
		t.Fatal("a note for the module's own identity")
	}

	// 1. the previous module's identity alone: the start attempts the exchange; the key server refuses
	if err := os.Remove(filepath.Join(data, inUse+".ap")); err != nil {
		t.Fatal(err)
	}
	write("etc/config/crRFD/data/"+previous+".ap", fixture)
	if err := Prep(ctx, d, "hmipserver", p, logf); err != nil {
		t.Fatal(err)
	}
	x := pending()
	if x == nil || x.From != previous || x.To != inUse || x.LocalKey || x.Address != p.HmIPAddressActive || x.At.IsZero() {
		t.Fatalf("note: %+v", x)
	}
	rec.answers["journalctl"] = "Init Hardware Info\nde.eq3.cbcs.server.local.base.internal.HMIPTRXInitialResponseListener [vert.x-eventloop-thread-0] Adapter exchange was rejected by key server.\n"
	if err := Ready(ctx, d, "hmipserver", 4242, logf); err == nil || !strings.Contains(err.Error(), "adapter-exchange-rejected") {
		t.Fatalf("ready: %v", err)
	}
	got := ReadHmIPExchanges(root)
	if len(got) != 1 || got[0].From != previous || got[0].To != inUse || got[0].Outcome != ExchangeRejected || got[0].Cause != CauseRefused || got[0].Mode != ExchangeModeKeyServer || got[0].Line != "Adapter exchange was rejected by key server." || got[0].Address != p.HmIPAddressActive {
		t.Fatalf("rejected entry: %+v", got)
	}
	if pending() != nil {
		t.Fatal("the note survived the entry")
	}
	// the daemon reads the record as its own user: world-readable whatever the unit's umask
	if st, err := os.Stat(exchangeRecordPath(root)); err != nil || st.Mode().Perm() != 0o644 {
		t.Fatalf("record mode: %v %v", err, st)
	}
	if st, err := os.Stat(filepath.Dir(exchangeRecordPath(root))); err != nil || st.Mode().Perm() != 0o755 {
		t.Fatalf("directory mode: %v %v", err, st)
	}

	// 2. the server was not reached: the cause comes along
	_ = os.Remove(FatalPath(root))
	if err := Prep(ctx, d, "hmipserver", p, logf); err != nil {
		t.Fatal(err)
	}
	rec.answers["journalctl"] = "Frontend Server responded with HTTP Status 503\nAdapter exchange was rejected by key server.\n"
	if err := Ready(ctx, d, "hmipserver", 4243, logf); err == nil {
		t.Fatal("ready passed")
	}
	if got = ReadHmIPExchanges(root); len(got) != 2 || got[0].Outcome != ExchangeRejected || got[0].Cause != CauseUnreachable || got[1].Cause != CauseRefused {
		t.Fatalf("newest first, unreachable: %+v", got)
	}

	// 3. accepted, but hmipserver's files show it only after the ready step: the note stays, and
	// the stop after it closes the entry from the files and the unit's journal
	_ = os.Remove(FatalPath(root))
	if err := Prep(ctx, d, "hmipserver", p, logf); err != nil {
		t.Fatal(err)
	}
	write("var/status/HMServerStarted", nil)
	rec.answers["journalctl"] = "Exchanging adapter from " + previous + " to " + inUse + "\n"
	if err := Ready(ctx, d, "hmipserver", 4244, logf); err != nil {
		t.Fatal(err)
	}
	if pending() == nil || len(ReadHmIPExchanges(root)) != 2 {
		t.Fatal("an exchange that does not show yet was closed")
	}
	// hmipserver rewrites the files when the exchange succeeds
	if err := os.Remove(filepath.Join(data, previous+".ap")); err != nil {
		t.Fatal(err)
	}
	write("etc/config/crRFD/data/"+inUse+".ap", fixture)
	rec.answers["journalctl"] = "Exchanging adapter from " + previous + " to " + inUse + "\nAdapter exchange successful.\n"
	if err := Stopped(ctx, d, "hmipserver", logf); err != nil {
		t.Fatal(err)
	}
	if got = ReadHmIPExchanges(root); len(got) != 3 || got[0].Outcome != ExchangeAccepted || got[0].Mode != ExchangeModeKeyServer || got[0].Line != "Adapter exchange successful." {
		t.Fatalf("accepted at the stop: %+v", got[0])
	}
	if !rec.called("journalctl -u hmipserver.service") || pending() != nil {
		t.Fatalf("the unit's journal was not read, or the note stayed: %v", rec.calls)
	}

	// 4. a local swap onto a module whose firmware cannot take the network key (B-289): hmipserver
	// logs success without asking the server; the record says so
	if err := os.Remove(filepath.Join(data, inUse+".ap")); err != nil {
		t.Fatal(err)
	}
	write("etc/config/crRFD/data/"+previous+".ap", fixture)
	if err := Prep(ctx, d, "hmipserver", p, logf); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(data, previous+".ap")); err != nil {
		t.Fatal(err)
	}
	write("etc/config/crRFD/data/"+inUse+".ap", fixture)
	rec.answers["journalctl"] = "Application version 1.8.3\nCould not exchange network key, adapter version not supported\nExchanging adapter from " + previous + " to " + inUse + "\nAdapter exchange successful.\n"
	write("var/status/HMServerStarted", nil) // the stop removed it
	if err := Ready(ctx, d, "hmipserver", 4245, logf); err != nil {
		t.Fatal(err)
	}
	if got = ReadHmIPExchanges(root); len(got) != 4 || got[0].Outcome != ExchangeAccepted || got[0].Mode != ExchangeModeLocalSwap || got[0].Line != "Adapter exchange successful." {
		t.Fatalf("local swap: %+v", got[0])
	}

	// 5. local key mode: offline, whatever the lines say
	write("etc/config/crRFD/hmip_user.conf", []byte("Network.Key=00112233445566778899AABBCCDDEEFF\nKeyServer.Mode=LOCAL\n"))
	if err := os.Remove(filepath.Join(data, inUse+".ap")); err != nil {
		t.Fatal(err)
	}
	write("etc/config/crRFD/data/"+previous+".ap", fixture)
	if err := Prep(ctx, d, "hmipserver", p, logf); err != nil {
		t.Fatal(err)
	}
	if x = pending(); x == nil || !x.LocalKey {
		t.Fatalf("local key mode not noted: %+v", x)
	}
	if err := os.Remove(filepath.Join(data, previous+".ap")); err != nil {
		t.Fatal(err)
	}
	write("etc/config/crRFD/data/"+inUse+".ap", fixture)
	rec.answers["journalctl"] = "Adapter exchange successful.\n"
	if err := Ready(ctx, d, "hmipserver", 4246, logf); err != nil {
		t.Fatal(err)
	}
	if got = ReadHmIPExchanges(root); len(got) != 5 || got[0].Mode != ExchangeModeLocalKey || got[0].Outcome != ExchangeAccepted {
		t.Fatalf("local key: %+v", got[0])
	}

	// 6. a note left by a start whose ready step never ran is closed by the next prep - unknown
	// when the files say nothing, and the next start gets its own note
	write("etc/config/crRFD/hmip_user.conf", nil)
	if err := os.Remove(filepath.Join(data, inUse+".ap")); err != nil {
		t.Fatal(err)
	}
	write("etc/config/crRFD/data/"+previous+".ap", fixture)
	if err := writeJSON(shadowPath(root, exchangePendingFile), exchangePending{At: d.now().Add(-time.Hour), From: previous, To: inUse}); err != nil {
		t.Fatal(err)
	}
	rec.answers["journalctl"] = ""
	if err := Prep(ctx, d, "hmipserver", p, logf); err != nil {
		t.Fatal(err)
	}
	if got = ReadHmIPExchanges(root); len(got) != 6 || got[0].Outcome != ExchangeUnknown || got[0].Mode != "" {
		t.Fatalf("stale note: %+v", got[0])
	}
	if x = pending(); x == nil || x.At.Before(d.now().Add(-time.Minute)) {
		t.Fatalf("the new start's note: %+v", x)
	}

	// a damaged line costs that entry alone
	f, _ := os.OpenFile(exchangeRecordPath(root), os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString("{not json\n")
	_ = f.Close()
	if got = ReadHmIPExchanges(root); len(got) != 6 {
		t.Fatalf("damaged line: %d entries", len(got))
	}
	if ReadHmIPExchanges(t.TempDir()) != nil {
		t.Fatal("a record without the file")
	}
}

func TestExchangeLine(t *testing.T) {
	for in, want := range map[string]string{
		"":     "",
		"a\nb": "",
		"x Adapter exchange was rejected by key server.\n":                                                       "Adapter exchange was rejected by key server.",
		"Could not exchange network key, adapter version not supported\nlogger Adapter exchange successful.  \n": "Adapter exchange successful.",
	} {
		if got := exchangeLine(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

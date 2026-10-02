package radio

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The local record of HmIP adapter exchanges (openccu-lite task 301, maintainer 2026-09-30 and
// 2026-10-01). When hmipserver starts on a module that has no identity of its own while another
// module's identity is in its data directory, it moves the HmIP network onto the module in use -
// the adapter exchange: offline in local key mode, else through eQ-3's key server, which may
// refuse it, and a module whose application firmware cannot hand the network key over is swapped
// in locally without the server (openccu-lite B-289). Nothing on the system says afterwards which
// module took which network over, when, whether the server took part and how it went; the
// Interfaces page reads it from the files of the moment and the journal, which the next move or a
// reboot takes away. So the unit's root steps keep a record: the prep step notes the exchange the
// start will attempt, the ready step (or the stop after it, or the next prep) writes the entry
// with its outcome. One JSON object per line, appended, under /etc/config - in every backup, never
// pruned, no outgoing call (D-90): the evidence for a later case, this system's or a forum
// thread's. The SGTINs and the HmIP address are no secret (hmip_address.conf is world-readable),
// so the file is 0644 and the daemon reads it as its own user.

const (
	// ExchangeRecordFile is the record, one JSON object per line, newest last.
	ExchangeRecordFile = "/etc/config/occulite/hmip-exchanges.jsonl"
	// exchangePendingFile notes the exchange a start of hmipserver will attempt, under RunDir.
	exchangePendingFile = "hmipserver.exchange"
)

// How the exchange was made.
const (
	// ExchangeModeKeyServer: through eQ-3's key server (local key mode off).
	ExchangeModeKeyServer = "key-server"
	// ExchangeModeLocalKey: offline, with the system's own network key (local key mode on).
	ExchangeModeLocalKey = "local-key"
	// ExchangeModeLocalSwap: hmipserver swapped the access point locally without asking the key
	// server, because the module's application firmware cannot take the network key over
	// ("Could not exchange network key, adapter version not supported"; openccu-lite B-289) - the
	// module then holds no network key and cannot send.
	ExchangeModeLocalSwap = "local-swap"
)

// How it went.
const (
	ExchangeAccepted = "accepted"
	ExchangeRejected = "rejected"
	// ExchangeUnknown: the files said nothing when the record was closed (hmipserver stopped or
	// was started again before the move showed in its data directory).
	ExchangeUnknown = "unknown"
)

// HmIPExchange is one entry of the record.
type HmIPExchange struct {
	At time.Time `json:"at"`
	// From is the module whose identity was on the system (several, comma-separated, when more
	// than one previous identity was there); To the module hmipserver was started on.
	From string `json:"from"`
	To   string `json:"to"`
	// Address is the HmIP network's address (hmip_address.conf) at the start, when known.
	Address string `json:"address,omitempty"`
	// Mode: key-server, local-key or local-swap; empty when the record was closed without
	// hmipserver's output and local key mode was off.
	Mode    string `json:"mode,omitempty"`
	Outcome string `json:"outcome"`
	// Cause: the rejection's cause (unreachable, refused) when rejected.
	Cause string `json:"cause,omitempty"`
	// Line is hmipserver's own word on the exchange, when it logged one.
	Line string `json:"line,omitempty"`
}

// exchangePending is the prep step's note of the exchange the start will attempt.
type exchangePending struct {
	At       time.Time `json:"at"`
	From     string    `json:"from"`
	To       string    `json:"to"`
	Address  string    `json:"address,omitempty"`
	LocalKey bool      `json:"local_key"`
}

// exchangeNeedles are hmipserver's lines about the exchange, the ones the record quotes.
var exchangeNeedles = []string{"Adapter exchange", "Could not exchange network key"}

// localSwapNeedle is the line of a local swap onto a module whose firmware cannot take the key.
const localSwapNeedle = "adapter version not supported"

// identityFiles lists the SGTINs with an access-point file in hmipserver's data directory, upper
// case, sorted. The steps run as root, so the directory (0700 hmipserver) is readable here.
func identityFiles(d Detector) []string {
	entries, err := os.ReadDir(d.path("/etc/config/crRFD/data"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := strings.ToUpper(e.Name())
		if strings.HasSuffix(name, ".AP") && len(name) == 27 {
			out = append(out, strings.TrimSuffix(name, ".AP"))
		}
	}
	sort.Strings(out)
	return out
}

func readExchangePending(d Detector) *exchangePending {
	var x exchangePending
	if err := readJSONFile(shadowPath(d.Root, exchangePendingFile), &x); err != nil || x.To == "" {
		return nil
	}
	return &x
}

// exchangeBeforeStart is the prep step's half: a note left by a start whose ready step never ran
// is closed first, from the files and the journal; then, when the module in use has no identity
// file while another module's is there, the exchange this start will attempt is noted.
func exchangeBeforeStart(ctx context.Context, d Detector, p Plan, logf func(string, ...any)) {
	if x := readExchangePending(d); x != nil {
		closeExchange(d, *x, journalSince(ctx, d, x.At), nil, true, logf)
	}
	if p.HmIP == nil || p.HmIP.SGTIN == "" {
		return
	}
	to := strings.ToUpper(p.HmIP.SGTIN)
	var from []string
	for _, sg := range identityFiles(d) {
		if sg == to {
			return
		}
		from = append(from, sg)
	}
	if len(from) == 0 {
		return
	}
	x := exchangePending{At: d.now(), From: strings.Join(from, ","), To: to, Address: p.HmIPAddressActive,
		LocalKey: ReadLocalKey(readFile(d.path("/etc/config/crRFD/hmip_user.conf"))).Enabled()}
	if err := writeJSON(shadowPath(d.Root, exchangePendingFile), x); err != nil {
		logf("prep hmipserver: the adapter exchange %s -> %s could not be noted: %v", x.From, x.To, err)
		return
	}
	logf("prep hmipserver: this start moves the HmIP network from %s onto %s (local key mode %v)", x.From, x.To, x.LocalKey)
}

// exchangeAfterStart is the ready step's half: with a known fatal error the entry is written at
// once; otherwise the files decide, and a move that does not show yet stays noted for the stop
// after it or the next prep.
func exchangeAfterStart(d Detector, output string, fatal *HmIPFatal, logf func(string, ...any)) {
	x := readExchangePending(d)
	if x == nil {
		return
	}
	closeExchange(d, *x, output, fatal, false, logf)
}

// exchangeAfterStop closes a note the ready step left open, from the files and the journal.
func exchangeAfterStop(ctx context.Context, d Detector, logf func(string, ...any)) {
	x := readExchangePending(d)
	if x == nil {
		return
	}
	closeExchange(d, *x, journalSince(ctx, d, x.At), nil, true, logf)
}

// closeExchange decides the outcome and writes the entry. Rejected: the marker's cause. Accepted:
// the module in use has its identity file and the previous module's is gone (hmipserver rewrites
// the files when the exchange succeeds). Neither: unknown when final, else the note stays.
func closeExchange(d Detector, x exchangePending, output string, fatal *HmIPFatal, final bool, logf func(string, ...any)) {
	rec := HmIPExchange{At: d.now(), From: x.From, To: x.To, Address: x.Address}
	switch {
	case fatal != nil && fatal.Code == "adapter-exchange-rejected":
		rec.Outcome, rec.Cause = ExchangeRejected, fatal.Cause
	case exchangeShows(d, x):
		rec.Outcome = ExchangeAccepted
	case final:
		rec.Outcome = ExchangeUnknown
	default:
		return
	}
	switch {
	case x.LocalKey:
		rec.Mode = ExchangeModeLocalKey
	case strings.Contains(output, localSwapNeedle):
		rec.Mode = ExchangeModeLocalSwap
	case output != "" || rec.Outcome == ExchangeRejected:
		rec.Mode = ExchangeModeKeyServer
	}
	rec.Line = exchangeLine(output)
	if err := appendExchange(d.Root, rec); err != nil {
		logf("hmipserver: the adapter exchange %s -> %s (%s) could not be recorded: %v", rec.From, rec.To, rec.Outcome, err)
		return
	}
	_ = os.Remove(shadowPath(d.Root, exchangePendingFile))
	logf("hmipserver: adapter exchange %s -> %s recorded: %s (%s)", rec.From, rec.To, rec.Outcome, rec.Mode)
}

// exchangeShows: the module in use has its access-point file and none of the previous modules has.
func exchangeShows(d Detector, x exchangePending) bool {
	have := map[string]bool{}
	for _, sg := range identityFiles(d) {
		have[sg] = true
	}
	if !have[x.To] {
		return false
	}
	for _, sg := range strings.Split(x.From, ",") {
		if have[sg] {
			return false
		}
	}
	return true
}

// exchangeLine is hmipserver's last word on the exchange in its output, the logger prefix dropped.
func exchangeLine(output string) string {
	line := ""
	for _, l := range strings.Split(output, "\n") {
		for _, n := range exchangeNeedles {
			if i := strings.Index(l, n); i >= 0 {
				line = strings.TrimSpace(l[i:])
			}
		}
	}
	return line
}

// journalSince is hmipserver's output about the exchange since a time: the unit's journal, as
// the ready step reads it by the main pid - but after a stop, or at the next start, the pid is
// gone, so the unit's lines are read instead.
func journalSince(ctx context.Context, d Detector, since time.Time) string {
	out, _ := d.run()(ctx, "journalctl", "-u", "hmipserver.service", "-o", "cat", "--no-pager", "-q", "-g", "exchange", "--since", since.Local().Format("2006-01-02 15:04:05"))
	return string(out)
}

// appendExchange appends one entry to the record, world-readable whatever the umask (the daemon
// reads it as its own user, as it reads the rejection marker; openccu-lite B-284).
func appendExchange(root string, rec HmIPExchange) error {
	p := exchangeRecordPath(root)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	_ = os.Chmod(filepath.Dir(p), 0o755)
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(b, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	_ = os.Chmod(p, 0o644)
	return werr
}

func exchangeRecordPath(root string) string {
	if root == "" || root == "/" {
		return ExchangeRecordFile
	}
	return filepath.Join(root, ExchangeRecordFile)
}

// ReadHmIPExchanges reads the record, newest first; empty without one. A line that is not an
// entry is skipped, so a damaged line costs that entry alone.
func ReadHmIPExchanges(root string) []HmIPExchange {
	f, err := os.Open(exchangeRecordPath(root))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []HmIPExchange
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var rec HmIPExchange
		if json.Unmarshal(sc.Bytes(), &rec) == nil && rec.To != "" {
			out = append(out, rec)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

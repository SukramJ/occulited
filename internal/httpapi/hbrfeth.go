package httpapi

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/hbrfeth"
	"github.com/hobbyquaker/occulited/internal/radio"
	"github.com/hobbyquaker/occulited/internal/warnings"
)

// ---- the HB-RF-ETH (openccu-lite task 218) ------------------------------------------------------
//
// A network board that carries one radio module: its IPv4 address in /etc/config/hb_rf_eth (as
// OpenCCU keeps it), the module's state from the kernel module, the board's own /sysinfo.json, and
// a Find over mDNS. Setting or removing the address kicks the radio hotplug, which connects or lets
// go of the board and re-plans the radio stack - no reboot; a board that does not answer is tried
// again in the background (radio.HBRFETHWatch) with a Status warning meanwhile.

func (a *SystemAPI) registerHBRFETH(mux *http.ServeMux, p string) {
	route(mux, auth.ScopeSystemRead, "GET "+p+"/radio/hb-rf-eth", a.hbRFETHGet)
	route(mux, auth.ScopeSystemWrite, "PUT "+p+"/radio/hb-rf-eth", a.hbRFETHPut)
	route(mux, auth.ScopeSystemWrite, "POST "+p+"/radio/hb-rf-eth/find", a.hbRFETHFind)
}

// hbRFETHView is the GET's answer.
type hbRFETHView struct {
	radio.HBRFETHStatus
	Board      *hbrfeth.Board `json:"board,omitempty"`
	BoardError string         `json:"board_error,omitempty"`
}

func (a *SystemAPI) hbRFETHClient() *hbrfeth.Client {
	if a.HBRFETHClient != nil {
		return a.HBRFETHClient
	}
	return &hbrfeth.Client{}
}

// ownAddresses are the system's IPv4 addresses, for a board's "connected to this system".
func (a *SystemAPI) ownAddresses() []string {
	if a.OwnAddresses != nil {
		return a.OwnAddresses()
	}
	var out []string
	addrs, _ := net.InterfaceAddrs()
	for _, ad := range addrs {
		if n, ok := ad.(*net.IPNet); ok && n.IP.To4() != nil {
			out = append(out, n.IP.String())
		}
	}
	return out
}

func (a *SystemAPI) hbRFETHView(ctx context.Context) hbRFETHView {
	v := hbRFETHView{HBRFETHStatus: a.HBRFETH.Status()}
	if v.Address != "" {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		b, err := a.hbRFETHClient().Info(ctx, v.Address)
		if err != nil {
			v.BoardError = err.Error()
		} else {
			b.State = hbrfeth.StateFor(b, a.ownAddresses())
			v.Board = &b
		}
	}
	return v
}

func (a *SystemAPI) hbRFETHGet(w http.ResponseWriter, r *http.Request) {
	if a.HBRFETH == nil {
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "unsupported", Message: "no radio on this system"})
		return
	}
	writeJSON(w, 200, a.hbRFETHView(r.Context()))
}

// hbRFETHPut is PUT {address, take}: an IPv4 address or "" to remove the board. A board that
// answers and is connected to another system is not taken without take=true (409 in-use: the
// last host to connect wins, and the other system loses its radio); one that does not answer is
// set all the same - it is tried again until it does.
func (a *SystemAPI) hbRFETHPut(w http.ResponseWriter, r *http.Request) {
	if a.HBRFETH == nil {
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "unsupported", Message: "no radio on this system"})
		return
	}
	var body struct {
		Address string `json:"address"`
		Take    bool   `json:"take"`
	}
	if err := readJSON(r, &body); err != nil {
		badBody(w, err)
		return
	}
	addr := strings.TrimSpace(body.Address)
	if addr != "" {
		if err := hbrfeth.ValidAddress(addr); err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: err.Error()})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		b, err := a.hbRFETHClient().Info(ctx, addr)
		cancel()
		if err == nil && !body.Take && hbrfeth.StateFor(b, a.ownAddresses()) == "other" {
			b.State = "other"
			writeJSON(w, http.StatusConflict, map[string]any{"error": "in-use", "message": "the board is connected to " + b.ConnectedTo + ": taking it takes the radio from that system", "board": b})
			return
		}
	}
	if err := a.Root.WriteHBRFETH(addr); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "write-failed", Message: err.Error()})
		return
	}
	by := ""
	if s := SessionFrom(r); s != nil {
		by = s.User
	}
	a.hbLog().Info("radio: HB-RF-ETH address set", "address", addr, "by", by)
	a.HBRFETH.Kick()
	writeJSON(w, 200, a.hbRFETHView(r.Context()))
}

// hbRFETHFind is POST /find: the boards on the LAN (mDNS, 2.5 s) and the configured one, each
// with what it says about itself and whose it is.
func (a *SystemAPI) hbRFETHFind(w http.ResponseWriter, r *http.Request) {
	if a.HBRFETH == nil {
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "unsupported", Message: "no radio on this system"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	boards, err := a.hbRFETHClient().Find(ctx, 2500*time.Millisecond, a.HBRFETH.Status().Address, a.ownAddresses())
	out := map[string]any{"boards": boards}
	if err != nil {
		out["error"] = err.Error()
	}
	writeJSON(w, 200, out)
}

func (a *SystemAPI) hbLog() *slog.Logger { return a.liteLog() }

// hb-rf-eth (task 218): a board is configured and not connected - it does not answer, or its
// module is not loaded. The variant is the address, so another board is a new warning.
func (a *SystemAPI) hbRFETHWarning(context.Context) ([]warnings.Warning, bool) {
	if a.HBRFETH == nil {
		return nil, true
	}
	st := a.HBRFETH.Status()
	if st.Address == "" || st.Connected {
		return nil, true
	}
	// B-218: a board that was connected is the kernel's to reconnect - the sentence says so
	return []warnings.Warning{{ID: "hb-rf-eth", Variant: st.Address, Severity: warnings.SeverityWarning, Href: "/system/lan-devices#hb-rf-eth", Params: map[string]any{"address": st.Address, "reconnecting": st.Reconnecting}}}, true
}

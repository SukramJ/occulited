// Package hbrfeth is what occulited knows about an HB-RF-ETH, a network board that carries one
// radio module and makes it a remote raw-uart over UDP (openccu-lite task 218): its address as
// the kernel module takes it, what the board says about itself (/sysinfo.json, readable without
// a login), and the boards on the LAN (mDNS, _raw-uart._udp).
//
// There is no pairing: the board takes the last host that connects. So a find says which host a
// board is connected to right now - this system, none, or another one - and the page asks before
// it takes a board another system uses. The board's firmware is shown as installed only: the
// update is the board's own page's (the maintainer's decision, 2026-09-24: nothing asks an
// update server for it).
package hbrfeth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// ValidAddress checks an address for the kernel module: IPv4 only (it parses the value with
// in_aton, so a host name or an IPv6 address becomes garbage), not 0.0.0.0, not a broadcast.
func ValidAddress(s string) error {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil || ip.To4() == nil || strings.Contains(s, ":") {
		return errors.New("the HB-RF-ETH is addressed by IPv4 only (the kernel module takes no name and no IPv6): four numbers such as 192.168.1.50")
	}
	ip = ip.To4()
	if ip.IsUnspecified() || ip.Equal(net.IPv4bcast) || ip.IsMulticast() || ip.IsLoopback() {
		return fmt.Errorf("%s is not a board's address", ip)
	}
	return nil
}

// Board is what an HB-RF-ETH says about itself.
type Board struct {
	Address string `json:"address"`
	Serial  string `json:"serial,omitempty"`
	// Firmware is the board's installed firmware (currentVersion)
	Firmware     string `json:"firmware,omitempty"`
	ModuleType   string `json:"module_type,omitempty"` // RPI-RF-MOD, HM-MOD-RPI-PCB, or "" for none
	ModuleSerial string `json:"module_serial,omitempty"`
	ModuleSGTIN  string `json:"module_sgtin,omitempty"`
	BidCosRadio  string `json:"bidcos_address,omitempty"`
	HmIPRadio    string `json:"hmip_address,omitempty"`
	// ConnectedTo is the host the board's radio is connected to now, "" when none
	ConnectedTo string `json:"connected_to,omitempty"`
	// State is this system's view: this (connected to this system), free, other
	State string `json:"state,omitempty"`
	// Name is the mDNS instance name when the board was found that way
	Name  string `json:"name,omitempty"`
	Error string `json:"error,omitempty"`
}

// Client reads boards. The zero value works: a 3-second HTTP timeout, mDNS on the LAN.
type Client struct {
	HTTP *http.Client
	// MDNSAddr is where the query goes: 224.0.0.251:5353 by default (a test's own responder)
	MDNSAddr string
	// BaseURL makes the sysinfo URL from an address: http://<address> by default
	BaseURL func(address string) string
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 3 * time.Second}
}

func (c *Client) base(addr string) string {
	if c.BaseURL != nil {
		return c.BaseURL(addr)
	}
	return "http://" + addr
}

// Info reads a board's /sysinfo.json.
func (c *Client) Info(ctx context.Context, address string) (Board, error) {
	b := Board{Address: address}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base(address)+"/sysinfo.json", nil)
	if err != nil {
		return b, err
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return b, fmt.Errorf("the board does not answer: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return b, fmt.Errorf("the board answered %s: not an HB-RF-ETH?", resp.Status)
	}
	var doc struct {
		SysInfo struct {
			Serial               string `json:"serial"`
			CurrentVersion       string `json:"currentVersion"`
			RawUartRemoteAddress string `json:"rawUartRemoteAddress"`
			RadioModuleType      string `json:"radioModuleType"`
			RadioModuleSerial    string `json:"radioModuleSerial"`
			RadioModuleBidCosMAC string `json:"radioModuleBidCosRadioMAC"`
			RadioModuleHmIPMAC   string `json:"radioModuleHmIPRadioMAC"`
			RadioModuleSGTIN     string `json:"radioModuleSGTIN"`
		} `json:"sysInfo"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&doc); err != nil || doc.SysInfo.Serial == "" && doc.SysInfo.CurrentVersion == "" {
		return b, errors.New("the answer is not an HB-RF-ETH's /sysinfo.json")
	}
	si := doc.SysInfo
	b.Serial, b.Firmware, b.ModuleSerial, b.ModuleSGTIN = si.Serial, si.CurrentVersion, si.RadioModuleSerial, si.RadioModuleSGTIN
	if si.RadioModuleType != "-" {
		b.ModuleType = si.RadioModuleType
	}
	b.BidCosRadio, b.HmIPRadio = radioMAC(si.RadioModuleBidCosMAC), radioMAC(si.RadioModuleHmIPMAC)
	if a := strings.TrimSpace(si.RawUartRemoteAddress); a != "" && a != "0.0.0.0" {
		b.ConnectedTo = a
	}
	return b, nil
}

// radioMAC drops the board's "0x000000" for a module without that radio.
func radioMAC(s string) string {
	s = strings.TrimSpace(s)
	if strings.Trim(strings.TrimPrefix(strings.ToLower(s), "0x"), "0") == "" {
		return ""
	}
	return s
}

// StateFor says whose a board is from this system's view: own are the system's addresses.
func StateFor(b Board, own []string) string {
	if b.ConnectedTo == "" {
		return "free"
	}
	for _, a := range own {
		if a == b.ConnectedTo {
			return "this"
		}
	}
	return "other"
}

// Service is the mDNS service an HB-RF-ETH announces.
const Service = "_raw-uart._udp.local."

// Browse asks the LAN once for HB-RF-ETH boards and collects the answers for wait: the address
// of every board, and its instance name. The query asks for unicast answers (the QU bit) from an
// ephemeral port, so nothing listens on 5353 (D-29); with the system's firewall on, answers from
// the LAN may be dropped - a typed address works regardless.
func (c *Client) Browse(ctx context.Context, wait time.Duration) (map[string]string, error) {
	dst := c.MDNSAddr
	if dst == "" {
		dst = "224.0.0.251:5353"
	}
	raddr, err := net.ResolveUDPAddr("udp4", dst)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	name, _ := dnsmessage.NewName(Service)
	q := dnsmessage.Message{Questions: []dnsmessage.Question{{Name: name, Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET | 1<<15}}}
	pkt, err := q.Pack()
	if err != nil {
		return nil, err
	}
	if _, err := conn.WriteToUDP(pkt, raddr); err != nil {
		return nil, fmt.Errorf("the mDNS query could not be sent: %w", err)
	}
	deadline := time.Now().Add(wait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetReadDeadline(deadline)
	found := map[string]string{}
	buf := make([]byte, 9000)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			break // the deadline
		}
		addr, inst, ok := parseAnswer(buf[:n])
		if !ok {
			continue
		}
		if addr == "" {
			addr = from.IP.String()
		}
		found[addr] = inst
	}
	return found, nil
}

// parseAnswer reads one mDNS answer: whether it announces the service, the instance's name, and
// the board's IPv4 when an A record rides along.
func parseAnswer(b []byte) (addr, instance string, ok bool) {
	var p dnsmessage.Parser
	if _, err := p.Start(b); err != nil {
		return "", "", false
	}
	_ = p.SkipAllQuestions()
	for {
		h, err := p.AnswerHeader()
		if err != nil {
			break
		}
		switch h.Type {
		case dnsmessage.TypePTR:
			r, err := p.PTRResource()
			if err == nil && strings.EqualFold(h.Name.String(), Service) {
				ok = true
				instance = strings.TrimSuffix(strings.TrimSuffix(r.PTR.String(), "."+Service), Service)
			}
		case dnsmessage.TypeA:
			r, err := p.AResource()
			if err == nil {
				addr = net.IP(r.A[:]).String()
			}
		default:
			_ = p.SkipAnswer()
		}
	}
	_ = p.SkipAllAuthorities()
	for {
		h, err := p.AdditionalHeader()
		if err != nil {
			break
		}
		if h.Type == dnsmessage.TypeA {
			if r, err := p.AResource(); err == nil {
				addr = net.IP(r.A[:]).String()
			}
			continue
		}
		_ = p.SkipAdditional()
	}
	return addr, instance, ok
}

// Find browses the LAN and reads every board found, the configured one too when the browse did
// not see it (a board on another subnet answers its address, not mDNS). own are the system's
// addresses, for the state.
func (c *Client) Find(ctx context.Context, wait time.Duration, configured string, own []string) ([]Board, error) {
	found, err := c.Browse(ctx, wait)
	if err != nil && configured == "" {
		return nil, err
	}
	if found == nil {
		found = map[string]string{}
	}
	if configured != "" {
		if _, ok := found[configured]; !ok {
			found[configured] = ""
		}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	out := []Board{}
	for addr, inst := range found {
		wg.Add(1)
		go func(addr, inst string) {
			defer wg.Done()
			b, err := c.Info(ctx, addr)
			b.Name = inst
			if err != nil {
				b.Error = err.Error()
			} else {
				b.State = StateFor(b, own)
			}
			mu.Lock()
			out = append(out, b)
			mu.Unlock()
		}(addr, inst)
	}
	wg.Wait()
	sort.Slice(out, func(a, b int) bool { return out[a].Address < out[b].Address })
	return out, err
}

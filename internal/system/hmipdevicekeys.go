package system

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hobbyquaker/occulited/internal/interfaces"
	"github.com/hobbyquaker/occulited/internal/radio"
)

// HmIPDeviceKeys is task 154 (D-103, D-104): the HmIP devices' own keys from their stickers in
// hmipserver's sgtin.map, so pairing a device - and its re-inclusion after a firmware update -
// needs no key server. A plain install mode pairs a mapped device; hmipserver reads the map at its
// start, so a change waits for the next HmIP-RF start (Apply). The keys stay in the map: no answer
// but Export carries one, and state.json holds only when each SGTIN last changed.
type HmIPDeviceKeys struct {
	Root     Root
	Services ServiceManager
	// StateDir holds changes.json: SGTIN → the time of its last add, change or removal (no key).
	StateDir string
	// HmIP is hmipserver's interface; false while there is none.
	HmIP func() (interfaces.Interface, bool)
	// Name is a device's name from the metadata ("" when it has none).
	Name func(ref string) string
	// Busy: a connection change or a flash is running, which a restart must not cross.
	Busy func() bool
	// Restarts is the lock shared with local key mode (B-198): whoever rewrites hmipserver's
	// configuration and restarts it holds it, so a switch and the apply never overlap. One of its
	// own when nil.
	Restarts *HmIPRestartLock
	Log      *slog.Logger
	Now      func() time.Time

	mu       sync.Mutex // the map's read-modify-write
	applying bool
	applyErr string
}

// DeviceKeyRow is one line of the view: a paired HmIP device, or a stored key that matches none.
type DeviceKeyRow struct {
	SGTIN   string `json:"sgtin,omitempty"` // empty for a paired device without a key
	Address string `json:"address,omitempty"`
	Type    string `json:"type,omitempty"`
	Name    string `json:"name,omitempty"`
	Paired  bool   `json:"paired"`
	HasKey  bool   `json:"has_key"`
	// Pending: the key was added or changed since HmIP-RF started, so HmIP-RF does not know it yet
	Pending bool `json:"pending,omitempty"`
}

// DeviceKeysView is what the section shows; it never carries a key.
type DeviceKeysView struct {
	Rows []DeviceKeyRow `json:"rows"`
	// Paired and WithKey: N of the M paired devices have a key
	Paired  int `json:"paired"`
	WithKey int `json:"with_key"`
	Stored  int `json:"stored"`
	// Pending: the changes (removals too) HmIP-RF picks up at its next start
	Pending int `json:"pending"`
	// DevicesKnown: hmipserver answered; without it every stored key is listed as not paired
	DevicesKnown bool `json:"devices_known"`
	// Unread says why HmIP-RF would not read the map at all (KeyServer.Mode=KEYSERVER)
	Unread string `json:"unread,omitempty"`
	// BadLines: lines of the map that are not an SGTIN and a 16-byte key (a hand edit)
	BadLines int    `json:"bad_lines,omitempty"`
	Applying bool   `json:"applying,omitempty"`
	Error    string `json:"error,omitempty"`
}

// ExportedKey is one entry of the key sheet.
type ExportedKey struct {
	SGTIN   string `json:"sgtin"`
	Key     string `json:"key"`
	Payload string `json:"payload"`
	Address string `json:"address,omitempty"`
	Type    string `json:"type,omitempty"`
	Name    string `json:"name,omitempty"`
}

// ErrDeviceKeys is a request that cannot be done now or is wrong; its message says why.
type ErrDeviceKeys struct{ Msg string }

func (e ErrDeviceKeys) Error() string { return e.Msg }

func (d *HmIPDeviceKeys) log() *slog.Logger {
	if d.Log != nil {
		return d.Log
	}
	return slog.Default()
}

func (d *HmIPDeviceKeys) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *HmIPDeviceKeys) changesPath() string { return filepath.Join(d.StateDir, "changes.json") }

func (d *HmIPDeviceKeys) changes() map[string]time.Time {
	out := map[string]time.Time{}
	if b, err := os.ReadFile(d.changesPath()); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func (d *HmIPDeviceKeys) saveChanges(c map[string]time.Time) {
	_ = os.MkdirAll(d.StateDir, 0o700)
	b, _ := json.MarshalIndent(c, "", "  ")
	if err := os.WriteFile(d.changesPath()+".tmp", b, 0o600); err == nil {
		_ = os.Rename(d.changesPath()+".tmp", d.changesPath())
	}
}

// keys is the map as it is on disk.
func (d *HmIPDeviceKeys) keys() ([]radio.DeviceKey, int) {
	return radio.ParseDeviceKeyMap(readFile(d.Root.join(radio.DeviceKeyMapFile)))
}

// started is when HmIP-RF last started; ok false while it does not run.
func (d *HmIPDeviceKeys) started() (time.Time, bool) {
	if d.Services == nil {
		return time.Time{}, false
	}
	list, err := d.Services.List()
	if err != nil {
		return time.Time{}, false
	}
	for _, s := range list {
		if s.ID == "hmipserver" && s.Running {
			t, err := time.Parse(time.RFC3339, s.Since)
			return t, err == nil
		}
	}
	return time.Time{}, false
}

// pendingSince: the changes HmIP-RF has not read. Those from before its start are dropped.
func (d *HmIPDeviceKeys) pending() map[string]bool {
	c := d.changes()
	if len(c) == 0 {
		return nil
	}
	since, running := d.started()
	out := map[string]bool{}
	pruned := false
	for s, at := range c {
		// Since is whole seconds: a change in the second HmIP-RF started counts as not read
		if running && at.Before(since) {
			delete(c, s)
			pruned = true
			continue
		}
		out[s] = true
	}
	if pruned {
		d.saveChanges(c)
	}
	return out
}

type pairedDevice struct{ address, typ string }

// paired is the HmIP devices hmipserver lists (the module itself and the receivers left out); nil
// when it does not answer.
func (d *HmIPDeviceKeys) paired(ctx context.Context) []pairedDevice {
	if d.HmIP == nil {
		return nil
	}
	i, ok := d.HmIP()
	if !ok {
		return nil
	}
	devices, errs := interfaces.Devices(ctx, []interfaces.Interface{i}, 10*time.Second)
	if errs[i.Name] != nil {
		return nil
	}
	out := []pairedDevice{}
	for _, dev := range devices {
		t := strings.ToUpper(dev.Type)
		if strings.Contains(dev.Address, ":") || strings.Contains(t, "RCV") || strings.Contains(t, "RPI-RF-MOD") || strings.Contains(t, "RFUSB") || strings.Contains(t, "HMIP-CCU") {
			continue
		}
		out = append(out, pairedDevice{address: strings.ToUpper(dev.Address), typ: dev.Type})
	}
	return out
}

func (d *HmIPDeviceKeys) name(address string) string {
	if d.Name == nil {
		return ""
	}
	return d.Name("HmIP-RF." + address)
}

// View joins the paired devices with the stored keys.
func (d *HmIPDeviceKeys) View(ctx context.Context) DeviceKeysView {
	d.mu.Lock()
	keys, bad := d.keys()
	applying, applyErr := d.applying, d.applyErr
	d.mu.Unlock()
	v := DeviceKeysView{Rows: []DeviceKeyRow{}, Stored: len(keys), BadLines: bad, Applying: applying, Error: applyErr}
	if mode := radio.ReadLocalKey(readFile(d.Root.join(hmipUserConf))).KeyServerMode; !radio.DeviceKeysRead(mode) {
		v.Unread = radio.ErrNoDeviceKeyMap.Error()
	}
	pend := d.pending()
	v.Pending = len(pend)
	byAddr := map[string]radio.DeviceKey{}
	for _, k := range keys {
		byAddr[radio.DeviceAddressOf(k.SGTIN)] = k
	}
	devices := d.paired(ctx)
	v.DevicesKnown = devices != nil
	matched := map[string]bool{}
	for _, dev := range devices {
		row := DeviceKeyRow{Address: dev.address, Type: dev.typ, Name: d.name(dev.address), Paired: true}
		if k, ok := byAddr[dev.address]; ok {
			row.SGTIN, row.HasKey, row.Pending = k.SGTIN, true, pend[k.SGTIN]
			matched[k.SGTIN] = true
			v.WithKey++
		}
		v.Rows = append(v.Rows, row)
	}
	v.Paired = len(devices)
	for _, k := range keys {
		if !matched[k.SGTIN] {
			v.Rows = append(v.Rows, DeviceKeyRow{SGTIN: k.SGTIN, Address: radio.DeviceAddressOf(k.SGTIN), HasKey: true, Pending: pend[k.SGTIN]})
		}
	}
	// the paired devices without a key first (what is left to scan), then by name and address
	sort.SliceStable(v.Rows, func(a, b int) bool {
		ra, rb := v.Rows[a], v.Rows[b]
		if ra.Paired != rb.Paired {
			return ra.Paired
		}
		if ra.HasKey != rb.HasKey {
			return !ra.HasKey
		}
		if ra.Name != rb.Name {
			return ra.Name < rb.Name
		}
		return ra.Address < rb.Address
	})
	return v
}

// AddResult answers an add: the entry stored and whether it is a paired device's.
type AddResult struct {
	SGTIN    string `json:"sgtin"`
	Address  string `json:"address"`
	Paired   bool   `json:"paired"`
	Replaced bool   `json:"replaced,omitempty"` // the SGTIN had another key
	Same     bool   `json:"same,omitempty"`     // the same key was stored already
}

// Add stores a key: code is a scanned or pasted QR code, or sgtin and key are typed.
func (d *HmIPDeviceKeys) Add(ctx context.Context, code, sgtin, key string) (AddResult, error) {
	var k radio.DeviceKey
	var err error
	if strings.TrimSpace(code) != "" {
		k, err = radio.ParseDeviceCode(code)
	} else {
		k, err = radio.ParseDeviceKey(sgtin, key)
	}
	if err != nil {
		return AddResult{}, ErrDeviceKeys{err.Error()}
	}
	res := AddResult{SGTIN: k.SGTIN, Address: radio.DeviceAddressOf(k.SGTIN)}
	for _, dev := range d.paired(ctx) {
		if dev.address == res.Address {
			res.Paired = true
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	keys, _ := d.keys()
	found := false
	for i := range keys {
		if keys[i].SGTIN == k.SGTIN {
			found = true
			res.Same = keys[i].Key == k.Key
			res.Replaced = !res.Same
			keys[i].Key = k.Key
		}
	}
	if res.Same {
		return res, nil
	}
	if !found {
		keys = append(keys, k)
	}
	if err := d.write(keys, k.SGTIN); err != nil {
		return AddResult{}, err
	}
	d.log().Info("hmip device keys: key stored", "sgtin", k.SGTIN, "paired", res.Paired, "replaced", res.Replaced)
	return res, nil
}

// Delete removes an SGTIN's key.
func (d *HmIPDeviceKeys) Delete(sgtin string) error {
	sgtin = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(sgtin), "-", ""))
	d.mu.Lock()
	defer d.mu.Unlock()
	keys, _ := d.keys()
	kept := keys[:0]
	for _, k := range keys {
		if k.SGTIN != sgtin {
			kept = append(kept, k)
		}
	}
	if len(kept) == len(keys) {
		return ErrDeviceKeys{"no key is stored for " + sgtin}
	}
	if err := d.write(kept, sgtin); err != nil {
		return err
	}
	d.log().Info("hmip device keys: key removed", "sgtin", sgtin)
	return nil
}

// write puts the map and, the first time, its line in hmip_user.conf; changed is recorded.
// Called with mu held.
func (d *HmIPDeviceKeys) write(keys []radio.DeviceKey, changed string) error {
	// 0640: the file holds every device key; hmipserver's start gives it to hmipserver
	if err := writeFileAtomic(d.Root.join(radio.DeviceKeyMapFile), []byte(radio.FormatDeviceKeyMap(keys)), 0o640); err != nil {
		return fmt.Errorf("writing %s: %w", radio.DeviceKeyMapFile, err)
	}
	conf := readFile(d.Root.join(hmipUserConf))
	if cur := radio.MappingFile(conf); cur != radio.DeviceKeyMapFile {
		if cur != "" {
			d.log().Warn("hmip device keys: hmip_user.conf named another map; it names this one now", "was", cur)
		}
		conf = radio.SetMappingFile(conf, radio.DeviceKeyMapFile)
		if err := writeFileAtomic(d.Root.join(hmipUserConf), []byte(conf), hmipUserConfMode(conf)); err != nil {
			return fmt.Errorf("writing hmip_user.conf: %w", err)
		}
	}
	c := d.changes()
	c[changed] = d.now()
	d.saveChanges(c)
	return nil
}

// Apply restarts HmIP-RF, which reads the map at its start; it runs in the background.
func (d *HmIPDeviceKeys) Apply() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.applying {
		return ErrDeviceKeys{"HmIP-RF is restarting already"}
	}
	if d.Busy != nil && d.Busy() {
		return ErrDeviceKeys{"a radio connection change or a firmware flash is running"}
	}
	if d.Restarts == nil {
		d.Restarts = &HmIPRestartLock{}
	}
	if held, ok := d.Restarts.Acquire("the device keys' apply"); !ok {
		return ErrDeviceKeys{held + " is running"}
	}
	d.applying, d.applyErr = true, ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		_, err := d.Services.Control(ctx, "hmipserver", "restart")
		d.mu.Lock()
		d.applying = false
		d.Restarts.Release()
		if err != nil {
			d.applyErr = "restarting HmIP-RF: " + err.Error()
			d.log().Warn("hmip device keys: restart", "err", err)
		} else {
			d.log().Info("hmip device keys: HmIP-RF restarted to read the keys")
		}
		d.mu.Unlock()
	}()
	return nil
}

// Export is every stored key with its QR payload, for the key sheet: the only answer that carries
// keys. The caller logs who took it.
func (d *HmIPDeviceKeys) Export(ctx context.Context) []ExportedKey {
	d.mu.Lock()
	keys, _ := d.keys()
	d.mu.Unlock()
	types := map[string]string{}
	for _, dev := range d.paired(ctx) {
		types[dev.address] = dev.typ
	}
	out := make([]ExportedKey, 0, len(keys))
	for _, k := range keys {
		a := radio.DeviceAddressOf(k.SGTIN)
		out = append(out, ExportedKey{SGTIN: k.SGTIN, Key: k.Key, Payload: k.Payload(), Address: a, Type: types[a], Name: d.name(a)})
	}
	return out
}

// PairedSGTINs answers, for each of the SGTINs, whether that device is a paired HmIP device now.
// ok is false when hmipserver did not answer: nothing is decided then, and a caller that clears
// something on "paired" must not clear it on a failed listing.
//
// Task 201 uses it to let a declined pairing go once the device is in: the warning cannot clear
// itself, because the line that raised it stays in the journal.
func (d *HmIPDeviceKeys) PairedSGTINs(ctx context.Context, sgtins []string) (map[string]bool, bool) {
	devices := d.paired(ctx)
	if devices == nil {
		return nil, false
	}
	have := make(map[string]bool, len(devices))
	for _, dev := range devices {
		have[dev.address] = true
	}
	out := make(map[string]bool, len(sgtins))
	for _, s := range sgtins {
		out[s] = have[radio.DeviceAddressOf(strings.ToUpper(s))]
	}
	return out, true
}

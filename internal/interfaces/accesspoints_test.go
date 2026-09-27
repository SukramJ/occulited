package interfaces

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mdzio/go-hmccu/v2/itf/xmlrpc"
)

const xmlHead = `<?xml version="1.0"?><methodResponse><params><param><value>`
const xmlTail = `</value></param></params></methodResponse>`

func member(name, value string) string {
	return `<member><name>` + name + `</name><value>` + value + `</value></member>`
}

func device(addr, typ, fw, avail string) string {
	return `<value><struct>` + member("ADDRESS", addr) + member("TYPE", typ) + member("FIRMWARE", fw) +
		member("AVAILABLE_FIRMWARE", avail) + member("PARENT", "") + `</struct></value>` +
		`<value><struct>` + member("ADDRESS", addr+":0") + member("TYPE", "MAINTENANCE") + member("PARENT", addr) + `</struct></value>`
}

// apInterface is hmipserver with the module, a HAP-B1 (TARGA's SGTIN, an entry of its own in
// listBidcosInterfaces), the Charly's DRAP (wired: no duty cycle), a HAP that has not reported
// yet (channel 0 faults) and an ordinary device.
func apInterface(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body := string(raw)
		w.Header().Set("Content-Type", "text/xml")
		fault := `<?xml version="1.0"?><methodResponse><fault><value><struct>` + member("faultCode", "<int>-5</int>") + member("faultString", "Unknown Parameter value") + `</struct></value></fault></methodResponse>`
		switch {
		case strings.Contains(body, "<methodName>listDevices</methodName>"):
			_, _ = w.Write([]byte(xmlHead + `<array><data>` +
				device("00040000000A01", "HmIP-RFUSB", "4.4.18", "0.0.0") +
				device("00030000000A13", "HmIP-HAP-B1", "2.2.18", "0.0.0") +
				device("00170000000A08", "HmIPW-DRAP", "3.0.36", "3.0.36") +
				device("000A1B2C3D4E5F", "HmIP-PDT", "2.2.4", "0.0.0") +
				device("0000000000AAAA", "HmIP-HAP", "2.2.6", "0.0.0") +
				`</data></array>` + xmlTail))
		case strings.Contains(body, "<methodName>listBidcosInterfaces</methodName>"):
			_, _ = w.Write([]byte(xmlHead + `<array><data>` +
				`<value><struct>` + member("ADDRESS", "3014F711A000040000000A01") + member("TYPE", "HMIP_CCU2") + member("CONNECTED", "<boolean>1</boolean>") + member("DUTY_CYCLE", "<i4>1</i4>") + `</struct></value>` +
				`<value><struct>` + member("ADDRESS", "30150377DC00030000000A13") + member("TYPE", "HMIP_HAP") + member("CONNECTED", "<boolean>1</boolean>") + member("DUTY_CYCLE", "<i4>4</i4>") + `</struct></value>` +
				`</data></array>` + xmlTail))
		case strings.Contains(body, "<methodName>getDeviceDescription</methodName>") && strings.Contains(body, "00170000000A08"):
			_, _ = w.Write([]byte(xmlHead + `<struct>` + member("FIRMWARE_UPDATE_STATE", "LIVE_UP_TO_DATE") + `</struct>` + xmlTail))
		case strings.Contains(body, "<methodName>getDeviceDescription</methodName>"):
			_, _ = w.Write([]byte(fault))
		case strings.Contains(body, "<methodName>getParamset</methodName>") && strings.Contains(body, "00170000000A08:0"):
			_, _ = w.Write([]byte(xmlHead + `<struct>` + member("UNREACH", "<boolean>0</boolean>") + member("IP_ADDRESS", "192.0.2.209") +
				member("CONFIG_PENDING", "<boolean>1</boolean>") + member("OPERATING_VOLTAGE", "<double>24.1</double>") + `</struct>` + xmlTail))
		case strings.Contains(body, "<methodName>getParamset</methodName>") && strings.Contains(body, "00030000000A13:0"):
			_, _ = w.Write([]byte(xmlHead + `<struct>` + member("UNREACH", "<boolean>1</boolean>") + member("IP_ADDRESS", "<string>192.0.2.155</string>") +
				member("DUTY_CYCLE_LEVEL", "<double>12.5</double>") + member("DUTY_CYCLE", "<boolean>0</boolean>") + member("CARRIER_SENSE_LEVEL", "<double>7.6</double>") +
				member("UPDATE_PENDING", "<boolean>1</boolean>") + `</struct>` + xmlTail))
		default:
			_, _ = w.Write([]byte(fault))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAccessPoints(t *testing.T) {
	srv := apInterface(t)
	ifs := FromList([]struct{ Name, URL string }{{"HmIP-RF", "xmlrpc://" + strings.TrimPrefix(srv.URL, "http://")}})
	aps, err := AccessPoints(t.Context(), ifs[0], 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(aps) != 3 {
		t.Fatalf("want the two HAPs and the DRAP, not the module or the PDT: %+v", aps)
	}
	unknown, hap, drap := aps[0], aps[1], aps[2]
	if hap.Address != "00030000000A13" || hap.Type != "HmIP-HAP-B1" || hap.Firmware != "2.2.18" || hap.IPAddress != "192.0.2.155" {
		t.Errorf("hap: %+v", hap)
	}
	if hap.Reachable == nil || *hap.Reachable || hap.DutyCycle == nil || *hap.DutyCycle != 12.5 || hap.CarrierSense == nil || *hap.CarrierSense != 8 {
		t.Errorf("hap values: %+v", hap)
	}
	if !hap.UpdatePending || hap.ConfigPending || hap.DutyCycleLimit || hap.Connected == nil || !*hap.Connected || hap.Error != "" {
		t.Errorf("hap flags: %+v", hap)
	}
	if drap.Reachable == nil || !*drap.Reachable || drap.IPAddress != "192.0.2.209" || drap.DutyCycle != nil || !drap.ConfigPending || drap.Connected != nil {
		t.Errorf("drap: %+v", drap)
	}
	if drap.FirmwareUpdateState != "LIVE_UP_TO_DATE" || drap.AvailableFirmware != "3.0.36" {
		t.Errorf("drap firmware: %+v", drap)
	}
	if unknown.Address != "0000000000AAAA" || unknown.Reachable != nil || unknown.Error == "" || unknown.Firmware != "2.2.6" {
		t.Errorf("a HAP whose channel 0 faults: %+v", unknown)
	}
	dead := FromList([]struct{ Name, URL string }{{"HmIP-RF", "xmlrpc://127.0.0.1:1"}})
	if _, err := AccessPoints(t.Context(), dead[0], time.Second); err == nil {
		t.Error("a silent interface is an error")
	}
}

// An empty channel 0 (hmipserver leaves out what it has not had yet) is "not known", not reachable.
func TestAccessPointEmptyChannel(t *testing.T) {
	ap := AccessPoint{Address: "00030000000A13"}
	fillAccessPoint(&ap, xmlrpc.Q(&xmlrpc.Value{Struct: &xmlrpc.Struct{}}))
	if ap.Reachable != nil || ap.DutyCycle != nil || ap.IPAddress != "" || ap.ConfigPending {
		t.Errorf("%+v", ap)
	}
	// a value of the wrong type is skipped without poisoning the rest
	ap = AccessPoint{}
	fillAccessPoint(&ap, xmlrpc.Q(&xmlrpc.Value{Struct: &xmlrpc.Struct{Members: []*xmlrpc.Member{
		{Name: "UNREACH", Value: &xmlrpc.Value{I4: "1"}},
		{Name: "IP_ADDRESS", Value: &xmlrpc.Value{Boolean: "1"}},
		{Name: "CONFIG_PENDING", Value: &xmlrpc.Value{Boolean: "1"}},
	}}}))
	if ap.Reachable != nil || ap.IPAddress != "" || !ap.ConfigPending {
		t.Errorf("%+v", ap)
	}
}

func TestIsLANAccessPoint(t *testing.T) {
	for typ, want := range map[string]bool{
		"HmIP-HAP": true, "HmIP-HAP-B1": true, "HmIP-HAP JS1": true, "HmIP-HAP-JS1": true, "HMIP-HAP": true, "HmIPW-DRAP": true,
		"HmIP-RFUSB": false, "RPI-RF-MOD": false, "HmIP-CCU3": false, "HmIPW-DRS8": false, "HmIP-PDT": false, "HmIP-SWSD": false,
	} {
		if IsLANAccessPoint(typ) != want {
			t.Errorf("%s: %v", typ, !want)
		}
	}
}

func TestFindAndSGTIN(t *testing.T) {
	ifs := FromList([]struct{ Name, URL string }{{"BidCos-RF", "xmlrpc://127.0.0.1:1"}, {"HmIP-RF", "xmlrpc://127.0.0.1:2"}})
	if i, err := Find(ifs, "HmIP-RF"); err != nil || i.Name != "HmIP-RF" {
		t.Errorf("%v %v", i, err)
	}
	if _, err := Find(ifs, "VirtualDevices"); err != ErrNoInterface {
		t.Errorf("%v", err)
	}
	for in, want := range map[string]string{"30150377DC00030000000A13": "00030000000A13", "3014F711A000170000000A08": "00170000000A08", "00030000000A13": "", "": ""} {
		if got := SGTINDevice(in); got != want {
			t.Errorf("%s: %q", in, got)
		}
	}
}

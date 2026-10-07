package main

import (
	"testing"

	"github.com/hobbyquaker/occulited/internal/literpc"
	"github.com/mdzio/go-hmccu/v2/itf/xmlrpc"
)

// B-46: only a write to a maintenance channel (<device>:0) has the service messages read back.
func TestMaintenanceWrite(t *testing.T) {
	for addr, want := range map[string]string{"JEQ9000001:0": "JEQ9000001", "00010000000A10:0": "00010000000A10", "JEQ9000001:1": "", "JEQ9000001:10": "", "JEQ9000001": "", ":0": "", "": ""} {
		c := literpc.Call{Method: "setValue", Params: []*xmlrpc.Value{xmlrpc.NewString(addr), xmlrpc.NewString("STICKY_UNREACH"), xmlrpc.NewBool(false)}}
		if got, ok := maintenanceWrite(c); got != want && ok || ok != (want != "") {
			t.Errorf("maintenanceWrite(%q) = %q, %v", addr, got, ok)
		}
	}
	if _, ok := maintenanceWrite(literpc.Call{Method: "setValue"}); ok {
		t.Error("a call without parameters")
	}
}

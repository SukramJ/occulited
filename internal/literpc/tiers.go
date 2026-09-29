package literpc

import (
	"strings"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/mdzio/go-hmccu/v2/itf/xmlrpc"
)

// The method tiers (task 78, D-79, D-116): what a call needs. Reading is rpc:read, setValue
// and putParamset(VALUES) rpc:operate, the configuration - paramsets, links, install mode,
// metadata, teams, the interface assignment - rpc:configure, and what deletes, re-keys, updates
// firmware or restores a device rpc:admin. A method the table does not know needs rpc:admin:
// the CCU firmware adds methods, and an unknown one must not slip through as a read. init is
// refused before the tiers (D-70). A system.multicall is checked per inner call.
var tiers = map[string]auth.Scope{
	// read
	"system.listMethods": auth.ScopeRPCRead, "system.methodHelp": auth.ScopeRPCRead, "system.methodSignature": auth.ScopeRPCRead,
	"listDevices": auth.ScopeRPCRead, "getDeviceDescription": auth.ScopeRPCRead, "getParamsetDescription": auth.ScopeRPCRead,
	"getParamset": auth.ScopeRPCRead, "getParamsetId": auth.ScopeRPCRead, "getValue": auth.ScopeRPCRead, "getLinks": auth.ScopeRPCRead,
	"getLinkInfo": auth.ScopeRPCRead, "getLinkPeers": auth.ScopeRPCRead, "getMetadata": auth.ScopeRPCRead, "getAllMetadata": auth.ScopeRPCRead,
	"listBidcosInterfaces": auth.ScopeRPCRead, "getInstallMode": auth.ScopeRPCRead, "getKeyMismatchDevice": auth.ScopeRPCRead,
	"getServiceMessages": auth.ScopeRPCRead, "listReplaceableDevices": auth.ScopeRPCRead, "getVersion": auth.ScopeRPCRead,
	"ping": auth.ScopeRPCRead, "rssiInfo": auth.ScopeRPCRead, "getLGWStatus": auth.ScopeRPCRead, "listTeams": auth.ScopeRPCRead,
	"getDeviceStatus": auth.ScopeRPCRead, "getMasterValue": auth.ScopeRPCRead, "clientServerInitialized": auth.ScopeRPCRead,
	"refreshDeployedDeviceFirmwareList": auth.ScopeRPCRead, "getRFLGWInfoLED": auth.ScopeRPCRead, "getParamsetsInfo": auth.ScopeRPCRead,
	"listAllDevices": auth.ScopeRPCRead, "getBackgroundBackupState": auth.ScopeRPCRead, "getCurrentDutyCycle": auth.ScopeRPCRead,
	// operate
	"setValue": auth.ScopeRPCOperate,
	// configure
	"putParamset": auth.ScopeRPCConfigure, "setInstallMode": auth.ScopeRPCConfigure, "addLink": auth.ScopeRPCConfigure,
	"removeLink": auth.ScopeRPCConfigure, "setLinkInfo": auth.ScopeRPCConfigure, "setMetadata": auth.ScopeRPCConfigure,
	"deleteMetadata": auth.ScopeRPCConfigure, "setBidcosInterface": auth.ScopeRPCConfigure, "setTeam": auth.ScopeRPCConfigure,
	"addDevice": auth.ScopeRPCConfigure, "activateLinkParamset": auth.ScopeRPCConfigure, "reportValueUsage": auth.ScopeRPCConfigure,
	"abortDeleteDevice": auth.ScopeRPCConfigure, "logLevel": auth.ScopeRPCConfigure, "setRFLGWInfoLED": auth.ScopeRPCConfigure,
	"setInterfaceClock": auth.ScopeRPCConfigure, "addVirtualDevice": auth.ScopeRPCConfigure, "setMasterValue": auth.ScopeRPCConfigure,
	"determineParameter": auth.ScopeRPCConfigure, "searchDevices": auth.ScopeRPCConfigure, "setTempKey": auth.ScopeRPCConfigure,
	// administer
	"deleteDevice": auth.ScopeRPCAdmin, "changeKey": auth.ScopeRPCAdmin, "restoreConfigToDevice": auth.ScopeRPCAdmin,
	"updateFirmware": auth.ScopeRPCAdmin, "installFirmware": auth.ScopeRPCAdmin, "changeDevice": auth.ScopeRPCAdmin,
	"replaceDevice": auth.ScopeRPCAdmin, "resetDevice": auth.ScopeRPCAdmin,
}

// Tier is the scope one call needs. logLevel without a parameter is a read; putParamset on
// VALUES is operating a device, on MASTER or LINK configuring it.
func Tier(c Call) auth.Scope {
	switch c.Method {
	case "logLevel":
		if len(c.Params) == 0 {
			return auth.ScopeRPCRead
		}
	case "putParamset":
		if len(c.Params) >= 2 && strings.EqualFold(xmlrpc.Q(c.Params[1]).String(), "VALUES") {
			return auth.ScopeRPCOperate
		}
	}
	if t, ok := tiers[c.Method]; ok {
		return t
	}
	return auth.ScopeRPCAdmin
}

// Known says whether the table names the method (the page can say "unknown: administer").
func Known(method string) bool { _, ok := tiers[method]; return ok }

// Methods is the tier table as it stands: every method it names and the scope a call needs,
// before the two parameter rules of Tier (the method catalogue of openccu-lite task 298).
func Methods() map[string]auth.Scope {
	out := make(map[string]auth.Scope, len(tiers))
	for m, s := range tiers {
		out[m] = s
	}
	return out
}

package httpapi

import "github.com/hobbyquaker/occulited/internal/literpc"

// Capabilities is the capability object of the open GET /api/meta/v1/version (tasks 194, 195,
// 196, 219): what a client can use before it holds a credential. pairing is whether programs may
// ask for a token. The interfaces the system runs are not in it: an open route does not say which
// radio hardware the system has; they are GET /api/rpc/v1/interfaces, behind rpc:read (task 286).
func Capabilities(pairing bool) map[string]any {
	return map[string]any{"pairing": pairing, "state": true, "history": true,
		// task 196, S1: the API versions a client checks its majors against, the stream's
		// transports and limits, and S2's typed double in the JSON path
		"apis":        map[string]int{"meta": 1, "rpc": 1, "system": 1, "auth": 1},
		"transports":  []string{"sse", "websocket"},
		"limits":      map[string]int{"streams_per_token": literpc.PerSubject, "streams_total": literpc.Total, "buffer_seconds": literpc.BufferSeconds, "buffer_events": literpc.BufferEvents},
		"json_double": true}
}

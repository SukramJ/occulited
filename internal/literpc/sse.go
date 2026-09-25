package literpc

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// sseSink is the SSE encoder: id, event and one data line per message, ": ping" as the
// heartbeat, ": connected" at once so a proxy in between opens the response.
type sseSink struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

// NewSSESink prepares the response (headers, the first comment) and returns the sink.
func NewSSESink(w http.ResponseWriter) Sink {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no") // nginx in front: do not buffer
	w.WriteHeader(http.StatusOK)
	s := sseSink{w: w, rc: http.NewResponseController(w)}
	_, _ = fmt.Fprint(w, ": connected\n\n")
	_ = s.rc.Flush()
	return s
}

func (s sseSink) write(b []byte) error {
	_ = s.rc.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if _, err := s.w.Write(b); err != nil {
		return err
	}
	return s.rc.Flush()
}

func (s sseSink) Send(id, typ string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	line := ""
	if id != "" {
		line = "id: " + id + "\n"
	}
	return s.write([]byte(line + "event: " + typ + "\ndata: " + string(b) + "\n\n"))
}

func (s sseSink) Ping() error { return s.write([]byte(": ping\n\n")) }

func (s sseSink) Close(int, string) {}

// marshalWS is the WebSocket frame's object.
func marshalWS(id, typ string, data any) ([]byte, error) {
	m := map[string]any{"type": typ, "data": data}
	if id != "" {
		m["id"] = id
	}
	return json.Marshal(m)
}

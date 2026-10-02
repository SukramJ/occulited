// Package h is the generator's test fixture: handlers in the shapes occulited's use.
package h

import (
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

type apiError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error { return json.NewDecoder(r.Body).Decode(v) }

type API struct{}

type Base struct {
	ID string `json:"id"`
}

type Level string

type Stamp struct{ t time.Time }

func (s Stamp) MarshalJSON() ([]byte, error) { return json.Marshal(s.t) }

type Code int

func (c Code) MarshalText() ([]byte, error) { return []byte("x"), nil }

type Tree struct {
	Base
	Name     string          `json:"name"`
	Note     string          `json:"note,omitempty"`
	Level    Level           `json:"level"`
	Parent   *Tree           `json:"parent"`
	Children []Tree          `json:"children,omitzero"`
	Blob     []byte          `json:"blob"`
	At       time.Time       `json:"at"`
	For      time.Duration   `json:"for"`
	Raw      json.RawMessage `json:"raw"`
	Stamp    Stamp           `json:"stamp"`
	Code     Code            `json:"code"`
	Count    int64           `json:"count,string"`
	Size     uint            `json:"size"`
	Ratio    float64         `json:"ratio"`
	On       bool            `json:"on"`
	Tags     map[string]int  `json:"tags"`
	Pair     [2]string       `json:"pair"`
	Any      any             `json:"any"`
	Skip     string          `json:"-"`
	hidden   string
	Plain    string
	Fn       func()  `json:"fn"`
	Opt      *string `json:"opt,omitempty"`
}

// tree answers a named type.
func (a *API) tree(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("fail") != "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "bad", Message: "no"})
		return
	}
	writeJSON(w, 200, Tree{})
}

// literal answers a map literal with a nested one, and one key set later.
func (a *API) literal(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"ok": true, "n": 1, "inner": map[string]any{"s": "x"}}
	if r.FormValue("more") != "" {
		out["more"] = []string{"a"}
	}
	writeJSON(w, http.StatusCreated, out)
}

// create reads an anonymous struct and answers through a helper.
func (a *API) create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		Size int    `json:"size"`
	}
	if err := readJSON(r, &body); err != nil {
		writeJSON(w, 400, apiError{Error: "invalid"})
		return
	}
	a.answer(w, body.Name)
}

func (a *API) answer(w http.ResponseWriter, name string) {
	writeJSON(w, 200, map[string]string{"name": name})
}

// decode reads with a decoder and answers 204.
func (a *API) decode(w http.ResponseWriter, r *http.Request) {
	var b Base
	_ = json.NewDecoder(r.Body).Decode(&b)
	w.WriteHeader(http.StatusNoContent)
}

// stream is server-sent events.
func (a *API) stream(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
}

// download is a file.
func (a *API) download(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/x-pem-file")
	_, _ = w.Write([]byte("x"))
}

// redirect sends the browser elsewhere.
func (a *API) redirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/", http.StatusFound)
}

// factory makes a handler.
func (a *API) factory(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 202, map[string]string{"kind": kind})
	}
}

// computed writes an error with a computed status, which is no answer.
func (a *API) computed(w http.ResponseWriter, _ *http.Request) {
	code := 500
	writeJSON(w, code, apiError{Error: "x"})
}

// filters reads query parameters outside the handler, from the request's url.Values (B-33)
func filters(v url.Values) string { return v.Get("tag") + page(v) }

func page(v url.Values) string { return v.Get("before") }

// Error collides with the document's own Error component.
type Error struct {
	Why string `json:"why"`
}

// multi answers two shapes, a var-declared map with a key set to two things and a nested map
// variable.
func (a *API) multi(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Has("e") {
		writeJSON(w, 200, Error{})
		return
	}
	_ = filters(r.URL.Query())
	inner := map[string]any{"deep": 1}
	var out = map[string]any{"inner": inner}
	out["v"] = 1
	out["v"] = "one"
	writeJSON(w, 200, out)
	writeJSON(w, 200, out)
}

// viewed answers a view built by another function.
func (a *API) viewed(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, a.view(r.URL.Query().Has("b")))
}

func (a *API) view(b bool) map[string]any {
	if b {
		return map[string]any{"b": "s"}
	}
	return map[string]any{"a": 1}
}

// convert names a message and builds it, as literpc's does.
func convert(kind string) (string, map[string]any) {
	if kind == "a" {
		return "alpha", map[string]any{"x": 1}
	}
	typ := kind + "!"
	return typ, map[string]any{"addresses": []string{}}
}

// Run sends through a callback, as literpc's does.
func Run(send func(id, typ string, data any) bool) {
	hello := map[string]any{"boot": "b"}
	hello["seq"] = 1
	send("1", "hello", hello)
	resync := func() { send("", "resync", map[string]any{"reason": "gap"}) }
	resync()
	typ, data := convert("a")
	send("2", typ, data)
}

// Handlers hands out the method values as a mux would get them.
func (a *API) Handlers() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"tree": a.tree, "literal": a.literal, "create": a.create, "decode": a.decode, "stream": a.stream,
		"download": a.download, "redirect": a.redirect, "factory": a.factory("x"), "computed": a.computed, "multi": a.multi, "viewed": a.viewed,
	}
}

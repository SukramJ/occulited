package apidoc

import (
	"encoding/json"
	"strings"
	"testing"
)

const fixture = "github.com/hobbyquaker/occulited/internal/apidoc/testdata/h"

func handler(name string) string { return fixture + ".(*API)." + name + "-fm" }

func fixtureDoc(t *testing.T, routes []Route) map[string]any {
	t.Helper()
	a, err := Load(".", "./testdata/h")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := a.OpenAPI(Info{Title: "t", Version: "dev"}, fixture, routes)
	if err != nil {
		t.Fatal(err)
	}
	// through JSON, so the test reads what is committed
	b, err := Encode(doc)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// at walks a decoded document: at(doc, "paths", "/x", "get").
func at(t *testing.T, v any, path ...string) any {
	t.Helper()
	for _, p := range path {
		m, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("%v: not an object at %q", path, p)
		}
		if v, ok = m[p]; !ok {
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			t.Fatalf("%v: no %q (have %v)", path, p, keys)
		}
	}
	return v
}

func js(v any) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return strings.TrimSuffix(b.String(), "\n")
}

func TestOpenAPIFromHandlers(t *testing.T) {
	routes := []Route{
		{Method: "GET", Path: "/api/x/v1/tree/{id}", Scopes: []string{"x:read"}, Handler: handler("tree"), Doc: Doc{Summary: "tree"}},
		{Method: "POST", Path: "/api/x/v1/literal", Scopes: []string{"x:write", "x:read"}, Handler: handler("literal"), Doc: Doc{Summary: "literal"}},
		{Method: "POST", Path: "/api/x/v1/create", Scopes: []string{""}, Handler: handler("create"), Doc: Doc{Summary: "create", Query: map[string]string{"dry": "a dry run"}}},
		{Method: "PUT", Path: "/api/x/v1/decode/{path...}", Scopes: []string{"x:write"}, Handler: handler("decode"), Doc: Doc{Summary: "decode"}},
		{Method: "GET", Path: "/api/x/v1/stream", Scopes: []string{"x:read"}, Handler: handler("stream"), Doc: Doc{Summary: "stream"}},
		{Method: "GET", Path: "/api/x/v1/download", Scopes: []string{"x:read"}, Handler: handler("download"), Doc: Doc{Summary: "download"}},
		{Method: "GET", Path: "/api/x/v1/redirect", Scopes: []string{""}, Handler: handler("redirect"), Doc: Doc{Summary: "redirect"}},
		{Method: "POST", Path: "/api/x/v1/factory", Scopes: []string{"x:write"}, Handler: fixture + ".(*API).factory.func1", Doc: Doc{Summary: "factory"}},
		{Method: "GET", Path: "/api/x/v1/computed", Scopes: []string{"x:read"}, Handler: handler("computed"), Doc: Doc{Summary: "computed"}},
		{Method: "GET", Path: "/api/x/v1/ws", Scopes: []string{"x:read"}, Doc: Doc{Summary: "ws", WebSocket: true}},
		{Method: "POST", Path: "/api/x/v1/upload", Scopes: []string{"x:write"}, Doc: Doc{Summary: "upload", ReqContent: "application/octet-stream", RespContent: "text/plain", Req: fixture + ".Base"}},
		{Method: "GET", Path: "/api/x/v1/typed", Scopes: []string{"x:read"}, Doc: Doc{Summary: "typed", Resp: fixture + ".Base", Status: 201}},
		{Method: "GET", Path: "/api/x/v1/sse", Scopes: []string{"x:read"}, Doc: Doc{Summary: "sse", Stream: true}},
		{Method: "GET", Path: "/api/x/v1/multi", Scopes: []string{"x:read"}, Handler: handler("multi"), Doc: Doc{Summary: "multi"}},
		{Method: "GET", Path: "/api/x/v1/viewed", Scopes: []string{"x:read"}, Handler: handler("viewed"), Doc: Doc{Summary: "viewed"}},
		{Method: "GET", Path: "/health", Doc: Doc{Summary: "health", RespSchema: Schema{"type": "object"}}},
	}
	doc := fixtureDoc(t, routes)
	if at(t, doc, "openapi") != "3.1.1" {
		t.Error("not 3.1")
	}
	schemas := at(t, doc, "components", "schemas").(map[string]any)

	// a named struct: a component, fields as encoding/json sees them
	tree := at(t, doc, "paths", "/api/x/v1/tree/{id}", "get")
	if got := js(at(t, tree, "responses", "200", "content", "application/json", "schema")); got != `{"$ref":"#/components/schemas/Tree"}` {
		t.Errorf("tree answer: %s", got)
	}
	props := at(t, schemas, "Tree", "properties").(map[string]any)
	for k, want := range map[string]string{
		"id":       `{"type":"string"}`, // embedded, flattened
		"parent":   `{"anyOf":[{"$ref":"#/components/schemas/Tree"},{"type":"null"}]}`,
		"children": `{"items":{"$ref":"#/components/schemas/Tree"},"type":["array","null"]}`,
		"blob":     `{"contentEncoding":"base64","type":"string"}`,
		"at":       `{"format":"date-time","type":"string"}`,
		"for":      `{"description":"nanoseconds","type":"integer"}`,
		"raw":      `{}`,
		"stamp":    `{}`,
		"code":     `{"type":"string"}`,
		"count":    `{"type":"string"}`,
		"size":     `{"minimum":0,"type":"integer"}`,
		"ratio":    `{"type":"number"}`,
		"on":       `{"type":"boolean"}`,
		"level":    `{"type":"string"}`,
		"tags":     `{"additionalProperties":{"type":"integer"},"type":["object","null"]}`,
		"pair":     `{"items":{"type":"string"},"maxItems":2,"minItems":2,"type":"array"}`,
		"any":      `{}`,
		"Plain":    `{"type":"string"}`,
		"fn":       `{"not":{}}`,
		"opt":      `{"type":["string","null"]}`,
	} {
		if got := js(props[k]); got != want {
			t.Errorf("Tree.%s: %s, want %s", k, got, want)
		}
	}
	for _, k := range []string{"Skip", "hidden", "-"} {
		if _, ok := props[k]; ok {
			t.Errorf("Tree has %s", k)
		}
	}
	req := js(at(t, schemas, "Tree", "required"))
	if strings.Contains(req, `"note"`) || strings.Contains(req, `"children"`) || !strings.Contains(req, `"id"`) || !strings.Contains(req, `"name"`) {
		t.Errorf("Tree required: %s", req)
	}
	if got := js(at(t, tree, "parameters")); !strings.Contains(got, `"in":"path","name":"id","required":true`) || !strings.Contains(got, `"in":"query","name":"fail"`) {
		t.Errorf("tree parameters: %s", got)
	}
	if got := js(at(t, tree, "security")); got != `[{"bearer":["x:read"]},{"cookie":["x:read"]}]` {
		t.Errorf("tree security: %s", got)
	}
	if at(t, tree, "operationId") != "getXTreeById" || js(at(t, tree, "tags")) != `["x/tree"]` {
		t.Errorf("tree id/tags: %v %v", at(t, tree, "operationId"), at(t, tree, "tags"))
	}
	for _, st := range []string{"401", "403", "default"} {
		at(t, tree, "responses", st)
	}
	if _, ok := at(t, tree, "responses").(map[string]any)["400"]; ok {
		t.Error("an error status is documented as an answer")
	}

	// a map literal: its keys, a nested literal, a key set later (optional), the status
	lit := at(t, doc, "paths", "/api/x/v1/literal", "post")
	s := at(t, lit, "responses", "201", "content", "application/json", "schema")
	if got := js(s); got != `{"properties":{"inner":{"properties":{"s":{"type":"string"}},"required":["s"],"type":"object"},"more":{"items":{"type":"string"},"type":["array","null"]},"n":{"type":"integer"},"ok":{"type":"boolean"}},"required":["inner","n","ok"],"type":"object"}` {
		t.Errorf("literal: %s", got)
	}
	if got := js(at(t, lit, "security")); got != `[{"bearer":["x:write"]},{"cookie":["x:write"]},{"bearer":["x:read"]},{"cookie":["x:read"]}]` {
		t.Errorf("two scopes: %s", got)
	}
	if got := js(at(t, lit, "x-occulite-scopes")); got != `["x:write","x:read"]` {
		t.Errorf("x-occulite-scopes: %s", got)
	}

	// an anonymous request struct (no required fields), an answer from a helper, an open route
	cr := at(t, doc, "paths", "/api/x/v1/create", "post")
	if got := js(at(t, cr, "requestBody", "content", "application/json", "schema")); got != `{"properties":{"name":{"type":"string"},"size":{"type":"integer"}},"type":"object"}` {
		t.Errorf("create request: %s", got)
	}
	if got := js(at(t, cr, "responses", "200", "content", "application/json", "schema")); got != `{"properties":{"name":{"type":"string"}},"required":["name"],"type":"object"}` {
		t.Errorf("create answer: %s", got)
	}
	if js(at(t, cr, "security")) != `[]` {
		t.Error("an open route has security")
	}
	if _, ok := at(t, cr, "responses").(map[string]any)["401"]; ok {
		t.Error("an open route documents 401")
	}
	if got := js(at(t, cr, "parameters")); got != `[{"description":"a dry run","in":"query","name":"dry","required":false,"schema":{"type":"string"}}]` {
		t.Errorf("create parameters: %s", got)
	}

	// a decoder, a request type named for its direction, a 204, {path...}
	dec := at(t, doc, "paths", "/api/x/v1/decode/{path}", "put")
	if got := js(at(t, dec, "requestBody", "content", "application/json", "schema")); got != `{"$ref":"#/components/schemas/BaseInput"}` {
		t.Errorf("decode request: %s", got)
	}
	if _, ok := at(t, schemas, "BaseInput").(map[string]any)["required"]; ok {
		t.Error("a request schema names required fields")
	}
	if got := js(at(t, dec, "responses", "204")); got != `{"description":"No content"}` {
		t.Errorf("decode 204: %s", got)
	}
	if got := js(at(t, dec, "parameters")); !strings.Contains(got, "slashes") {
		t.Errorf("{path...}: %s", got)
	}

	if got := js(at(t, doc, "paths", "/api/x/v1/stream", "get", "responses", "200", "content")); !strings.Contains(got, "text/event-stream") {
		t.Errorf("stream: %s", got)
	}
	if got := js(at(t, doc, "paths", "/api/x/v1/sse", "get", "responses", "200", "content")); !strings.Contains(got, "text/event-stream") {
		t.Errorf("sse by hand: %s", got)
	}
	if got := js(at(t, doc, "paths", "/api/x/v1/download", "get", "responses", "200", "content")); got != `{"application/x-pem-file":{"schema":{"contentMediaType":"application/x-pem-file","type":"string"}}}` {
		t.Errorf("download: %s", got)
	}
	at(t, doc, "paths", "/api/x/v1/redirect", "get", "responses", "302")
	if got := js(at(t, doc, "paths", "/api/x/v1/factory", "post", "responses", "202", "content", "application/json", "schema")); !strings.Contains(got, `"kind"`) {
		t.Errorf("factory: %s", got)
	}
	if got := js(at(t, doc, "paths", "/api/x/v1/computed", "get", "responses", "200")); got != `{"description":"OK"}` {
		t.Errorf("computed: %s", got)
	}
	at(t, doc, "paths", "/api/x/v1/ws", "get", "responses", "101")
	up := at(t, doc, "paths", "/api/x/v1/upload", "post")
	if got := js(at(t, up, "requestBody", "content")); got != `{"application/octet-stream":{"schema":{"contentMediaType":"application/octet-stream","type":"string"}}}` {
		t.Errorf("upload request: %s", got)
	}
	if got := js(at(t, up, "responses", "200", "content")); !strings.Contains(got, "text/plain") {
		t.Errorf("upload answer: %s", got)
	}
	if got := js(at(t, doc, "paths", "/api/x/v1/typed", "get", "responses", "201", "content", "application/json", "schema")); got != `{"$ref":"#/components/schemas/Base"}` {
		t.Errorf("typed: %s", got)
	}
	if got := js(at(t, doc, "paths", "/health", "get", "tags")); got != `["api"]` {
		t.Errorf("health tags: %s", got)
	}
	multi := js(at(t, doc, "paths", "/api/x/v1/multi", "get", "responses", "200", "content", "application/json", "schema"))
	if multi != `{"anyOf":[{"$ref":"#/components/schemas/Error2"},{"properties":{"inner":{"properties":{"deep":{"type":"integer"}},"required":["deep"],"type":"object"},"v":{"anyOf":[{"type":"integer"},{"type":"string"}]}},"required":["inner"],"type":"object"}]}` {
		t.Errorf("multi: %s", multi)
	}
	if got := js(at(t, doc, "paths", "/api/x/v1/viewed", "get", "responses", "200", "content", "application/json", "schema")); got != `{"anyOf":[{"properties":{"a":{"type":"integer"}},"required":["a"],"type":"object"},{"properties":{"b":{"type":"string"}},"required":["b"],"type":"object"}]}` {
		t.Errorf("viewed: %s", got)
	}
	if got := js(at(t, schemas, "Error", "required")); got != `["error","message"]` {
		t.Errorf("Error: %s", got)
	}
	tags := js(at(t, doc, "tags"))
	if !strings.Contains(tags, `{"description":"The routes under `+"`/api/x/v1/tree/{id}`"+`.","name":"x/tree"}`) {
		t.Errorf("tags: %s", tags)
	}
}

func TestOpenAPIRefusals(t *testing.T) {
	a, err := Load(".", "./testdata/h")
	if err != nil {
		t.Fatal(err)
	}
	info := Info{Title: "t", Version: "dev"}
	for name, routes := range map[string][]Route{
		"no summary":      {{Method: "GET", Path: "/a", Doc: Doc{}}},
		"unknown handler": {{Method: "GET", Path: "/a", Handler: fixture + ".(*API).nope-fm", Doc: Doc{Summary: "a"}}},
		"unknown type":    {{Method: "GET", Path: "/a", Doc: Doc{Summary: "a", Resp: fixture + ".Nope"}}},
		"bad type name":   {{Method: "GET", Path: "/a", Doc: Doc{Summary: "a", Req: "Nope"}}},
		"same operation":  {{Method: "GET", Path: "/api/x/v1/a-b", Doc: Doc{Summary: "a"}}, {Method: "GET", Path: "/api/x/v1/a_b", Doc: Doc{Summary: "a"}}},
	} {
		if _, err := a.OpenAPI(info, fixture, routes); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := a.OpenAPI(info, "example.com/none", nil); err == nil {
		t.Error("a home without apiError: no error")
	}
	if _, err := Load(".", "./testdata/none"); err == nil {
		t.Error("a package that does not exist loaded")
	}
}

func TestOperationID(t *testing.T) {
	for in, want := range map[string]string{
		"GET /api/system/v1/wifi/networks/{ssid}":          "getSystemWifiNetworksBySsid",
		"POST /api/meta/v1/objects:bulk":                   "postMetaObjectsBulk",
		"DELETE /api/meta/v1/enums/{enum}/nodes/{path...}": "deleteMetaEnumsByEnumNodesByPath",
		"GET /api/openapi.json":                            "getOpenapiJson",
		"POST /api/system/v1/boot-timing":                  "postSystemBootTiming",
	} {
		m, p, _ := strings.Cut(in, " ")
		if got := OperationID(m, p); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}

func TestNullable(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{`{}`, `{}`},
		{`{"type":"string"}`, `{"type":["string","null"]}`},
		{`{"type":["array","null"]}`, `{"type":["array","null"]}`},
		{`{"type":["array"]}`, `{"type":["array","null"]}`},
		{`{"$ref":"#/x"}`, `{"anyOf":[{"$ref":"#/x"},{"type":"null"}]}`},
	} {
		var s Schema
		_ = json.Unmarshal([]byte(c.in), &s)
		if got := js(nullable(s)); got != c.want {
			t.Errorf("nullable(%s) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestAsyncAPI(t *testing.T) {
	a, err := Load(".", "./testdata/h")
	if err != nil {
		t.Fatal(err)
	}
	sent, err := a.Calls(fixture+".Run", "send", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, m := range sent {
		names = append(names, m.Name+":"+strings.Join(m.Body.Keys(), ","))
	}
	if got := strings.Join(names, " "); got != "hello:boot,seq resync:reason :" {
		t.Errorf("Calls: %s", got)
	}
	ret, err := a.Returns(fixture+".convert", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(ret) != 2 || ret[0].Name != "alpha" || ret[1].Name != "" || js(ret[1].Body.Keys()) != `["addresses"]` {
		t.Errorf("Returns: %+v", ret)
	}
	if (Body{}).Keys() != nil {
		t.Error("a typed body has keys")
	}
	for _, fn := range []func() error{
		func() error { _, err := a.Calls(fixture+".nope", "send", 0, 1); return err },
		func() error { _, err := a.Returns(fixture+".nope", 0, 1); return err },
	} {
		if fn() == nil {
			t.Error("an unknown function: no error")
		}
	}
	msgs := []ChannelMessage{{Name: "hello", Summary: "hi", Bodies: []Body{sent[0].Body}}, {Name: "resync", Payload: Schema{"type": "object"}}}
	doc, err := a.AsyncAPI(Info{Title: "t", Version: "dev", License: map[string]any{"name": "GPL-3.0-only", "identifier": "GPL-3.0-only"}}, fixture, []Channel{
		{ID: "events", Address: "/events", Scopes: []string{"x:read"}, Summary: "events", Messages: msgs},
		{ID: "ws", Address: "/ws", WebSocket: true, Scopes: []string{"x:read"}, Summary: "ws", Description: "frames", Messages: msgs},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Encode(doc)
	var d map[string]any
	_ = json.Unmarshal(b, &d)
	if at(t, d, "asyncapi") != "3.0.0" || js(at(t, d, "info", "license")) != `{"name":"GPL-3.0-only","url":"https://spdx.org/licenses/GPL-3.0-only.html"}` {
		t.Errorf("head: %v %v", at(t, d, "asyncapi"), at(t, d, "info", "license"))
	}
	if got := js(at(t, d, "components", "messages", "eventsHello", "payload")); got != `{"properties":{"boot":{"type":"string"},"seq":{"type":"integer"}},"required":["boot"],"type":"object"}` {
		t.Errorf("sse payload: %s", got)
	}
	if got := js(at(t, d, "components", "messages", "wsResync", "payload")); got != `{"properties":{"data":{"type":"object"},"id":{"description":"The event id to resume after (`+"`<boot>:<seq>`"+`), where the message has one.","type":"string"},"type":{"const":"resync"}},"required":["type","data"],"type":"object"}` {
		t.Errorf("ws payload: %s", got)
	}
	if got := js(at(t, d, "channels", "ws", "servers")); got != `[{"$ref":"#/servers/ws"}]` {
		t.Errorf("ws server: %s", got)
	}
	if at(t, d, "operations", "receiveEvents", "description") != "events." || at(t, d, "operations", "receiveWs", "description") != "frames" {
		t.Error("operation descriptions")
	}
	if got := js(at(t, d, "operations", "receiveEvents", "x-occulite-scopes")); got != `["x:read"]` {
		t.Errorf("scopes: %s", got)
	}
	for name, chans := range map[string][]Channel{
		"incomplete": {{ID: "x", Address: "/x", Summary: "x"}},
		"twice":      {{ID: "x", Address: "/x", Summary: "x", Messages: []ChannelMessage{{Name: "a", Payload: Schema{}}, {Name: "a", Payload: Schema{}}}}},
		"no payload": {{ID: "x", Address: "/x", Summary: "x", Messages: []ChannelMessage{{Name: "a"}}}},
	} {
		if _, err := a.AsyncAPI(Info{}, fixture, chans); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

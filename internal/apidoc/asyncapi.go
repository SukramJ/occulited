package apidoc

import (
	"fmt"
	"sort"
)

// Channel is one event stream: a route that answers server-sent events or upgrades to a WebSocket.
type Channel struct {
	ID          string   // the channel's key, e.g. liteEvents
	Address     string   // the route's path
	WebSocket   bool     // a WebSocket; otherwise server-sent events
	Scopes      []string // the route's scopes from the table
	Summary     string
	Description string
	Messages    []ChannelMessage
}

// ChannelMessage is one kind of message on a channel: its name (the SSE `event:` field, the
// WebSocket frame's `type`; "message" for SSE data without an event name) and its payload.
type ChannelMessage struct {
	Name, Summary string
	Bodies        []Body // the payload's shapes, from the analyzer (Calls, Returns, Handler)
	Payload       Schema // or a schema written by hand
}

// AsyncAPI builds the AsyncAPI 3.0 document of the streams. A WebSocket channel's payload is its
// frame, {type, id?, data}; an SSE channel's is the data line.
func (a *Analyzer) AsyncAPI(info Info, home string, channels []Channel) (map[string]any, error) {
	comps := NewComponents("#/components/schemas/", home)
	chans := map[string]any{}
	ops := map[string]any{}
	msgs := map[string]any{}
	for _, ch := range channels {
		if ch.ID == "" || ch.Address == "" || ch.Summary == "" || len(ch.Messages) == 0 {
			return nil, fmt.Errorf("apidoc: channel %q is incomplete", ch.ID)
		}
		server := "http"
		if ch.WebSocket {
			server = "ws"
		}
		chMsgs := map[string]any{}
		var opMsgs []any
		names := map[string]bool{}
		for _, m := range ch.Messages {
			if names[m.Name] {
				return nil, fmt.Errorf("apidoc: channel %s has message %s twice", ch.ID, m.Name)
			}
			names[m.Name] = true
			payload := m.Payload
			if payload == nil {
				payload = comps.Distinct(m.Bodies, Response)
			}
			if payload == nil {
				return nil, fmt.Errorf("apidoc: channel %s message %s has no payload", ch.ID, m.Name)
			}
			if ch.WebSocket {
				payload = Schema{"type": "object", "required": []string{"type", "data"}, "properties": Schema{
					"type": Schema{"const": m.Name},
					"id":   Schema{"type": "string", "description": "The event id to resume after (`<boot>:<seq>`), where the message has one."},
					"data": payload,
				}}
			}
			key := ch.ID + upperFirst(m.Name)
			msg := map[string]any{"name": m.Name, "payload": payload, "contentType": "application/json"}
			if m.Summary != "" {
				msg["summary"] = m.Summary
			}
			msgs[key] = msg
			chMsgs[m.Name] = map[string]any{"$ref": "#/components/messages/" + key}
			opMsgs = append(opMsgs, map[string]any{"$ref": "#/channels/" + ch.ID + "/messages/" + m.Name})
		}
		c := map[string]any{
			"address":  ch.Address,
			"title":    ch.Summary,
			"messages": chMsgs,
			"servers":  []any{map[string]any{"$ref": "#/servers/" + server}},
		}
		if ch.Description != "" {
			c["description"] = ch.Description
		}
		chans[ch.ID] = c
		sort.Slice(opMsgs, func(i, j int) bool { return fmt.Sprint(opMsgs[i]) < fmt.Sprint(opMsgs[j]) })
		ops["receive"+upperFirst(ch.ID)] = map[string]any{
			"action":            "receive",
			"channel":           map[string]any{"$ref": "#/channels/" + ch.ID},
			"summary":           ch.Summary,
			"description":       orSummary(ch.Description, ch.Summary),
			"messages":          opMsgs,
			"security":          []any{map[string]any{"$ref": "#/components/securitySchemes/bearer"}, map[string]any{"$ref": "#/components/securitySchemes/cookie"}},
			"x-occulite-scopes": ch.Scopes,
		}
	}
	host := map[string]any{"host": map[string]any{"default": "ccu.local", "description": "The system's name or address."}}
	doc := map[string]any{
		"asyncapi": "3.0.0",
		"info": map[string]any{"title": info.Title, "version": info.Version, "description": info.Description,
			"license": asyncLicense(info.License), "contact": info.Contact, "tags": []any{map[string]any{"name": "occulited"}}},
		"defaultContentType": "application/json",
		"servers": map[string]any{
			"http": map[string]any{"host": "{host}", "protocol": "https", "variables": host,
				"description": "Server-sent events over the web port (`text/event-stream`); `: ping` comments keep the connection."},
			"ws": map[string]any{"host": "{host}", "protocol": "wss", "variables": host,
				"description": "A WebSocket over the web port: one JSON object per text frame; ping frames keep the connection."},
		},
		"channels":   chans,
		"operations": ops,
		"components": map[string]any{
			"messages": msgs,
			"schemas":  comps.Schemas(),
			"securitySchemes": map[string]any{
				"bearer": map[string]any{"type": "http", "scheme": "bearer",
					"description": "A session id or an API token; the channel's scope is in x-occulite-scopes."},
				"cookie": map[string]any{"type": "httpApiKey", "in": "cookie", "name": "occulite_session",
					"description": "The web UI's session cookie; lite-rpc's streams also want the request from the system's own origin: Sec-Fetch-Site same-origin, an Origin that is this host, or - from a page over plain HTTP, where a browser sends neither - the header X-Occulite-Request, which an EventSource cannot set (system-api.md, lite-rpc's credentials)."},
			},
		},
	}
	return doc, nil
}

// asyncLicense is the license object AsyncAPI takes: name and url; OpenAPI's SPDX identifier
// becomes SPDX's page of it.
func asyncLicense(l map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range l {
		if k == "identifier" {
			out["url"] = fmt.Sprintf("https://spdx.org/licenses/%v.html", v)
			continue
		}
		out[k] = v
	}
	return out
}

func orSummary(desc, summary string) string {
	if desc != "" {
		return desc
	}
	return summary + "."
}

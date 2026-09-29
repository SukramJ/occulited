// Package apidoc writes occulited's API documents (openccu-lite task 298): the OpenAPI 3.1
// document of the REST API, the AsyncAPI 3.0 document of its event streams, and the lite-rpc
// method catalogue. It is a generator the tests run, never part of the binary: the documents are
// committed under docs/, and occulited serves the committed OpenAPI document.
//
// The types come from the handlers' own source. The route table says which handler serves a
// route; the analyzer reads that handler (and the same-package functions it hands the response
// writer or the request to) with go/types and finds what it reads from the request body
// (readJSON), which query parameters it asks for, and what it answers with writeJSON on a
// success status - a named type, an anonymous struct or a map literal, whose keys and value types
// are as good as a struct's. So a document cannot drift from the code: changing a handler's answer
// changes the generated document, and CI fails until the committed one follows.
package apidoc

import (
	"go/types"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Schema is one JSON Schema (2020-12, the dialect OpenAPI 3.1 and AsyncAPI 3.0 embed). A map, so
// any keyword fits and encoding/json writes the keys sorted - the committed documents are stable.
type Schema = map[string]any

// Mode is the direction a type is read in: a request body is lenient (a field the client leaves out
// is the zero value), so it names no required fields; a response says what is always there.
type Mode int

const (
	Response Mode = iota
	Request
)

// Components collects the named schemas one document references by $ref.
type Components struct {
	prefix  string
	schemas map[string]Schema
	names   map[compKey]string
	taken   map[string]bool
	// Home is the package whose types are named without a package prefix (httpapi).
	Home string
}

type compKey struct {
	t    string // the type's full name: types.Named are not comparable across loads in a map key
	mode Mode
}

// NewComponents is an empty set whose references start with prefix, e.g. "#/components/schemas/".
func NewComponents(prefix, home string) *Components {
	return &Components{prefix: prefix, schemas: map[string]Schema{}, names: map[compKey]string{}, taken: map[string]bool{}, Home: home}
}

// Schemas is every named schema, by name.
func (c *Components) Schemas() map[string]Schema { return c.schemas }

// Add registers a hand-made schema under name and answers the reference to it.
func (c *Components) Add(name string, s Schema) Schema {
	c.schemas[name] = s
	c.taken[name] = true
	return c.Ref(name)
}

// Ref is the reference to a component by name.
func (c *Components) Ref(name string) Schema { return Schema{"$ref": c.prefix + name} }

// Of is the schema of a Go type as encoding/json writes (Response) or reads (Request) it.
func (c *Components) Of(t types.Type, m Mode) Schema {
	return c.of(t, m, 0)
}

func fullName(n *types.Named) string {
	o := n.Obj()
	if o.Pkg() == nil {
		return o.Name()
	}
	return o.Pkg().Path() + "." + o.Name()
}

func (c *Components) of(t types.Type, m Mode, depth int) Schema {
	if depth > 40 {
		return Schema{}
	}
	t = types.Unalias(t)
	if n, ok := t.(*types.Named); ok {
		switch fullName(n) {
		case "time.Time":
			return Schema{"type": "string", "format": "date-time"}
		case "time.Duration":
			return Schema{"type": "integer", "description": "nanoseconds"}
		case "encoding/json.RawMessage":
			return Schema{}
		case "encoding/json.Number":
			return Schema{"type": "number"}
		}
		if hasMethod(n, "MarshalJSON") {
			return Schema{} // it says nothing about its shape to a type checker
		}
		if hasMethod(n, "MarshalText") {
			return Schema{"type": "string"}
		}
		if _, ok := n.Underlying().(*types.Struct); ok {
			return c.ref(n, m, depth)
		}
		return c.of(n.Underlying(), m, depth+1)
	}
	switch u := t.(type) {
	case *types.Basic:
		return basic(u)
	case *types.Pointer:
		return nullable(c.of(u.Elem(), m, depth+1))
	case *types.Slice:
		if b, ok := types.Unalias(u.Elem()).(*types.Basic); ok && b.Kind() == types.Byte {
			return Schema{"type": "string", "contentEncoding": "base64"}
		}
		// encoding/json writes a nil slice as null
		return Schema{"type": []any{"array", "null"}, "items": c.of(u.Elem(), m, depth+1)}
	case *types.Array:
		return Schema{"type": "array", "items": c.of(u.Elem(), m, depth+1), "minItems": u.Len(), "maxItems": u.Len()}
	case *types.Map:
		return Schema{"type": []any{"object", "null"}, "additionalProperties": c.of(u.Elem(), m, depth+1)}
	case *types.Struct:
		return c.object(u, m, depth)
	case *types.Interface:
		return Schema{}
	case *types.TypeParam:
		return Schema{}
	}
	return Schema{"not": Schema{}} // chan, func: encoding/json refuses them
}

func basic(b *types.Basic) Schema {
	switch {
	case b.Info()&types.IsBoolean != 0:
		return Schema{"type": "boolean"}
	case b.Info()&types.IsInteger != 0 && b.Info()&types.IsUnsigned != 0:
		return Schema{"type": "integer", "minimum": 0}
	case b.Info()&types.IsInteger != 0:
		return Schema{"type": "integer"}
	case b.Info()&types.IsFloat != 0:
		return Schema{"type": "number"}
	case b.Info()&types.IsString != 0:
		return Schema{"type": "string"}
	case b.Kind() == types.UntypedNil:
		return Schema{"type": "null"}
	}
	return Schema{}
}

func hasMethod(n *types.Named, name string) bool {
	for _, t := range []types.Type{n, types.NewPointer(n)} {
		ms := types.NewMethodSet(t)
		for i := range ms.Len() {
			if ms.At(i).Obj().Name() == name {
				return true
			}
		}
	}
	return false
}

// nullable adds null: to the type list where the schema has one, else as an alternative.
func nullable(s Schema) Schema {
	if len(s) == 0 {
		return s
	}
	out := Schema{}
	for k, v := range s {
		out[k] = v
	}
	switch ty := s["type"].(type) {
	case string:
		out["type"] = []any{ty, "null"}
		return out
	case []any:
		for _, x := range ty {
			if x == "null" {
				return s
			}
		}
		out["type"] = append(append([]any{}, ty...), "null")
		return out
	}
	return Schema{"anyOf": []any{s, Schema{"type": "null"}}}
}

// ref registers a named struct once per mode and answers the reference to it.
func (c *Components) ref(n *types.Named, m Mode, depth int) Schema {
	k := compKey{types.TypeString(n, nil), m}
	name, ok := c.names[k]
	if !ok {
		name = c.nameFor(n, m)
		c.names[k] = name
		c.taken[name] = true
		c.schemas[name] = Schema{} // a placeholder, so a recursive field finds the name
		c.schemas[name] = c.object(n.Underlying().(*types.Struct), m, depth)
	}
	return c.Ref(name)
}

// nameFor is a component's name: the type's name capitalized, with its package's name in front
// when it is not the home package (system.WiFiView → SystemWiFiView), "Input" behind for the
// request reading, and a number when two still collide.
func (c *Components) nameFor(n *types.Named, m Mode) string {
	name := upperFirst(n.Obj().Name())
	if p := n.Obj().Pkg(); p != nil && p.Path() != c.Home {
		name = upperFirst(p.Name()) + name
	}
	if args := n.TypeArgs(); args != nil {
		for i := range args.Len() {
			name += upperFirst(types.TypeString(args.At(i), func(p *types.Package) string { return p.Name() }))
		}
	}
	if m == Request {
		name += "Input"
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			return r
		}
		return -1
	}, name)
	base, i := name, 2
	for c.taken[name] {
		name = base + strconv.Itoa(i)
		i++
	}
	return name
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// object is a struct's schema: its fields as encoding/json sees them.
func (c *Components) object(st *types.Struct, m Mode, depth int) Schema {
	props := Schema{}
	var required []string
	c.fields(st, m, depth, props, &required, map[*types.Struct]bool{})
	s := Schema{"type": "object", "properties": props}
	if len(required) > 0 && m == Response {
		sort.Strings(required)
		s["required"] = required
	}
	return s
}

func (c *Components) fields(st *types.Struct, m Mode, depth int, props Schema, required *[]string, seen map[*types.Struct]bool) {
	if seen[st] {
		return
	}
	seen[st] = true
	for i := range st.NumFields() {
		f := st.Field(i)
		tag := reflect.StructTag(st.Tag(i)).Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if f.Embedded() && name == "" {
			ft := types.Unalias(f.Type())
			if p, ok := ft.(*types.Pointer); ok {
				ft = types.Unalias(p.Elem())
			}
			if s, ok := ft.Underlying().(*types.Struct); ok {
				c.fields(s, m, depth+1, props, required, seen)
				continue
			}
		}
		if !f.Exported() {
			continue
		}
		if name == "" {
			name = f.Name()
		}
		if _, dup := props[name]; dup {
			continue // the shallower field wins, as in encoding/json
		}
		s := c.of(f.Type(), m, depth+1)
		if hasOpt(opts, "string") {
			s = Schema{"type": "string"}
		}
		props[name] = s
		if !hasOpt(opts, "omitempty") && !hasOpt(opts, "omitzero") {
			*required = append(*required, name)
		}
	}
}

func hasOpt(opts, want string) bool {
	for o := range strings.SplitSeq(opts, ",") {
		if o == want {
			return true
		}
	}
	return false
}

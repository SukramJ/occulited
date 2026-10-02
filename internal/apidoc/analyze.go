package apidoc

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"
	"os"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Analyzer reads handlers from source.
type Analyzer struct {
	pkgs      map[string]*packages.Package
	decls     map[*types.Func]declIn
	returning map[*types.Func]bool
}

type declIn struct {
	decl *ast.FuncDecl
	pkg  *packages.Package
}

// Load type-checks the packages (import paths or ./patterns) whose handlers the documents describe.
func Load(dir string, patterns ...string) (*Analyzer, error) {
	cfg := &packages.Config{Dir: dir, Mode: packages.NeedName | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		// the files the release builds: the same document on every developer's machine
		Env: append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, err
	}
	a := &Analyzer{pkgs: map[string]*packages.Package{}, decls: map[*types.Func]declIn{}, returning: map[*types.Func]bool{}}
	for _, p := range pkgs {
		if len(p.Errors) > 0 {
			return nil, fmt.Errorf("apidoc: %s: %v", p.PkgPath, p.Errors[0])
		}
		a.pkgs[p.PkgPath] = p
		for _, f := range p.Syntax {
			for _, d := range f.Decls {
				if fd, ok := d.(*ast.FuncDecl); ok && fd.Body != nil {
					if fn, ok := p.TypesInfo.Defs[fd.Name].(*types.Func); ok {
						a.decls[fn] = declIn{fd, p}
					}
				}
			}
		}
	}
	return a, nil
}

// Lookup finds a package-level type by "importpath.Name" among the loaded packages and their
// imports.
func (a *Analyzer) Lookup(name string) (types.Type, error) {
	i := strings.LastIndex(name, ".")
	if i < 0 {
		return nil, fmt.Errorf("apidoc: %q is not importpath.Name", name)
	}
	path, tn := name[:i], name[i+1:]
	for _, p := range a.pkgs {
		for _, tp := range append([]*types.Package{p.Types}, p.Types.Imports()...) {
			if tp.Path() == path {
				if o, ok := tp.Scope().Lookup(tn).(*types.TypeName); ok {
					return o.Type(), nil
				}
			}
		}
	}
	return nil, fmt.Errorf("apidoc: type %s not found", name)
}

// Handler is what one handler reads and answers.
type Handler struct {
	Requests  []Body         // what it decodes the body into
	Responses map[int][]Body // what it writes on a success status (2xx)
	Query     []string       // the query parameters it asks for
	Headers   map[string][]string
	Streams   bool  // it answers text/event-stream
	Empty     []int // the success statuses it sends without a body (w.WriteHeader(204))
	Redirects []int // the redirects it sends (http.Redirect)
}

// Body is one JSON body: a type, or a map literal's keys (Lit, with the function it is in).
type Body struct {
	Alts []Body // the returns of a function whose result is the body
	Type types.Type
	Lit  *ast.CompositeLit
	Adds []keyed // keys set on the literal's variable after it (x["k"] = v)
	info *types.Info
	fn   *ast.FuncDecl
}

type keyed struct {
	key string
	val ast.Expr
}

// FuncName is the runtime's name of a handler (runtime.FuncForPC): "path.(*T).m-fm" for a method
// value, "path.f" for a function.
func (a *Analyzer) Handler(runtimeName string) (*Handler, error) {
	fn := a.funcByRuntimeName(runtimeName)
	if fn == nil {
		return nil, fmt.Errorf("apidoc: handler %s not found in the loaded packages", runtimeName)
	}
	h := &Handler{Responses: map[int][]Body{}, Headers: map[string][]string{}}
	a.walk(h, fn, map[*types.Func]bool{}, 0)
	sort.Strings(h.Query)
	return h, nil
}

func (a *Analyzer) funcByRuntimeName(name string) *types.Func {
	name = strings.TrimSuffix(name, "-fm")
	// a closure (a handler factory's answer) is read with the function it is made in
	name = closureRE.ReplaceAllString(name, "")
	for fn := range a.decls {
		if runtimeName(fn) == name {
			return fn
		}
	}
	return nil
}

var closureRE = regexp.MustCompile(`(\.func\d+)+$`)

func runtimeName(fn *types.Func) string {
	sig := fn.Type().(*types.Signature)
	if r := sig.Recv(); r != nil {
		t := r.Type()
		ptr := false
		if p, ok := t.(*types.Pointer); ok {
			t, ptr = p.Elem(), true
		}
		n, _ := types.Unalias(t).(*types.Named)
		if n == nil {
			return ""
		}
		if ptr {
			return fn.Pkg().Path() + ".(*" + n.Obj().Name() + ")." + fn.Name()
		}
		return fn.Pkg().Path() + "." + n.Obj().Name() + "." + fn.Name()
	}
	return fn.Pkg().Path() + "." + fn.Name()
}

func (a *Analyzer) walk(h *Handler, fn *types.Func, seen map[*types.Func]bool, depth int) {
	d, ok := a.decls[fn]
	if !ok || seen[fn] || depth > 4 {
		return
	}
	seen[fn] = true
	info := d.pkg.TypesInfo
	ast.Inspect(d.decl.Body, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		callee := calleeOf(info, c)
		switch {
		case callee != nil && callee.Name() == "writeJSON" && len(c.Args) == 3:
			status := 200
			if tv := info.Types[c.Args[1]]; tv.Value != nil {
				v, _ := constant.Int64Val(tv.Value)
				status = int(v)
			} else if isAPIError(info.TypeOf(c.Args[2])) {
				return true // writeJSON(w, code, apiError{...}) with a computed code
			}
			if status >= 200 && status < 300 {
				h.Responses[status] = append(h.Responses[status], a.body(info, d.decl, c.Args[2]))
			}
		case callee != nil && callee.Name() == "readJSON" && len(c.Args) >= 2:
			if t := deref(info.TypeOf(c.Args[len(c.Args)-1])); !isInterface(t) {
				h.Requests = append(h.Requests, Body{Type: t})
			}
		case callee != nil && callee.Name() == "Decode" && decodesRequestBody(info, c):
			if t := deref(info.TypeOf(c.Args[0])); !isInterface(t) {
				h.Requests = append(h.Requests, Body{Type: t})
			}
		case callee != nil && (callee.Name() == "Get" || callee.Name() == "Has") && isURLValues(info, c):
			if s, ok := constString(info, c.Args[0]); ok {
				h.Query = appendUnique(h.Query, s)
			}
		case callee != nil && (callee.Name() == "FormValue" || callee.Name() == "PathValue") && len(c.Args) == 1:
			if s, ok := constString(info, c.Args[0]); ok && callee.Name() == "FormValue" {
				h.Query = appendUnique(h.Query, s)
			}
		case callee != nil && callee.Name() == "WriteHeader" && len(c.Args) == 1 && isNamed(recvOf(info, c), "net/http", "ResponseWriter"):
			if tv := info.Types[c.Args[0]]; tv.Value != nil {
				if v, _ := constant.Int64Val(tv.Value); v >= 200 && v < 300 {
					h.Empty = appendUniqueInt(h.Empty, int(v))
				}
			}
		case callee != nil && callee.Name() == "Redirect" && callee.Pkg() != nil && callee.Pkg().Path() == "net/http" && len(c.Args) == 4:
			if tv := info.Types[c.Args[3]]; tv.Value != nil {
				v, _ := constant.Int64Val(tv.Value)
				h.Redirects = appendUniqueInt(h.Redirects, int(v))
			}
		case callee != nil && callee.Name() == "Set" && isHeader(info, c) && len(c.Args) == 2:
			k, ok1 := constString(info, c.Args[0])
			v, ok2 := constString(info, c.Args[1])
			if ok1 && ok2 {
				h.Headers[k] = appendUnique(h.Headers[k], v)
				if k == "Content-Type" && strings.HasPrefix(v, "text/event-stream") {
					h.Streams = true
				}
			}
		}
		// follow the same package's functions that get the handler's writer or request, or the
		// request's query (B-33: /log's filters and cursors are read in logQuery and logPage)
		if callee != nil && callee.Pkg() != nil && callee.Pkg() == d.pkg.Types && passesHTTP(info, c) {
			a.walk(h, callee, seen, depth+1)
		}
		return true
	})
}

func appendUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

func appendUniqueInt(list []int, v int) []int {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func calleeOf(info *types.Info, c *ast.CallExpr) *types.Func {
	var id *ast.Ident
	switch f := ast.Unparen(c.Fun).(type) {
	case *ast.Ident:
		id = f
	case *ast.SelectorExpr:
		id = f.Sel
	case *ast.IndexExpr: // a generic function's instantiation
		if s, ok := f.X.(*ast.Ident); ok {
			id = s
		}
	}
	if id == nil {
		return nil
	}
	fn, _ := info.Uses[id].(*types.Func)
	if fn != nil {
		fn = fn.Origin()
	}
	return fn
}

func deref(t types.Type) types.Type {
	if p, ok := types.Unalias(t).(*types.Pointer); ok {
		return p.Elem()
	}
	return t
}

func isInterface(t types.Type) bool {
	_, ok := t.Underlying().(*types.Interface)
	return ok
}

func isAPIError(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Name() == "apiError"
}

func isNamed(t types.Type, path, name string) bool {
	t = types.Unalias(t)
	if p, ok := t.(*types.Pointer); ok {
		t = types.Unalias(p.Elem())
	}
	n, ok := t.(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == path && n.Obj().Name() == name
}

func recvOf(info *types.Info, c *ast.CallExpr) types.Type {
	if s, ok := ast.Unparen(c.Fun).(*ast.SelectorExpr); ok {
		if t := info.TypeOf(s.X); t != nil {
			return t
		}
	}
	return types.Typ[types.Invalid]
}

func isURLValues(info *types.Info, c *ast.CallExpr) bool {
	t := recvOf(info, c)
	return isNamed(t, "net/url", "Values") && len(c.Args) == 1
}

func isHeader(info *types.Info, c *ast.CallExpr) bool {
	return isNamed(recvOf(info, c), "net/http", "Header")
}

// decodesRequestBody is json.NewDecoder(r.Body).Decode(&v) with r an *http.Request.
func decodesRequestBody(info *types.Info, c *ast.CallExpr) bool {
	s, ok := ast.Unparen(c.Fun).(*ast.SelectorExpr)
	if !ok || len(c.Args) != 1 {
		return false
	}
	nd, ok := ast.Unparen(s.X).(*ast.CallExpr)
	if !ok || len(nd.Args) != 1 {
		return false
	}
	if fn := calleeOf(info, nd); fn == nil || fn.Name() != "NewDecoder" {
		return false
	}
	body, ok := ast.Unparen(nd.Args[0]).(*ast.SelectorExpr)
	return ok && body.Sel.Name == "Body" && isNamed(info.TypeOf(body.X), "net/http", "Request")
}

func passesHTTP(info *types.Info, c *ast.CallExpr) bool {
	for _, arg := range c.Args {
		t := info.TypeOf(arg)
		if t != nil && (isNamed(t, "net/http", "ResponseWriter") || isNamed(t, "net/http", "Request") || isNamed(t, "net/url", "Values")) {
			return true
		}
	}
	return false
}

func constString(info *types.Info, e ast.Expr) (string, bool) {
	tv := info.Types[e]
	if tv.Value == nil || tv.Value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(tv.Value), true
}

// body is the answer writeJSON writes: a map literal (in place, or the variable it was assigned
// to, with the keys set on it later) or a type.
func (a *Analyzer) body(info *types.Info, fn *ast.FuncDecl, e ast.Expr) Body {
	e = ast.Unparen(e)
	if lit, ok := e.(*ast.CompositeLit); ok && isStringMap(info.TypeOf(lit)) {
		return Body{Lit: lit, info: info, fn: fn}
	}
	if id, ok := e.(*ast.Ident); ok && isStringMap(info.TypeOf(id)) {
		if v, ok := info.Uses[id].(*types.Var); ok {
			if lit, adds := mapVar(info, fn, v); lit != nil {
				return Body{Lit: lit, Adds: adds, info: info, fn: fn}
			}
		}
	}
	// a same-package function's result: what its returns hold (a view built as a map literal)
	if c, ok := e.(*ast.CallExpr); ok {
		if alts := a.returned(info, c, 0); len(alts) > 0 {
			return Body{Alts: alts}
		}
	}
	return Body{Type: info.TypeOf(e)}
}

// returned is what the function c calls returns in result idx, when it is one of the loaded
// packages' and its result is a string-keyed map (a view built by hand); nil otherwise.
func (a *Analyzer) returned(info *types.Info, c *ast.CallExpr, idx int) []Body {
	callee := calleeOf(info, c)
	if callee == nil {
		return nil
	}
	d, ok := a.decls[callee]
	if !ok || a.returning[callee] {
		return nil // not ours, or a recursion
	}
	a.returning[callee] = true
	defer delete(a.returning, callee)
	res := callee.Type().(*types.Signature).Results()
	if res.Len() <= idx || !isStringMap(res.At(idx).Type()) {
		return nil
	}
	var out []Body
	for _, ret := range returnsOf(d.decl) {
		if len(ret.Results) == res.Len() {
			out = append(out, a.body(d.pkg.TypesInfo, d.decl, ret.Results[idx]))
		}
	}
	return out
}

// returnsOf is a function's own return statements, not its closures'.
func returnsOf(fn *ast.FuncDecl) []*ast.ReturnStmt {
	var out []*ast.ReturnStmt
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			out = append(out, x)
		}
		return true
	})
	return out
}

// Message is one message a stream sends: its type's name (when a constant says it) and its body.
type Message struct {
	Name string
	Body Body
}

// Calls is what function fn (a runtime name, as for Handler) hands to calls of a function or
// closure named call: the constant string at argument typeArg (none when typeArg < 0) and the
// body at argument dataArg. It reads fn's closures too.
func (a *Analyzer) Calls(fn, call string, typeArg, dataArg int) ([]Message, error) {
	f := a.funcByRuntimeName(fn)
	if f == nil {
		return nil, fmt.Errorf("apidoc: function %s not found in the loaded packages", fn)
	}
	d := a.decls[f]
	info := d.pkg.TypesInfo
	var out []Message
	ast.Inspect(d.decl.Body, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok || len(c.Args) <= max(typeArg, dataArg) {
			return true
		}
		var name string
		switch f := ast.Unparen(c.Fun).(type) {
		case *ast.Ident:
			name = f.Name
		case *ast.SelectorExpr:
			name = f.Sel.Name
		}
		if name != call {
			return true
		}
		m := Message{Body: a.body(info, d.decl, c.Args[dataArg])}
		if typeArg >= 0 {
			m.Name, _ = constString(info, c.Args[typeArg])
		}
		out = append(out, m)
		return true
	})
	return out, nil
}

// Returns is what function fn returns: the constant string in result typeIdx (or "") and the
// body in result dataIdx, per return statement.
func (a *Analyzer) Returns(fn string, typeIdx, dataIdx int) ([]Message, error) {
	f := a.funcByRuntimeName(fn)
	if f == nil {
		return nil, fmt.Errorf("apidoc: function %s not found in the loaded packages", fn)
	}
	d := a.decls[f]
	info := d.pkg.TypesInfo
	var out []Message
	for _, ret := range returnsOf(d.decl) {
		if len(ret.Results) <= max(typeIdx, dataIdx) {
			continue
		}
		name, _ := constString(info, ret.Results[typeIdx])
		out = append(out, Message{Name: name, Body: a.body(info, d.decl, ret.Results[dataIdx])})
	}
	return out, nil
}

func isStringMap(t types.Type) bool {
	if t == nil {
		return false
	}
	m, ok := t.Underlying().(*types.Map)
	if !ok {
		return false
	}
	k, ok := m.Key().Underlying().(*types.Basic)
	return ok && k.Info()&types.IsString != 0
}

// mapVar finds v's defining map literal in fn and the constant keys assigned to it afterwards.
// A variable assigned more than one literal, or none, is not followed.
func mapVar(info *types.Info, fn *ast.FuncDecl, v *types.Var) (*ast.CompositeLit, []keyed) {
	var lit *ast.CompositeLit
	var adds []keyed
	n := 0
	ast.Inspect(fn.Body, func(nd ast.Node) bool {
		as, ok := nd.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, l := range as.Lhs {
			if i >= len(as.Rhs) && len(as.Rhs) != 1 {
				break
			}
			var rhs ast.Expr
			if len(as.Lhs) == len(as.Rhs) {
				rhs = as.Rhs[i]
			}
			switch x := l.(type) {
			case *ast.Ident:
				if obj := info.ObjectOf(x); obj == v && rhs != nil {
					n++
					if cl, ok := ast.Unparen(rhs).(*ast.CompositeLit); ok {
						lit = cl
					}
				}
			case *ast.IndexExpr:
				if id, ok := x.X.(*ast.Ident); ok && info.ObjectOf(id) == v && rhs != nil {
					if k, ok := constString(info, x.Index); ok {
						adds = append(adds, keyed{k, rhs})
					}
				}
			}
		}
		return true
	})
	// var out = map[string]any{...}
	ast.Inspect(fn.Body, func(nd ast.Node) bool {
		vs, ok := nd.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range vs.Names {
			if info.ObjectOf(name) == v && i < len(vs.Values) {
				n++
				if cl, ok := ast.Unparen(vs.Values[i]).(*ast.CompositeLit); ok {
					lit = cl
				}
			}
		}
		return true
	})
	if n != 1 {
		return nil, nil
	}
	return lit, adds
}

// Schema is a body's schema.
func (c *Components) Body(b Body, m Mode) Schema {
	if b.Alts != nil {
		if s := c.Distinct(b.Alts, m); s != nil {
			return s
		}
		return Schema{}
	}
	if b.Lit == nil {
		if b.Type == nil {
			return Schema{}
		}
		return c.Of(b.Type, m)
	}
	return c.literal(b.info, b.fn, b.Lit, b.Adds, m, 0)
}

func (c *Components) literal(info *types.Info, fn *ast.FuncDecl, lit *ast.CompositeLit, adds []keyed, m Mode, depth int) Schema {
	props := Schema{}
	var required []string
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		k, ok := constString(info, kv.Key)
		if !ok {
			return c.Of(info.TypeOf(lit), m) // a computed key: only the map's type is known
		}
		props[k] = c.value(info, fn, kv.Value, m, depth)
		required = append(required, k)
	}
	for _, a := range adds {
		if _, ok := props[a.key]; !ok {
			props[a.key] = c.value(info, fn, a.val, m, depth)
		} else {
			props[a.key] = merge(props[a.key].(Schema), c.value(info, fn, a.val, m, depth))
		}
	}
	s := Schema{"type": "object", "properties": props}
	if len(required) > 0 && m == Response {
		sort.Strings(required)
		s["required"] = required
	}
	return s
}

// value is one map entry's schema: a nested map literal is followed, anything else by its type.
func (c *Components) value(info *types.Info, fn *ast.FuncDecl, e ast.Expr, m Mode, depth int) Schema {
	e = ast.Unparen(e)
	if depth < 6 {
		if lit, ok := e.(*ast.CompositeLit); ok && isStringMap(info.TypeOf(lit)) {
			return c.literal(info, fn, lit, nil, m, depth+1)
		}
		if id, ok := e.(*ast.Ident); ok && isStringMap(info.TypeOf(id)) && fn != nil {
			if v, ok := info.Uses[id].(*types.Var); ok {
				if lit, adds := mapVar(info, fn, v); lit != nil {
					return c.literal(info, fn, lit, adds, m, depth+1)
				}
			}
		}
	}
	tv := info.Types[e]
	if tv.Type == nil {
		return Schema{}
	}
	if b, ok := tv.Type.(*types.Basic); ok && b.Info()&types.IsUntyped != 0 {
		return basic(b)
	}
	return c.Of(tv.Type, m)
}

// merge is the schema of a key set to two different things: either.
func merge(a, b Schema) Schema {
	if equal(a, b) {
		return a
	}
	return Schema{"anyOf": []any{a, b}}
}

func equal(a, b Schema) bool { return fmt.Sprint(a) == fmt.Sprint(b) }

// Distinct is the bodies' schemas without repeats; one is itself, several are oneOf... anyOf,
// since two answers of the same handler can overlap.
func (c *Components) Distinct(bodies []Body, m Mode) Schema {
	var out []Schema
	seen := map[string]bool{}
	for _, b := range bodies {
		s := c.Body(b, m)
		k := fmt.Sprint(s)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, s)
	}
	if len(out) > 1 { // a helper's writeJSON(w, 200, v any) adds nothing to a typed answer
		kept := out[:0]
		for _, s := range out {
			if len(s) > 0 {
				kept = append(kept, s)
			}
		}
		out = kept
	}
	switch len(out) {
	case 0:
		return nil
	case 1:
		return out[0]
	}
	sort.Slice(out, func(i, j int) bool { return fmt.Sprint(out[i]) < fmt.Sprint(out[j]) })
	anyOf := make([]any, len(out))
	for i, s := range out {
		anyOf[i] = s
	}
	return Schema{"anyOf": anyOf}
}

// Status codes sorted.
func (h *Handler) Statuses() []int {
	var out []int
	for s := range h.Responses {
		out = append(out, s)
	}
	sort.Ints(out)
	return out
}

// Keys is a map literal body's constant keys (with those set later); nil for a typed body.
func (b Body) Keys() []string {
	if b.Lit == nil {
		return nil
	}
	var out []string
	for _, el := range b.Lit.Elts {
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			if k, ok := constString(b.info, kv.Key); ok {
				out = append(out, k)
			}
		}
	}
	for _, a := range b.Adds {
		out = append(out, a.key)
	}
	sort.Strings(out)
	return out
}

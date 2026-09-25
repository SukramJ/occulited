// Package meta is the metadata store of openccu-lite: friendly names and taxonomy for Homematic
// devices and channels. The file format is docs/meta-format.md, the operations are
// docs/meta-api.md, and fixtures/ is the corpus both this and the TypeScript implementation must
// pass (D-16). Nothing here interprets device state; the store never invents objects.
package meta

import "encoding/json"

// Format is the on-disk format version this implementation reads and writes.
const Format = 1

// MaxDepth is the maximum number of node levels below an enum.
const MaxDepth = 8

// Document is the whole store as on disk.
type Document struct {
	Format   int                `json:"format"`
	Revision uint64             `json:"revision"`
	Objects  map[string]*Object `json:"objects"`
	Enums    map[string]*Enum   `json:"enums"`
}

// Object is a device or a channel, keyed by its ref (<interface>.<address>).
type Object struct {
	Name     string                     `json:"name"`
	Enums    []string                   `json:"enums"`
	Meta     map[string]json.RawMessage `json:"meta"`
	Orphaned bool                       `json:"orphaned,omitempty"`
}

// Enum is a named taxonomy holding an ordered tree of nodes.
type Enum struct {
	Name map[string]string `json:"name"`
	Tree []*Node           `json:"tree"`
}

// Node is one node of an enum's tree. Ids are stable; names are not.
type Node struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Icon     string  `json:"icon,omitempty"`
	Children []*Node `json:"children,omitempty"`
}

// ObjectPatch is a partial update of an object (PATCH semantics). A nil field is "leave alone".
// A Meta entry with a nil RawMessage (JSON null) removes that namespace.
type ObjectPatch struct {
	Name  *string
	Enums *[]string
	Meta  map[string]json.RawMessage
}

// NodePatch is a partial update of a node. Parent moves the node: nil = leave alone, a pointer to
// "" = move to the enum root, otherwise the new parent's path. Position reorders among siblings.
type NodePatch struct {
	Name     *string
	Icon     *string
	Parent   *string
	Position *int
}

// ImportMode selects how an imported document is applied.
type ImportMode int

const (
	// ImportReplace replaces the whole store with the imported document.
	ImportReplace ImportMode = iota
	// ImportMerge keeps existing objects and enums the import does not mention and overwrites
	// the ones it does.
	ImportMerge
)

// Defaults returns the two default enums of a fresh store, with empty trees: rooms and functions,
// what a CCU has (task 46). A floor is a room with rooms below it; a store that still carries a
// "floor" enum from before keeps it, the defaults only decide what a new store starts with.
func Defaults() map[string]*Enum {
	return map[string]*Enum{
		"room":     {Name: map[string]string{"de": "Räume", "en": "Rooms"}, Tree: []*Node{}},
		"function": {Name: map[string]string{"de": "Gewerke", "en": "Functions"}, Tree: []*Node{}},
	}
}

// NewDocument returns an empty store at revision 0 with the default enums.
func NewDocument() *Document {
	return &Document{Format: Format, Revision: 0, Objects: map[string]*Object{}, Enums: Defaults()}
}

// Clone returns a deep copy. Mutations work on a copy, validate it, and swap it in, so a failed
// operation leaves the store untouched.
func (d *Document) Clone() *Document {
	c := &Document{Format: d.Format, Revision: d.Revision, Objects: make(map[string]*Object, len(d.Objects)), Enums: make(map[string]*Enum, len(d.Enums))}
	for k, o := range d.Objects {
		c.Objects[k] = o.clone()
	}
	for k, e := range d.Enums {
		c.Enums[k] = e.clone()
	}
	return c
}

func (o *Object) clone() *Object {
	c := &Object{Name: o.Name, Orphaned: o.Orphaned, Enums: append([]string{}, o.Enums...), Meta: make(map[string]json.RawMessage, len(o.Meta))}
	for k, v := range o.Meta {
		c.Meta[k] = append(json.RawMessage{}, v...)
	}
	return c
}

func (e *Enum) clone() *Enum {
	c := &Enum{Name: make(map[string]string, len(e.Name)), Tree: cloneNodes(e.Tree)}
	for k, v := range e.Name {
		c.Name[k] = v
	}
	return c
}

func cloneNodes(ns []*Node) []*Node {
	out := make([]*Node, len(ns))
	for i, n := range ns {
		out[i] = &Node{ID: n.ID, Name: n.Name, Icon: n.Icon, Children: cloneNodes(n.Children)}
	}
	return out
}

// normalize fills defaults so that every object has non-nil Enums and Meta and every enum a
// non-nil Tree; the canonical output always carries those keys.
func (d *Document) normalize() {
	if d.Objects == nil {
		d.Objects = map[string]*Object{}
	}
	if d.Enums == nil {
		d.Enums = map[string]*Enum{}
	}
	for _, o := range d.Objects {
		if o.Enums == nil {
			o.Enums = []string{}
		}
		if o.Meta == nil {
			o.Meta = map[string]json.RawMessage{}
		}
	}
	for _, e := range d.Enums {
		if e.Tree == nil {
			e.Tree = []*Node{}
		}
		if e.Name == nil {
			e.Name = map[string]string{}
		}
	}
}

// Clone returns a deep copy of the enum (for callers outside the package that build documents
// from the store's current trees).
func (e *Enum) Clone() *Enum { return e.clone() }

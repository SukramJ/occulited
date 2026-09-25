package meta

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	idRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	iconRe = regexp.MustCompile(`^[a-z0-9-]+$`)
)

const (
	maxIDLen   = 32
	maxNameLen = 255
	maxMetaLen = 16 * 1024
)

// ValidateRef checks <interface>.<address>: a non-empty interface without '.', a non-empty address.
func ValidateRef(ref string) error {
	i := strings.IndexByte(ref, '.')
	if i <= 0 || i == len(ref)-1 {
		return errf(ErrInvalidRef, "ref %q is not <interface>.<address>", ref)
	}
	if strings.ContainsAny(ref, "\n\r\t /") {
		return errf(ErrInvalidRef, "ref %q contains whitespace or '/'", ref)
	}
	return nil
}

// NormalizeName trims and validates a display name; the trimmed value is what gets stored.
func NormalizeName(name string) (string, error) {
	n := strings.TrimSpace(name)
	if n == "" {
		return "", errf(ErrInvalidName, "name must not be empty")
	}
	// A name is a human label, and it travels: into meta.json, through the change stream into
	// every consumer, and into the HM-Script export that carries it back to a CCU. A control
	// character has no place in one and truncates or corrupts something downstream - a NUL most
	// of all, which utf8.ValidString happily accepts.
	for _, r := range n {
		if r < 0x20 || r == 0x7f {
			return "", errf(ErrInvalidName, "name must not contain control characters")
		}
	}
	if len(n) > maxNameLen || !utf8.ValidString(n) {
		return "", errf(ErrInvalidName, "name is too long or not valid UTF-8")
	}
	return n, nil
}

// ValidateID checks an enum id, a node id or a meta namespace.
func ValidateID(id string) error {
	if len(id) > maxIDLen || !idRe.MatchString(id) {
		return errf(ErrInvalidID, "id %q must match [a-z0-9][a-z0-9-]* and be at most %d characters", id, maxIDLen)
	}
	return nil
}

func validateIcon(icon string) error {
	if icon != "" && (len(icon) > maxIDLen || !iconRe.MatchString(icon)) {
		return errf(ErrInvalidID, "icon %q must match [a-z0-9-]+", icon)
	}
	return nil
}

func validateEnumName(name map[string]string) error {
	if name == nil || strings.TrimSpace(name["en"]) == "" {
		return errf(ErrInvalidName, "an enum needs at least an English name")
	}
	for lang, v := range name {
		if _, err := NormalizeName(v); err != nil {
			return errf(ErrInvalidName, "enum name for %q: %v", lang, err)
		}
	}
	return nil
}

// splitPath returns the enum id and the node ids of a path such as "room/eg/bad".
func splitPath(path string) (enum string, ids []string) {
	parts := strings.Split(path, "/")
	return parts[0], parts[1:]
}

// findNode resolves a path to the enum, the node (nil when the path names the enum itself), its
// parent list (the slice holding it) and its index. An unknown enum or node yields ErrUnknownPath;
// an unknown enum with no node ids yields ErrUnknownEnum.
func (d *Document) findNode(path string) (e *Enum, n *Node, siblings *[]*Node, idx int, err error) {
	enumID, ids := splitPath(path)
	e, ok := d.Enums[enumID]
	if !ok {
		if len(ids) == 0 {
			return nil, nil, nil, 0, errf(ErrUnknownEnum, "enum %q does not exist", enumID)
		}
		return nil, nil, nil, 0, errf(ErrUnknownPath, "path %q does not resolve", path)
	}
	list := &e.Tree
	for _, id := range ids {
		found := -1
		for i, c := range *list {
			if c.ID == id {
				found = i
				break
			}
		}
		if found < 0 {
			return e, nil, nil, 0, errf(ErrUnknownPath, "path %q does not resolve", path)
		}
		n = (*list)[found]
		siblings, idx = list, found
		list = &n.Children
	}
	return e, n, siblings, idx, nil
}

// validateTree checks ids, names, icons, sibling uniqueness and depth of one enum's tree.
func validateTree(enumID string, nodes []*Node, depth int) error {
	if depth > MaxDepth {
		return errf(ErrTooDeep, "enum %q exceeds %d levels", enumID, MaxDepth)
	}
	seen := map[string]bool{}
	for _, n := range nodes {
		if err := ValidateID(n.ID); err != nil {
			return err
		}
		if seen[n.ID] {
			return errf(ErrDuplicateID, "enum %q has two siblings with id %q", enumID, n.ID)
		}
		seen[n.ID] = true
		nm, err := NormalizeName(n.Name)
		if err != nil {
			return err
		}
		n.Name = nm
		if err := validateIcon(n.Icon); err != nil {
			return err
		}
		if len(n.Children) > 0 {
			if err := validateTree(enumID, n.Children, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// Validate checks every invariant of docs/meta-format.md and normalizes names in place.
func (d *Document) Validate() error {
	if d.Format > Format {
		return errf(ErrFormatUnsupported, "format %d is newer than %d", d.Format, Format)
	}
	d.normalize()
	for id, e := range d.Enums {
		if err := ValidateID(id); err != nil {
			return err
		}
		if err := validateEnumName(e.Name); err != nil {
			return err
		}
		if err := validateTree(id, e.Tree, 1); err != nil {
			return err
		}
	}
	for ref, o := range d.Objects {
		if err := ValidateRef(ref); err != nil {
			return err
		}
		nm, err := NormalizeName(o.Name)
		if err != nil {
			return errf(ErrInvalidName, "object %q: %v", ref, err)
		}
		o.Name = nm
		seen := map[string]bool{}
		for _, p := range o.Enums {
			if seen[p] {
				return errf(ErrDuplicatePath, "object %q lists %q twice", ref, p)
			}
			seen[p] = true
			_, n, _, _, err := d.findNode(p)
			if err != nil {
				return errf(ErrUnknownPath, "object %q: path %q does not resolve", ref, p)
			}
			if n == nil {
				return errf(ErrUnknownPath, "object %q: %q names an enum, not a node", ref, p)
			}
		}
		total := 0
		for ns, v := range o.Meta {
			if err := ValidateID(ns); err != nil {
				return errf(ErrInvalidID, "object %q: meta namespace %q is invalid", ref, ns)
			}
			total += len(v)
		}
		if total > maxMetaLen {
			return errf(ErrInvalidBody, "object %q: meta exceeds %d bytes", ref, maxMetaLen)
		}
	}
	return nil
}

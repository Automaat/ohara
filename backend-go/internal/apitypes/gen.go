// Package apitypes writes the TypeScript types of the JSON the SPA reads and
// sends, from the Go structs the handlers encode and decode.
//
// A struct becomes an object type named by its entry in the list given to
// Generate, with its fields in Go order and embedded structs flattened as
// encoding/json flattens them. A pointer field may be null unless it is
// omitempty, which drops a nil pointer: such a field may be absent instead.
// Types listed as scalars (time.Time, collection.Date) are strings. A string
// field tagged `ts:"Name"` takes the values of the enum Name instead.
package apitypes

import (
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
)

// Named gives a Go struct the TypeScript name it is written under.
type Named struct {
	Name string
	Type reflect.Type
}

// Enum is a string union: the values a field tagged with its name takes.
type Enum struct {
	Name   string
	Values []string
}

// Scalar is a Go type that encodes itself as a JSON string.
type Scalar struct {
	Type reflect.Type
}

// API is everything Generate writes.
type API struct {
	Enums   []Enum
	Scalars []Scalar
	Types   []Named
}

// maxLine is prettier's printWidth in .prettierrc.
const maxLine = 100

var (
	marshalerType     = reflect.TypeFor[json.Marshaler]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
)

// Of names the Go type T.
func Of[T any](name string) Named {
	return Named{Name: name, Type: reflect.TypeFor[T]()}
}

// ScalarOf marks T as a JSON string.
func ScalarOf[T any]() Scalar {
	return Scalar{Type: reflect.TypeFor[T]()}
}

type generator struct {
	enums   map[string]bool
	scalars map[reflect.Type]bool
	names   map[reflect.Type]string
}

// Generate writes header, then api's enums and types, as one TypeScript
// module. It fails on a field whose type it cannot name: a struct missing
// from api.Types, a type that marshals itself but is not a scalar, an
// interface, a map, or an enum tag naming no enum.
func Generate(w io.Writer, header string, api API) error {
	g := generator{
		enums:   map[string]bool{},
		scalars: map[reflect.Type]bool{},
		names:   map[reflect.Type]string{},
	}
	used := map[string]bool{}
	for _, e := range api.Enums {
		if used[e.Name] {
			return fmt.Errorf("type name %s used twice", e.Name)
		}
		if len(e.Values) == 0 {
			return fmt.Errorf("enum %s has no values", e.Name)
		}
		used[e.Name] = true
		g.enums[e.Name] = true
	}
	for _, s := range api.Scalars {
		g.scalars[s.Type] = true
	}
	for _, n := range api.Types {
		if used[n.Name] {
			return fmt.Errorf("type name %s used twice", n.Name)
		}
		if n.Type.Kind() != reflect.Struct {
			return fmt.Errorf("%s: %s is not a struct", n.Name, n.Type)
		}
		if _, ok := g.names[n.Type]; ok {
			return fmt.Errorf("%s: %s is named twice", n.Name, n.Type)
		}
		used[n.Name] = true
		g.names[n.Type] = n.Name
	}

	var b strings.Builder
	b.WriteString(header)
	for _, e := range api.Enums {
		b.WriteString("\n")
		writeUnion(&b, e.Name, e.Values)
	}
	for _, n := range api.Types {
		b.WriteString("\n")
		if err := g.writeObject(&b, n); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func writeUnion(b *strings.Builder, name string, values []string) {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = quote(v)
	}
	line := "export type " + name + " = " + strings.Join(quoted, " | ") + ";"
	if len(line) <= maxLine {
		b.WriteString(line + "\n")
		return
	}
	b.WriteString("export type " + name + " =\n")
	for i, q := range quoted {
		b.WriteString("\t| " + q)
		if i == len(quoted)-1 {
			b.WriteString(";")
		}
		b.WriteString("\n")
	}
}

// quote writes s as a single-quoted TypeScript string, prettier's style.
func quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`, "\n", `\n`)
	return "'" + r.Replace(s) + "'"
}

type field struct {
	name     string
	tsType   string
	optional bool
	depth    int
}

func (g *generator) writeObject(b *strings.Builder, n Named) error {
	fields, err := g.fields(n.Type, 0)
	if err != nil {
		return fmt.Errorf("%s: %w", n.Name, err)
	}
	fields, err = dominant(fields)
	if err != nil {
		return fmt.Errorf("%s: %w", n.Name, err)
	}
	if len(fields) == 0 {
		b.WriteString("export type " + n.Name + " = Record<string, never>;\n")
		return nil
	}
	b.WriteString("export type " + n.Name + " = {\n")
	for _, f := range fields {
		opt := ""
		if f.optional {
			opt = "?"
		}
		b.WriteString("\t" + propertyName(f.name) + opt + ": " + f.tsType + ";\n")
	}
	b.WriteString("};\n")
	return nil
}

// fields lists t's JSON fields as encoding/json sees them, embedded structs
// expanded in place.
func (g *generator) fields(t reflect.Type, depth int) ([]field, error) {
	var out []field
	for sf := range t.Fields() {
		tag := sf.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if sf.Anonymous && name == "" {
			et := sf.Type
			if et.Kind() == reflect.Pointer {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct {
				inner, err := g.fields(et, depth+1)
				if err != nil {
					return nil, err
				}
				out = append(out, inner...)
				continue
			}
		}
		if !sf.IsExported() {
			continue
		}
		if name == "" {
			name = sf.Name
		}
		if hasOption(opts, "string") {
			return nil, fmt.Errorf("field %s: the json string option is not supported", sf.Name)
		}
		omitEmpty := hasOption(opts, "omitempty")
		ts, err := g.fieldType(sf, omitEmpty)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", sf.Name, err)
		}
		out = append(out, field{name: name, tsType: ts, optional: omitEmpty, depth: depth})
	}
	return out, nil
}

func hasOption(opts, option string) bool {
	for o := range strings.SplitSeq(opts, ",") {
		if o == option {
			return true
		}
	}
	return false
}

// dominant keeps, for each JSON name, the field encoding/json writes: the
// shallowest one. Two at the same depth would drop both, which an API type
// never means to do.
func dominant(fields []field) ([]field, error) {
	best := map[string]int{}
	for _, f := range fields {
		if d, ok := best[f.name]; !ok || f.depth < d {
			best[f.name] = f.depth
		}
	}
	seen := map[string]bool{}
	var out []field
	for _, f := range fields {
		if f.depth != best[f.name] {
			continue
		}
		if seen[f.name] {
			return nil, fmt.Errorf("field %s appears twice at the same depth", f.name)
		}
		seen[f.name] = true
		out = append(out, f)
	}
	return out, nil
}

func (g *generator) fieldType(sf reflect.StructField, omitEmpty bool) (string, error) {
	t := sf.Type
	nullable := false
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
		nullable = !omitEmpty
	}
	var ts string
	var err error
	if enum, ok := sf.Tag.Lookup("ts"); ok {
		ts, err = g.enumType(t, enum)
	} else {
		ts, err = g.tsType(t)
	}
	if err != nil {
		return "", err
	}
	if nullable {
		ts += " | null"
	}
	return ts, nil
}

func (g *generator) enumType(t reflect.Type, enum string) (string, error) {
	if !g.enums[enum] {
		return "", fmt.Errorf("unknown enum %q", enum)
	}
	switch {
	case t.Kind() == reflect.String:
		return enum, nil
	case t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.String:
		return enum + "[]", nil
	default:
		return "", fmt.Errorf("enum %s on %s: only strings and string slices take an enum", enum, t)
	}
}

func (g *generator) tsType(t reflect.Type) (string, error) {
	if g.scalars[t] {
		return "string", nil
	}
	if name, ok := g.names[t]; ok {
		return name, nil
	}
	if marshalsItself(t) {
		return "", fmt.Errorf("%s marshals itself: list it as a scalar", t)
	}
	switch t.Kind() {
	case reflect.String:
		return "string", nil
	case reflect.Bool:
		return "boolean", nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "number", nil
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return "", errors.New("byte slices are not supported")
		}
		if t.Elem().Kind() == reflect.Pointer {
			return "", errors.New("slices of pointers are not supported")
		}
		elem, err := g.tsType(t.Elem())
		if err != nil {
			return "", err
		}
		return elem + "[]", nil
	case reflect.Struct:
		return "", fmt.Errorf("struct %s has no TypeScript name", t)
	default:
		return "", fmt.Errorf("%s (%s) is not supported", t, t.Kind())
	}
}

func marshalsItself(t reflect.Type) bool {
	p := reflect.PointerTo(t)
	return t.Implements(marshalerType) || p.Implements(marshalerType) ||
		t.Implements(textMarshalerType) || p.Implements(textMarshalerType)
}

// propertyName quotes a JSON name that is not a TypeScript identifier.
func propertyName(name string) string {
	if name == "" {
		return quote(name)
	}
	for i, r := range name {
		ok := r == '_' || r == '$' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(i > 0 && r >= '0' && r <= '9')
		if !ok {
			return quote(name)
		}
	}
	return name
}

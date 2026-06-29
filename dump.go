// Copyright (c) the go-ruby-yaml/yaml authors
//
// SPDX-License-Identifier: BSD-3-Clause

package yaml

import (
	"fmt"
	"math"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// encoder serialises a tree of Ruby values to a Psych-compatible YAML document.
// It mirrors MRI Psych's default block style (the layout go-embedded-ruby's
// emitter was validated against): a block sequence that is a mapping value sits
// at the same indent as its key, a nested mapping two spaces deeper, and a
// sequence-dash child continues on the dash line.
type encoder struct {
	b    strings.Builder
	root Value
	// anchors maps an already-emitted reference value (by identity) to its anchor
	// number, so a shared / cyclic node is written once with "&N" and aliased
	// thereafter with "*N".
	anchors map[any]int
	// refcount holds how many times each anchorable node is reachable from root;
	// a count above one forces an anchor. Computed lazily and cached.
	refcount map[any]int
	seq      int
}

// dump renders v as a complete YAML document beginning with the "---" directive.
func dump(v Value) (string, error) {
	v = canon(v)
	e := &encoder{root: v, anchors: map[any]int{}}
	var err error
	err = catch(func() {
		e.b.WriteString("---")
		switch {
		case isComplex(v):
			e.b.WriteByte(' ')
			e.writeAnchorTag(v)
			if e.tagBodyEmpty(v) {
				e.b.WriteString(" {}\n")
				return
			}
			e.b.WriteByte('\n')
			e.encodePairs(e.tagBody(v), 0)
		case isInline(v):
			e.b.WriteByte(' ')
			e.b.WriteString(e.inlineEmpty(v))
			e.b.WriteByte('\n')
		default:
			e.b.WriteByte('\n')
			e.encodeNode(v, 0)
		}
	})
	if err != nil {
		return "", err
	}
	return e.b.String(), nil
}

// dumpErr is the sentinel panic value used to unwind from a deep emission path
// when a value cannot be represented; catch recovers it into a returned error.
type dumpErr struct{ err error }

// catch runs fn and converts a dumpErr panic into a returned error, re-panicking
// any other value.
func catch(fn func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if de, ok := r.(dumpErr); ok {
				err = de.err
				return
			}
			panic(r)
		}
	}()
	fn()
	return nil
}

// fail aborts the current emission with err.
func fail(format string, a ...any) { panic(dumpErr{fmt.Errorf(format, a...)}) }

// isInline reports whether v renders on the document line: any scalar, or an
// empty Array / Map (Psych writes "--- []" / "--- {}").
func isInline(v Value) bool {
	switch n := v.(type) {
	case []any:
		return len(n) == 0
	case *Map:
		return n.Len() == 0
	}
	return true
}

// inlineEmpty renders the document-line value: "[]"/"{}" for an empty collection,
// otherwise the scalar form.
func (e *encoder) inlineEmpty(v Value) string {
	switch n := v.(type) {
	case []any:
		_ = n
		return "[]"
	case *Map:
		_ = n
		return "{}"
	}
	return e.scalar(v)
}

// encodeNode writes a non-empty collection at the given indentation.
func (e *encoder) encodeNode(v Value, indent int) {
	pad := strings.Repeat(" ", indent)
	switch n := v.(type) {
	case []any:
		for _, el := range n {
			e.b.WriteString(pad)
			e.b.WriteByte('-')
			e.writeSeqChild(el, indent)
		}
	case *Map:
		for _, p := range n.pairs {
			if e.writeComplexKey(p.Key, p.Val, indent, pad) {
				continue
			}
			e.b.WriteString(pad)
			e.b.WriteString(e.keyScalar(p.Key))
			e.b.WriteByte(':')
			e.writeMapChild(p.Val, indent)
		}
	}
}

// keyScalar renders a mapping key scalar; a nil key is written as "! ”" so the
// "key:" line always parses (the bare empty scalar would be ambiguous).
func (e *encoder) keyScalar(k Value) string {
	if k == nil {
		return "! ''"
	}
	return e.scalar(k)
}

// writeComplexKey emits a mapping entry whose key is itself non-scalar, using
// Psych's explicit "? <key>" / ": <value>" block form, and reports whether it
// handled the entry.
func (e *encoder) writeComplexKey(k, val Value, indent int, openPad string) bool {
	if !isComplexKey(k) {
		return false
	}
	e.b.WriteString(openPad)
	e.b.WriteByte('?')
	e.writeSeqChild(k, indent)
	e.b.WriteString(strings.Repeat(" ", indent))
	e.b.WriteByte(':')
	e.writeMapChild(val, indent)
	return true
}

// isComplexKey reports whether a mapping key must use the explicit "?"/":" form.
func isComplexKey(v Value) bool {
	switch n := v.(type) {
	case []any:
		return len(n) > 0
	case *Map:
		return n.Len() > 0
	}
	return isComplex(v)
}

// writeMapChild emits the value of a mapping entry (after "key:").
func (e *encoder) writeMapChild(v Value, indent int) {
	if e.writeComplexChild(v, indent) {
		return
	}
	switch n := v.(type) {
	case []any:
		if len(n) == 0 {
			e.b.WriteString(" []\n")
			return
		}
		e.b.WriteByte('\n')
		e.encodeNode(v, indent)
	case *Map:
		if n.Len() == 0 {
			e.b.WriteString(" {}\n")
			return
		}
		e.b.WriteByte('\n')
		e.encodeNode(v, indent+2)
	default:
		e.writeInlineScalar(v, indent)
	}
}

// writeSeqChild emits the value following a sequence dash.
func (e *encoder) writeSeqChild(v Value, indent int) {
	if e.writeComplexSeqChild(v, indent) {
		return
	}
	switch n := v.(type) {
	case []any:
		if len(n) == 0 {
			e.b.WriteString(" []\n")
			return
		}
		e.b.WriteByte(' ')
		e.encodeInlineFirst(v, indent+2)
	case *Map:
		if n.Len() == 0 {
			e.b.WriteString(" {}\n")
			return
		}
		e.b.WriteByte(' ')
		e.encodeInlineFirst(v, indent+2)
	default:
		// A sequence-dash block scalar's body sits two columns past the dash (Psych
		// aligns it under the dash's indent, not the content column).
		e.writeInlineScalar(v, indent)
	}
}

// encodeInlineFirst renders a collection whose first line was opened on the
// parent's dash line.
func (e *encoder) encodeInlineFirst(v Value, indent int) {
	pad := strings.Repeat(" ", indent)
	switch n := v.(type) {
	case []any:
		for i, el := range n {
			if i > 0 {
				e.b.WriteString(pad)
			}
			e.b.WriteByte('-')
			e.writeSeqChild(el, indent)
		}
	case *Map:
		for i, p := range n.pairs {
			openPad := pad
			if i == 0 {
				openPad = ""
			}
			if e.writeComplexKey(p.Key, p.Val, indent, openPad) {
				continue
			}
			if i > 0 {
				e.b.WriteString(pad)
			}
			e.b.WriteString(e.keyScalar(p.Key))
			e.b.WriteByte(':')
			e.writeMapChild(p.Val, indent)
		}
	}
}

// writeInlineScalar writes a scalar value on the current line after a "-" or
// "key:" sitting at the given indent; a nil renders as nothing after the
// indicator (Psych's bare "key:"). A multi-line String renders as a literal block
// scalar whose body is indented two spaces deeper than indent.
func (e *encoder) writeInlineScalar(v Value, indent int) {
	if s, ok := v.(string); ok && s != "" && strings.Contains(s, "\n") {
		e.b.WriteByte(' ')
		e.b.WriteString(blockScalar(s, indent+2))
		e.b.WriteByte('\n')
		return
	}
	s := e.scalar(v)
	if s == "" {
		e.b.WriteByte('\n')
		return
	}
	e.b.WriteByte(' ')
	e.b.WriteString(s)
	e.b.WriteByte('\n')
}

// scalar renders a single non-collection value to its Psych scalar form.
func (e *encoder) scalar(v Value) string {
	switch n := v.(type) {
	case nil:
		return ""
	case bool:
		if n {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(n)
	case int64:
		return strconv.FormatInt(n, 10)
	case *big.Int:
		return n.String()
	case float32:
		return yamlFloat(float64(n))
	case float64:
		return yamlFloat(n)
	case Symbol:
		return yamlSymbol(string(n))
	case string:
		return yamlString(n)
	case time.Time:
		// Psych emits a Time as an unquoted ISO-8601 timestamp, "Z" for UTC and a
		// numeric "+HH:MM" offset otherwise.
		ts := n.Format("2006-01-02 15:04:05.000000000 -07:00")
		if strings.HasSuffix(ts, " +00:00") {
			ts = strings.TrimSuffix(ts, " +00:00") + " Z"
		}
		return ts
	case *Regexp:
		return "!ruby/regexp /" + n.Source + "/" + n.Flags
	case Class:
		return "!ruby/class '" + string(n) + "'"
	case Module:
		return "!ruby/module '" + string(n) + "'"
	}
	fail("can't dump %T to YAML", v)
	return ""
}

// yamlFloat renders a Float the way Psych does (.inf / .nan, trailing ".0" for
// integral values).
func yamlFloat(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return ".inf"
	case math.IsInf(f, -1):
		return "-.inf"
	case math.IsNaN(f):
		return ".nan"
	}
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

// yamlSymbol renders a Ruby Symbol as `:name`; a name carrying a newline / tab is
// escaped via the double-quoted form.
func yamlSymbol(name string) string {
	if strings.ContainsAny(name, "\n\t") {
		return ":" + yamlDoubleQuote(name)
	}
	return ":" + name
}

// yamlString renders a Ruby String following Psych's plain / single-quote /
// double-quote / block-scalar selection.
func yamlString(s string) string {
	if s == "" {
		return "''"
	}
	if strings.Contains(s, "\n") {
		// The document-line / key paths emit at effective indent 0, so the body sits
		// at column 2; the inline-scalar writers re-render at the correct depth.
		return blockScalar(s, 2)
	}
	if needsDoubleQuote(s) {
		return yamlDoubleQuote(s)
	}
	if needsSingleQuote(s) {
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return s
}

// blockScalar renders a multi-line string as a literal block scalar whose body
// lines are indented to bodyIndent columns (Psych's key-indent + 2).
func blockScalar(s string, bodyIndent int) string {
	chomp := "-"
	body := s
	if strings.HasSuffix(s, "\n") {
		chomp = ""
		body = strings.TrimRight(s, "\n")
	}
	pad := "\n" + strings.Repeat(" ", bodyIndent)
	var b strings.Builder
	b.WriteString("|" + chomp)
	for _, line := range strings.Split(body, "\n") {
		b.WriteString(pad)
		b.WriteString(line)
	}
	return b.String()
}

// needsDoubleQuote reports whether a string contains a byte forcing double-quoting
// (control characters) or opens with an indicator best rescued by double quotes.
func needsDoubleQuote(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	switch s[0] {
	case '-', '?', ':', '@', '`', '*', '&', '!', '%', '#', '~', '|', '>', '"', '\'', '{', '}', '[', ']', ',':
		return true
	}
	return false
}

// needsSingleQuote reports whether an otherwise-plain string must be single-quoted
// because it would round-trip as a non-string or carries a flow indicator.
func needsSingleQuote(s string) bool {
	if yamlReservedWord(s) || looksNumericYAML(s) {
		return true
	}
	if strings.HasPrefix(s, "#") || strings.Contains(s, " #") {
		return true
	}
	if strings.HasSuffix(s, ":") || strings.Contains(s, ": ") {
		return true
	}
	return false
}

var yamlReserved = map[string]bool{
	"yes": true, "no": true, "true": true, "false": true,
	"null": true, "~": true, "on": true, "off": true,
	"y": true, "n": true,
}

func yamlReservedWord(s string) bool { return yamlReserved[strings.ToLower(s)] }

var (
	yamlNumRe  = regexp.MustCompile(`\A[-+]?(\d[\d_]*)(\.\d*)?([eE][-+]?\d+)?\z`)
	yamlDateRe = regexp.MustCompile(`\A\d{4}-\d{2}-\d{2}`)
	yamlTimeRe = regexp.MustCompile(`\A\d{1,2}:\d{2}`)
	yamlHexRe  = regexp.MustCompile(`\A0x[0-9A-Fa-f]+\z`)
)

// looksNumericYAML reports whether a plain string would parse back as a number,
// timestamp, or hex literal and so must be quoted to stay a string.
func looksNumericYAML(s string) bool {
	return yamlNumRe.MatchString(s) || yamlDateRe.MatchString(s) || yamlTimeRe.MatchString(s) || yamlHexRe.MatchString(s)
}

// yamlDoubleQuote renders s as a double-quoted YAML scalar, escaping the
// characters Psych escapes.
func yamlDoubleQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		case 0:
			b.WriteString(`\0`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\x%02X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// --- complex values (object / range / class / regexp via tags) ---------------

// isComplex reports whether v is a complex value written with a block tag (an
// Object or a Range). Scalars (including Regexp / Class) and plain collections
// are not complex.
func isComplex(v Value) bool {
	switch v.(type) {
	case *Object, *Range:
		return true
	}
	return false
}

// openTag returns the tag string to write on the opening line of a complex value.
func (e *encoder) openTag(v Value) string {
	switch n := v.(type) {
	case *Object:
		return "!ruby/object" + objectClassSuffix(n)
	default: // *Range
		return "!ruby/range"
	}
}

// objectClassSuffix renders the ":ClassName" suffix of a !ruby/object tag, or ""
// for a bare/anonymous Object.
func objectClassSuffix(o *Object) string {
	if o.Class == "" || o.Class == "Object" {
		return ""
	}
	return ":" + o.Class
}

// tagBodyEmpty reports whether the complex value has no body lines (an object
// with no instance variables), written inline as "<tag> {}".
func (e *encoder) tagBodyEmpty(v Value) bool {
	if o, ok := v.(*Object); ok {
		return len(o.IVars) == 0
	}
	return false
}

// tagBody returns the ordered key/value pairs forming the block body of a complex
// value: an object's instance variables, or a Range's begin/end/excl triple.
func (e *encoder) tagBody(v Value) []Pair {
	switch n := v.(type) {
	case *Object:
		keys := n.orderedIVarKeys()
		pairs := make([]Pair, len(keys))
		for i, k := range keys {
			pairs[i] = Pair{Key: k, Val: n.IVars[k]}
		}
		return pairs
	default: // *Range
		r := n.(*Range)
		return []Pair{
			{"begin", rangeBound(r.Begin)},
			{"end", rangeBound(r.End)},
			{"excl", r.Exclusive},
		}
	}
}

// encodePairs writes a complex value's body as a block mapping at the given
// indent, each key a plain identifier (unquoted, as Psych writes ivar / member
// names).
func (e *encoder) encodePairs(pairs []Pair, indent int) {
	pad := strings.Repeat(" ", indent)
	for _, p := range pairs {
		e.b.WriteString(pad)
		e.b.WriteString(p.Key.(string))
		e.b.WriteByte(':')
		e.writeMapChild(p.Val, indent)
	}
}

// rangeBound maps a Range endpoint, where a beginless / endless bound is nil.
func rangeBound(v Value) Value { return v }

// writeComplexChild emits a complex value (object / range) or a shared collection
// as a mapping entry's value (after "key:").
func (e *encoder) writeComplexChild(v Value, indent int) bool {
	if n, ok := e.alias(v); ok {
		e.b.WriteString(" *")
		e.b.WriteString(strconv.Itoa(n))
		e.b.WriteByte('\n')
		return true
	}
	if e.shared(v) {
		switch v.(type) {
		case []any, *Map:
			e.b.WriteByte(' ')
			e.writeAnchorTag(v)
			e.b.WriteByte('\n')
			e.encodeNode(v, indent+2)
			return true
		}
	}
	if !isComplex(v) {
		return false
	}
	e.b.WriteByte(' ')
	e.writeAnchorTag(v)
	if e.tagBodyEmpty(v) {
		e.b.WriteString(" {}\n")
		return true
	}
	e.b.WriteByte('\n')
	e.encodePairs(e.tagBody(v), indent+2)
	return true
}

// writeComplexSeqChild emits a complex / shared value following a sequence dash.
func (e *encoder) writeComplexSeqChild(v Value, indent int) bool {
	if n, ok := e.alias(v); ok {
		e.b.WriteString(" *")
		e.b.WriteString(strconv.Itoa(n))
		e.b.WriteByte('\n')
		return true
	}
	if e.shared(v) {
		switch v.(type) {
		case []any, *Map:
			e.b.WriteByte(' ')
			e.writeAnchorTag(v)
			e.b.WriteByte('\n')
			e.encodeNode(v, indent+2)
			return true
		}
	}
	if !isComplex(v) {
		return false
	}
	e.b.WriteByte(' ')
	e.writeAnchorTag(v)
	if e.tagBodyEmpty(v) {
		e.b.WriteString(" {}\n")
		return true
	}
	e.b.WriteByte('\n')
	e.encodePairs(e.tagBody(v), indent+2)
	return true
}

// writeAnchorTag writes a value's opening tag, prefixed by "&N " when the value
// is shared.
func (e *encoder) writeAnchorTag(v Value) {
	tag := ""
	if isComplex(v) {
		tag = e.openTag(v)
	}
	if e.shared(v) {
		e.seq++
		e.anchors[identity(v)] = e.seq
		e.b.WriteByte('&')
		e.b.WriteString(strconv.Itoa(e.seq))
		if tag != "" {
			e.b.WriteByte(' ')
		}
	}
	e.b.WriteString(tag)
}

// alias reports whether v has already been emitted under an anchor.
func (e *encoder) alias(v Value) (int, bool) {
	if !anchorable(v) {
		return 0, false
	}
	n, ok := e.anchors[identity(v)]
	return n, ok
}

// shared reports whether v occurs more than once in the value graph.
func (e *encoder) shared(v Value) bool {
	if !anchorable(v) {
		return false
	}
	if e.refcount == nil {
		e.refcount = map[any]int{}
		e.countRefs(e.root, map[any]bool{})
	}
	return e.refcount[identity(v)] > 1
}

// countRefs walks the value graph from v, incrementing the reference count of
// every anchorable node and stopping at a node already entered (so cycles
// terminate).
func (e *encoder) countRefs(v Value, seen map[any]bool) {
	if !anchorable(v) {
		return
	}
	id := identity(v)
	e.refcount[id]++
	if seen[id] {
		return
	}
	seen[id] = true
	switch n := v.(type) {
	case []any:
		for _, el := range n {
			e.countRefs(el, seen)
		}
	case *Map:
		for _, p := range n.pairs {
			e.countRefs(p.Key, seen)
			e.countRefs(p.Val, seen)
		}
	case *Object:
		for _, k := range n.orderedIVarKeys() {
			e.countRefs(n.IVars[k], seen)
		}
	case *Range:
		e.countRefs(n.Begin, seen)
		e.countRefs(n.End, seen)
	}
}

// anchorable reports whether v is a reference type that can be shared behind a
// YAML anchor (non-empty collections and objects). Scalars never get anchors,
// and an empty collection is emitted inline ("[]"/"{}") so it is never anchored —
// this also keeps two distinct empty slices (which share no stable identity) from
// being mistaken for one shared node.
func anchorable(v Value) bool {
	switch n := v.(type) {
	case []any:
		return len(n) > 0
	case *Map:
		return n.Len() > 0
	case *Object, *Range:
		return true
	}
	return false
}

// identity returns a stable map key for a reference value's identity. Slices are
// not comparable, so a non-empty []any is keyed by the address of its first
// element; the pointer-typed shapes key by themselves.
func identity(v Value) any {
	if s, ok := v.([]any); ok {
		return sliceID(s)
	}
	return v
}

// sliceID returns a comparable identity for a non-empty slice (the address of its
// first element, stable for the lifetime of a dump). It is only called for
// anchorable slices, which are non-empty.
func sliceID(s []any) any {
	return &s[0]
}

// --- input canonicalisation ---------------------------------------------------

// canon converts host-provided Go values into the canonical model the encoder
// walks: plain Go maps to a sorted *Map, leaving the already-canonical shapes
// untouched. It recurses so a nested map is converted too, preserving slice
// identity for anchors; a seen set guards cyclic graphs so a self-referential
// collection terminates.
func canon(v Value) Value { return canonRec(v, map[any]bool{}) }

// canonRec is canon's worker, threading the cycle-guard set.
func canonRec(v Value, seen map[any]bool) Value {
	switch n := v.(type) {
	case []any:
		// []Value is an alias of []any, so this case covers both host spellings. An
		// empty slice has no stable identity and no children, so it needs no guard.
		if len(n) == 0 {
			return n
		}
		id := sliceID(n)
		if seen[id] {
			return n
		}
		seen[id] = true
		for i := range n {
			n[i] = canonRec(n[i], seen)
		}
		return n
	case map[string]any:
		return canonStringMap(n, seen)
	case map[Symbol]any:
		return canonSymbolMap(n, seen)
	case *Map:
		if seen[n] {
			return n
		}
		seen[n] = true
		for i := range n.pairs {
			n.pairs[i].Key = canonRec(n.pairs[i].Key, seen)
			n.pairs[i].Val = canonRec(n.pairs[i].Val, seen)
		}
		return n
	case *Object:
		if seen[n] {
			return n
		}
		seen[n] = true
		for k := range n.IVars {
			n.IVars[k] = canonRec(n.IVars[k], seen)
		}
		return n
	case *Range:
		if seen[n] {
			return n
		}
		seen[n] = true
		n.Begin = canonRec(n.Begin, seen)
		n.End = canonRec(n.End, seen)
		return n
	}
	return v
}

// canonStringMap builds a *Map from a Go map keyed by string, in sorted key order.
func canonStringMap(m map[string]any, seen map[any]bool) *Map {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := NewMap()
	for _, k := range keys {
		out.Set(k, canonRec(m[k], seen))
	}
	return out
}

// canonSymbolMap builds a *Map from a Go map keyed by Symbol, in sorted key order.
func canonSymbolMap(m map[Symbol]any, seen map[any]bool) *Map {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, string(k))
	}
	sort.Strings(keys)
	out := NewMap()
	for _, k := range keys {
		out.Set(Symbol(k), canonRec(m[Symbol(k)], seen))
	}
	return out
}

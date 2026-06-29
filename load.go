// Copyright (c) the go-ruby-yaml/yaml authors
//
// SPDX-License-Identifier: BSD-3-Clause

package yaml

import (
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"
)

// loader parses the Psych-compatible YAML subset this package emits — and that
// Puppet's local persistence round-trips — back into Ruby values. It covers
// block mappings and sequences, flow `[]` / `{}`, plain / single- / double-quoted
// / literal-block scalars, the Psych scalar keywords, Symbols (`:name`), the
// `!ruby/symbol` / `!ruby/object:` / `!ruby/range` / `!ruby/class` tags, and
// `&anchor` / `*alias` references.
type loader struct {
	lines   []line
	pos     int
	anchors map[string]Value
}

// line is one physical input line split into its leading indentation and the
// remaining content; raw is the full line, used by literal block scalars. A
// blank line carries blank=true: it is significant only inside a block scalar
// (where it preserves a paragraph break) and is skipped by every other parser.
type line struct {
	indent  int
	content string
	raw     string
	blank   bool
}

// load parses a complete YAML document string into a Ruby value. A blank or
// marker-only document loads as nil (Psych's empty-document behaviour).
func load(src string) (Value, error) {
	l := &loader{anchors: map[string]Value{}}
	if err := checkTabs(src); err != nil {
		return nil, err
	}
	l.tokenize(src)
	l.skipBlanks()
	if l.pos >= len(l.lines) {
		return nil, nil
	}
	markerPos := l.pos
	first := l.lines[markerPos]
	if first.content == "---" || strings.HasPrefix(first.content, "--- ") {
		rest := ""
		if len(first.content) > 4 {
			rest = strings.TrimSpace(first.content[4:])
		}
		if rest == "" {
			l.pos = markerPos + 1
			if l.peek() == nil {
				return nil, nil
			}
			return l.parseNode(0), nil
		}
		if style, chomp, ok := blockScalarTag(rest); ok {
			l.pos = markerPos + 1
			return l.parseBlockScalar(style, chomp, first.indent), nil
		}
		if tag, _, content := splitTagAnchor(rest); tag != "" && content == "" {
			l.pos = markerPos + 1
			if l.peek() == nil {
				return l.taggedEmpty(tag), nil
			}
			if isSeqEntry(l.peek().content) {
				return l.parseSequence(l.peek().indent, tag), nil
			}
			return l.parseMapping(l.peek().indent, tag), nil
		}
		l.lines[markerPos].content = rest
		l.lines[markerPos].indent = first.indent
		return l.parseNode(0), nil
	}
	return l.parseNode(0), nil
}

// checkTabs rejects a source that uses a tab character for line indentation,
// which YAML (and Psych) forbid; this is the one structural error the loader
// reports, the rest of the grammar being deliberately tolerant.
func checkTabs(src string) error {
	for _, raw := range strings.Split(src, "\n") {
		i := 0
		for i < len(raw) && raw[i] == ' ' {
			i++
		}
		if i < len(raw) && raw[i] == '\t' {
			return errTabIndent
		}
	}
	return nil
}

// errTabIndent is returned by Load / SafeLoad for tab-indented input.
var errTabIndent = &SyntaxError{Message: "found a tab character used for indentation"}

// SyntaxError is the error type Load / SafeLoad return for a malformed document
// (mirroring Psych::SyntaxError). It carries a human-readable message.
type SyntaxError struct{ Message string }

// Error implements error.
func (e *SyntaxError) Error() string { return "yaml: " + e.Message }

// tokenize splits src into lines, dropping whole-line comments; the document-end
// marker "..." ends input. Blank lines are retained as blank=true entries so a
// literal block scalar can preserve paragraph breaks; all other parsers skip
// them via skipBlanks / peek.
func (l *loader) tokenize(src string) {
	for _, raw := range strings.Split(src, "\n") {
		trimmed := strings.TrimRight(raw, "\r")
		content := strings.TrimLeft(trimmed, " ")
		if content == "" {
			l.lines = append(l.lines, line{blank: true, raw: trimmed})
			continue
		}
		if content == "..." {
			break
		}
		if strings.HasPrefix(content, "#") {
			continue
		}
		indent := len(trimmed) - len(content)
		l.lines = append(l.lines, line{indent: indent, content: content, raw: trimmed})
	}
	// Trim trailing blank lines so a document is not seen as non-empty for them.
	for len(l.lines) > 0 && l.lines[len(l.lines)-1].blank {
		l.lines = l.lines[:len(l.lines)-1]
	}
}

// blockScalarTag reports whether content is a literal / folded block-scalar
// indicator, returning the style byte ('|' or '>') and chomp ('-', '+', or 0).
func blockScalarTag(content string) (style, chomp byte, ok bool) {
	if content == "" || (content[0] != '|' && content[0] != '>') {
		return 0, 0, false
	}
	rest := content[1:]
	if rest == "" {
		return content[0], 0, true
	}
	if rest == "-" || rest == "+" {
		return content[0], rest[0], true
	}
	return 0, 0, false
}

// parseBlockScalar reads a literal / folded block scalar whose indicator is on the
// current line; the body is the following lines indented deeper than parentIndent.
func (l *loader) parseBlockScalar(style, chomp byte, parentIndent int) Value {
	var body []string
	bodyIndent := -1
	for l.pos < len(l.lines) {
		ln := l.lines[l.pos]
		if ln.blank {
			// A blank line is a paragraph break inside the block (its raw text, after
			// stripping the base indent, is empty); a trailing blank was already
			// trimmed by tokenize so this never runs past the block's end.
			body = append(body, "")
			l.pos++
			continue
		}
		if ln.indent <= parentIndent {
			break
		}
		if bodyIndent < 0 {
			bodyIndent = ln.indent
		}
		cut := bodyIndent
		if cut > len(ln.raw) {
			cut = len(ln.raw)
		}
		body = append(body, ln.raw[cut:])
		l.pos++
	}
	if len(body) == 0 {
		// An empty literal / folded block (no body lines) is the empty string,
		// regardless of chomp (Psych returns "").
		return ""
	}
	var s string
	if style == '>' {
		s = strings.Join(body, " ")
	} else {
		s = strings.Join(body, "\n")
	}
	switch chomp {
	case '-':
	case '+':
		s += "\n"
	default:
		s += "\n"
	}
	return s
}

// parseNode parses the node whose first line is l.lines[l.pos].
func (l *loader) parseNode(minIndent int) Value {
	_ = minIndent
	l.skipBlanks()
	ln := l.lines[l.pos]
	tag, anchorName, content := splitTagAnchor(ln.content)
	if content == "" {
		l.lines[l.pos].content = ""
		l.pos++
		v := l.parseBlock(ln.indent, tag)
		l.bind(anchorName, v)
		return v
	}
	l.lines[l.pos].content = content
	if isSeqEntry(content) {
		v := l.parseSequence(ln.indent, tag)
		l.bind(anchorName, v)
		return v
	}
	if !isFlowStart(content) {
		_, _, isMap := splitMapEntry(content)
		if isMap || isExplicitKey(content) {
			v := l.parseMapping(ln.indent, tag)
			l.bind(anchorName, v)
			return v
		}
	}
	l.pos++
	v := l.scalarValue(content, tag)
	l.bind(anchorName, v)
	return v
}

// parseBlock parses the block that follows a standalone tag / anchor.
func (l *loader) parseBlock(parentIndent int, tag string) Value {
	l.skipBlanks()
	if l.pos >= len(l.lines) || l.lines[l.pos].indent < parentIndent {
		return l.taggedEmpty(tag)
	}
	child := l.lines[l.pos]
	if isSeqEntry(child.content) {
		return l.parseSequence(child.indent, tag)
	}
	return l.parseMapping(child.indent, tag)
}

// parseSequence parses a block sequence: consecutive "- …" lines at exactly
// indent.
func (l *loader) parseSequence(indent int, tag string) Value {
	arr := []any{}
	for {
		l.skipBlanks()
		if l.pos >= len(l.lines) {
			break
		}
		ln := l.lines[l.pos]
		if ln.indent != indent || !isSeqEntry(ln.content) {
			break
		}
		rest := strings.TrimPrefix(ln.content, "-")
		rest = strings.TrimPrefix(rest, " ")
		if rest == "" {
			l.pos++
			if l.peek() != nil && l.peek().indent > indent {
				arr = append(arr, l.parseBlock(indent, ""))
			} else {
				arr = append(arr, nil)
			}
			continue
		}
		if style, chomp, ok := blockScalarTag(rest); ok {
			l.pos++
			arr = append(arr, l.parseBlockScalar(style, chomp, indent))
			continue
		}
		l.lines[l.pos].content = rest
		l.lines[l.pos].indent = indent + 2
		arr = append(arr, l.parseNode(indent+1))
	}
	return l.applySeqTag(arr, tag)
}

// parseMapping parses a block mapping: consecutive "key: …" lines at exactly
// indent. An explicit "? key" / ": value" entry carries a complex key.
func (l *loader) parseMapping(indent int, tag string) Value {
	h := NewMap()
	for {
		l.skipBlanks()
		if l.pos >= len(l.lines) {
			break
		}
		ln := l.lines[l.pos]
		if ln.indent != indent {
			break
		}
		if key, ok := l.explicitKey(ln, indent); ok {
			val := l.explicitValue(indent)
			h.Set(key, val)
			continue
		}
		keyStr, val, ok := splitMapEntry(ln.content)
		if !ok {
			break
		}
		key := l.scalarValue(keyStr, "")
		if strings.TrimSpace(val) == "" {
			l.pos++
			if next := l.peek(); next != nil && (next.indent > indent || (next.indent == indent && isSeqEntry(next.content))) {
				h.Set(key, l.parseNode(indent))
			} else {
				h.Set(key, nil)
			}
			continue
		}
		if style, chomp, ok := blockScalarTag(strings.TrimSpace(val)); ok {
			l.pos++
			h.Set(key, l.parseBlockScalar(style, chomp, indent))
			continue
		}
		l.lines[l.pos].content = strings.TrimSpace(val)
		l.lines[l.pos].indent = indent + 1
		h.Set(key, l.parseNode(indent+1))
	}
	return l.applyMapTag(h, tag)
}

// isExplicitKey reports whether content opens an explicit "? <key>" mapping entry.
func isExplicitKey(content string) bool {
	return content == "?" || strings.HasPrefix(content, "? ")
}

// explicitKey parses an explicit "? <key>" opener at the mapping indent, returning
// the parsed key. The key body either follows "? " inline or on the indented
// block beneath.
func (l *loader) explicitKey(ln line, indent int) (Value, bool) {
	if !isExplicitKey(ln.content) {
		return nil, false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(ln.content, "?"))
	if rest == "" {
		l.pos++
		return l.parseBlock(indent, ""), true
	}
	l.lines[l.pos].content = rest
	l.lines[l.pos].indent = indent + 2
	return l.parseNode(indent + 1), true
}

// explicitValue parses the ": <value>" half of an explicit-key entry, which sits
// at the mapping indent following the parsed key.
func (l *loader) explicitValue(indent int) Value {
	l.skipBlanks()
	if l.pos >= len(l.lines) {
		return nil
	}
	ln := l.lines[l.pos]
	if ln.indent != indent || (ln.content != ":" && !strings.HasPrefix(ln.content, ": ")) {
		// No matching ":" line — the key has a nil value (defensive).
		return nil
	}
	rest := strings.TrimSpace(strings.TrimPrefix(ln.content, ":"))
	if rest == "" {
		l.pos++
		if next := l.peek(); next != nil && (next.indent > indent || (next.indent == indent && isSeqEntry(next.content))) {
			return l.parseNode(indent)
		}
		return nil
	}
	l.lines[l.pos].content = rest
	l.lines[l.pos].indent = indent + 1
	return l.parseNode(indent + 1)
}

// scalarValue parses a single scalar token to a Ruby value, honouring an explicit
// tag and the implicit Psych scalar grammar otherwise.
func (l *loader) scalarValue(s string, tag string) Value {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "*") {
		if v, ok := l.anchors[strings.TrimSpace(s[1:])]; ok {
			return v
		}
		return nil
	}
	switch {
	case s == "[]":
		return l.applySeqTag([]any{}, tag)
	case s == "{}":
		return l.applyMapTag(NewMap(), tag)
	case strings.HasPrefix(s, "["):
		return l.parseFlowSeq(s)
	case strings.HasPrefix(s, "{"):
		return l.parseFlowMap(s)
	}
	switch tag {
	case "!ruby/symbol", "!ruby/sym":
		return Symbol(unquoteScalar(s))
	case "!ruby/string", "!str", "tag:yaml.org,2002:str":
		return unquoteScalar(s)
	case "!ruby/regexp":
		return parseRegexpScalar(s)
	case "!ruby/class":
		return Class(unquoteScalar(s))
	case "!ruby/module":
		return Module(unquoteScalar(s))
	}
	return parsePlainScalar(s)
}

// parseRegexpScalar parses the inline `/source/flags` body of a !ruby/regexp tag.
func parseRegexpScalar(s string) Value {
	if len(s) >= 2 && s[0] == '/' {
		if i := strings.LastIndexByte(s, '/'); i > 0 {
			return &Regexp{Source: s[1:i], Flags: s[i+1:]}
		}
	}
	return &Regexp{Source: s}
}

// parseFlowSeq parses a single-line flow sequence "[a, b, c]".
func (l *loader) parseFlowSeq(s string) Value {
	inner := strings.TrimSpace(s[1 : len(s)-1])
	arr := []any{}
	if inner == "" {
		return arr
	}
	for _, item := range splitFlow(inner) {
		arr = append(arr, l.scalarValue(item, ""))
	}
	return arr
}

// parseFlowMap parses a single-line flow mapping "{a: 1, b: 2}".
func (l *loader) parseFlowMap(s string) Value {
	inner := strings.TrimSpace(s[1 : len(s)-1])
	h := NewMap()
	if inner == "" {
		return h
	}
	for _, item := range splitFlow(inner) {
		k, v, ok := splitMapEntry(item)
		if !ok {
			continue
		}
		h.Set(l.scalarValue(k, ""), l.scalarValue(v, ""))
	}
	return h
}

// peek skips any blank lines at the cursor and returns the next significant line,
// or nil at end of input. Skipping advances pos so the structural parsers never
// observe a blank line (only parseBlockScalar reads blanks, directly).
func (l *loader) peek() *line {
	l.skipBlanks()
	if l.pos >= len(l.lines) {
		return nil
	}
	return &l.lines[l.pos]
}

// skipBlanks advances the cursor past blank lines.
func (l *loader) skipBlanks() {
	for l.pos < len(l.lines) && l.lines[l.pos].blank {
		l.pos++
	}
}

// bind records v under anchorName for later *alias references.
func (l *loader) bind(anchorName string, v Value) {
	if anchorName != "" {
		l.anchors[anchorName] = v
	}
}

// applySeqTag adapts a parsed sequence to its tag (only the plain sequence is
// meaningful; an unknown tag is ignored).
func (l *loader) applySeqTag(arr []any, _ string) Value { return arr }

// applyMapTag adapts a parsed mapping to its tag: a `!ruby/object:Class` mapping
// becomes an *Object, a `!ruby/range` becomes a *Range, other tags leave the Map.
func (l *loader) applyMapTag(h *Map, tag string) Value {
	if cls, ok := rubyObjectTag(tag); ok {
		return buildObject(cls, h)
	}
	if tag == "!ruby/range" {
		return buildRange(h)
	}
	return h
}

// taggedEmpty builds the value for a tag with no body.
func (l *loader) taggedEmpty(tag string) Value {
	if cls, ok := rubyObjectTag(tag); ok {
		return buildObject(cls, NewMap())
	}
	if tag == "!ruby/range" {
		return buildRange(NewMap())
	}
	return NewMap()
}

// buildObject materialises a `!ruby/object:Class` instance: an *Object of the
// named class with one IVar per mapping entry (bare name).
func buildObject(className string, h *Map) Value {
	obj := &Object{Class: className, IVars: map[string]Value{}}
	for _, p := range h.pairs {
		name := keyName(p.Key)
		obj.IVars[name] = p.Val
		obj.Order = append(obj.Order, name)
	}
	return obj
}

// buildRange materialises a `!ruby/range` mapping into a *Range.
func buildRange(h *Map) Value {
	r := &Range{}
	if v, ok := h.Get("begin"); ok {
		r.Begin = v
	}
	if v, ok := h.Get("end"); ok {
		r.End = v
	}
	if v, ok := h.Get("excl"); ok {
		if b, isBool := v.(bool); isBool {
			r.Exclusive = b
		}
	}
	return r
}

// keyName renders a mapping key as the bare ivar / member name (a Symbol or
// String key, both used by Psych object mappings).
func keyName(k Value) string {
	switch kk := k.(type) {
	case Symbol:
		return string(kk)
	case string:
		return kk
	}
	return ""
}

// --- scalar grammar -----------------------------------------------------------

// parsePlainScalar maps an unquoted / quoted scalar token to a Ruby value using
// Psych's implicit typing.
func parsePlainScalar(s string) Value {
	if s == "" {
		return nil
	}
	if s[0] == '\'' {
		return unquoteSingle(s)
	}
	if s[0] == '"' {
		return unquoteDouble(s)
	}
	switch s {
	case "~", "null", "Null", "NULL":
		return nil
	case "true", "True", "TRUE":
		return true
	case "false", "False", "FALSE":
		return false
	case ".inf", ".Inf", ".INF", "+.inf":
		return math.Inf(1)
	case "-.inf", "-.Inf", "-.INF":
		return math.Inf(-1)
	case ".nan", ".NaN", ".NAN":
		return math.NaN()
	}
	if strings.HasPrefix(s, ":") {
		return Symbol(unquoteScalar(s[1:]))
	}
	if t, ok := parseYAMLTime(s); ok {
		return t
	}
	if v, ok := parseYAMLInteger(s); ok {
		return v
	}
	if fv, ok := parseYAMLFloat(s); ok {
		return fv
	}
	return s
}

// parseYAMLInteger parses a YAML integer scalar (decimal with optional sign /
// underscores, or 0x/0o/0b base prefixes) to an int64 or, on overflow, a *big.Int.
func parseYAMLInteger(s string) (Value, bool) {
	clean := strings.ReplaceAll(s, "_", "")
	if clean == "" {
		return nil, false
	}
	base := 10
	digits := clean
	if pfx, b := stripBasePrefix(clean); b != 0 {
		base, digits = b, pfx
	}
	bi, ok := new(big.Int).SetString(digits, base)
	if !ok {
		return nil, false
	}
	return normInt(bi), true
}

// normInt narrows a *big.Int to int64 when it fits, else returns the *big.Int.
func normInt(bi *big.Int) Value {
	if bi.IsInt64() {
		return bi.Int64()
	}
	return bi
}

// stripBasePrefix returns the digits and base of a 0x / 0o / 0b literal
// (preserving the sign), or "", 0 when s carries no base prefix.
func stripBasePrefix(s string) (string, int) {
	sign := ""
	body := s
	if len(body) > 0 && (body[0] == '+' || body[0] == '-') {
		if body[0] == '-' {
			sign = "-"
		}
		body = body[1:]
	}
	if len(body) < 2 || body[0] != '0' {
		return "", 0
	}
	switch body[1] {
	case 'x', 'X':
		return sign + body[2:], 16
	case 'o', 'O':
		return sign + body[2:], 8
	case 'b', 'B':
		return sign + body[2:], 2
	}
	return "", 0
}

// parseYAMLFloat parses a YAML float scalar (it must carry a '.' or exponent).
func parseYAMLFloat(s string) (float64, bool) {
	if !strings.ContainsAny(s, ".eE") {
		return 0, false
	}
	clean := strings.ReplaceAll(s, "_", "")
	f, err := strconv.ParseFloat(clean, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// parseYAMLTime parses the Psych ISO-8601 timestamps the emitter writes.
func parseYAMLTime(s string) (Value, bool) {
	layouts := []string{
		"2006-01-02 15:04:05.000000000 Z",
		"2006-01-02 15:04:05.000000000 -07:00",
		"2006-01-02 15:04:05 Z",
		"2006-01-02 15:04:05 -07:00",
		"2006-01-02T15:04:05Z07:00",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return nil, false
}

// --- token helpers ------------------------------------------------------------

// isSeqEntry reports whether content is a block-sequence entry.
func isSeqEntry(content string) bool {
	return content == "-" || strings.HasPrefix(content, "- ")
}

// isFlowStart reports whether content opens a flow collection.
func isFlowStart(content string) bool {
	return strings.HasPrefix(content, "[") || strings.HasPrefix(content, "{")
}

// splitMapEntry splits "key: value" into its key and (possibly empty) value,
// honouring quoted keys.
func splitMapEntry(content string) (key, value string, ok bool) {
	i := mapColon(content)
	if i < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(content[:i])
	value = strings.TrimSpace(content[i+1:])
	return key, value, true
}

// mapColon returns the index of the key/value separator at the top flow level.
func mapColon(content string) int {
	var quote byte
	for i := 0; i < len(content); i++ {
		c := content[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case ':':
			if i == len(content)-1 || content[i+1] == ' ' {
				return i
			}
		}
	}
	return -1
}

// splitTagAnchor peels a leading "!tag" and/or "&anchor" from a node's first line.
func splitTagAnchor(content string) (tag, anchor, rest string) {
	rest = content
	for {
		rest = strings.TrimLeft(rest, " ")
		switch {
		case strings.HasPrefix(rest, "&"):
			anchor, rest = firstWord(rest[1:])
		case strings.HasPrefix(rest, "!"):
			tag, rest = firstWord(rest)
		default:
			return tag, anchor, strings.TrimLeft(rest, " ")
		}
	}
}

// firstWord splits s at its first space.
func firstWord(s string) (word, rest string) {
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// rubyObjectTag reports whether tag is a `!ruby/object[:Class]` tag.
func rubyObjectTag(tag string) (string, bool) {
	const p = "!ruby/object"
	if tag == p {
		return "Object", true
	}
	if strings.HasPrefix(tag, p+":") {
		return tag[len(p)+1:], true
	}
	return "", false
}

// splitFlow splits a flow-collection body on top-level commas.
func splitFlow(s string) []string {
	var parts []string
	depth := 0
	var quote byte
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '[', '{':
			depth++
		case ']', '}':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	parts = append(parts, strings.TrimSpace(s[start:]))
	return parts
}

// unquoteScalar strips surrounding quotes (and applies their escaping) if present.
func unquoteScalar(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '\'' {
		return unquoteSingle(s)
	}
	if len(s) >= 2 && s[0] == '"' {
		return unquoteDouble(s)
	}
	return s
}

// unquoteSingle decodes a single-quoted YAML scalar (” is a literal quote).
func unquoteSingle(s string) string {
	body := s
	if len(body) >= 2 && body[0] == '\'' && body[len(body)-1] == '\'' {
		body = body[1 : len(body)-1]
	}
	return strings.ReplaceAll(body, "''", "'")
}

// unquoteDouble decodes a double-quoted YAML scalar.
func unquoteDouble(s string) string {
	body := s
	if len(body) >= 2 && body[0] == '"' && body[len(body)-1] == '"' {
		body = body[1 : len(body)-1]
	}
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c != '\\' || i+1 >= len(body) {
			b.WriteByte(c)
			continue
		}
		i++
		switch body[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '0':
			b.WriteByte(0)
		case '"':
			b.WriteByte('"')
		case '\\':
			b.WriteByte('\\')
		case 'x':
			if i+2 < len(body) {
				if n, err := strconv.ParseUint(body[i+1:i+3], 16, 8); err == nil {
					b.WriteByte(byte(n))
					i += 2
					continue
				}
			}
			b.WriteByte('x')
		default:
			b.WriteByte(body[i])
		}
	}
	return b.String()
}

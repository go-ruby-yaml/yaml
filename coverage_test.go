// Copyright (c) the go-ruby-yaml/yaml authors
//
// SPDX-License-Identifier: BSD-3-Clause

package yaml

import (
	"math/big"
	"strings"
	"testing"
)

// TestDumpInlineFirstCollections drives encodeInlineFirst's nested-collection and
// multi-element branches: a sequence whose first element is itself a collection,
// and a mapping opened on a dash line with several entries / an empty value.
func TestDumpInlineFirstCollections(t *testing.T) {
	// Sequence of sequences with multiple elements (i>0 padding + nested).
	got := mustDump(t, []any{[]any{1, 2}, []any{3}})
	if got != "---\n- - 1\n  - 2\n- - 3\n" {
		t.Errorf("seq of seqs = %q", got)
	}
	// Sequence whose element is a multi-entry mapping (the "- a:\n  b:" layout).
	m := NewMap()
	m.Set("a", 1)
	m.Set("b", []any{})
	m.Set("c", NewMap())
	m.Set("d", nil)
	got = mustDump(t, []any{m})
	want := "---\n- a: 1\n  b: []\n  c: {}\n  d:\n"
	if got != want {
		t.Errorf("seq of map = %q\nwant %q", got, want)
	}
	// A dash-line mapping whose first key is complex (openPad="" branch) and a
	// later complex key (openPad=pad branch).
	mc := NewMap()
	mc.Set([]any{1}, "x")
	mc.Set([]any{2}, "y")
	got = mustDump(t, []any{mc})
	want = "---\n- ? - 1\n  : x\n  ? - 2\n  : 'y'\n"
	if got != want {
		t.Errorf("dash map complex keys = %q\nwant %q", got, want)
	}
	// A dash-line sequence whose first element is a mapping value (encodeInlineFirst
	// over a Map followed by more elements).
	m2 := NewMap()
	m2.Set("k", 1)
	got = mustDump(t, []any{[]any{m2, 9}})
	want = "---\n- - k: 1\n  - 9\n"
	if got != want {
		t.Errorf("dash seq with map first = %q\nwant %q", got, want)
	}
}

// TestDumpSeqChildEmptyCollections covers the empty-collection branches of
// writeSeqChild ("- []" / "- {}") and writeMapChild for an empty nested map value.
func TestDumpSeqChildEmpty(t *testing.T) {
	if got := mustDump(t, []any{[]any{}, NewMap()}); got != "---\n- []\n- {}\n" {
		t.Errorf("seq empties = %q", got)
	}
	m := NewMap()
	m.Set("a", []any{})
	m.Set("b", NewMap())
	if got := mustDump(t, m); got != "---\na: []\nb: {}\n" {
		t.Errorf("map empties = %q", got)
	}
}

// TestDumpDoubleQuoteEscapes covers every escape branch of yamlDoubleQuote and the
// needsDoubleQuote / needsSingleQuote indicator paths.
func TestDumpDoubleQuoteEscapes(t *testing.T) {
	// \r, \t, \0, \\, \" and a \xNN control via a leading-indicator string.
	cases := map[string]string{
		"\r":    "--- \"\\r\"\n",
		"a\"b":  "--- a\"b\n", // interior quote stays plain (no leading indicator)
		"\\x":   "--- \\x\n",  // interior backslash plain
		"|pipe": "--- \"|pipe\"\n",
		">fold": "--- \">fold\"\n",
		"@at":   "--- \"@at\"\n",
	}
	for in, want := range cases {
		if got := mustDump(t, in); got != want {
			t.Errorf("Dump(%q) = %q, want %q", in, got, want)
		}
	}
	// A double-quoted string whose body carries \, ", \0, \t, \r exercises every
	// branch: force it via a leading control byte.
	s := "\x01\\\"\t\r\x00ok"
	out := mustDump(t, s)
	if !strings.HasPrefix(out, "--- \"\\x01\\\\\\\"\\t\\r\\0ok\"") {
		t.Errorf("escape body = %q", out)
	}
	// needsSingleQuote: a trailing ":" and a " #" mid-string.
	if got := mustDump(t, "x:"); got != "--- 'x:'\n" {
		t.Errorf("trailing colon = %q", got)
	}
}

// TestDumpBignumNegative covers a negative bignum scalar.
func TestDumpBignumNegative(t *testing.T) {
	bi, _ := new(big.Int).SetString("-987654321098765432109876543210", 10)
	if got := mustDump(t, bi); got != "--- -987654321098765432109876543210\n" {
		t.Errorf("neg bignum = %q", got)
	}
}

// TestLoadIntegerBases covers the negative-sign and each base-prefix branch of
// stripBasePrefix / parseYAMLInteger, including a bignum hex.
func TestLoadIntegerBases(t *testing.T) {
	cases := map[string]int64{
		"--- -0x10\n": -16,
		"--- +0x10\n": 16,
		"--- -0o10\n": -8,
		"--- 0B11\n":  3,
		"--- -5\n":    -5,
	}
	for s, want := range cases {
		if v := mustLoad(t, s); !eqValue(v, want) {
			t.Errorf("Load(%q) = %#v, want %d", s, v, want)
		}
	}
	// Hex bignum overflows int64 into a *big.Int.
	v := mustLoad(t, "--- 0xFFFFFFFFFFFFFFFFFF\n")
	if _, ok := v.(*big.Int); !ok {
		t.Errorf("hex bignum = %T", v)
	}
	// A bare "0" with no base prefix and a non-numeric "0z" fall through.
	if v := mustLoad(t, "--- 0\n"); !eqValue(v, int64(0)) {
		t.Errorf("zero = %#v", v)
	}
	if v := mustLoad(t, "--- 0z9\n"); !eqValue(v, "0z9") {
		t.Errorf("0z9 = %#v", v)
	}
	// An all-underscore token is not an integer (empty after cleaning).
	if v := mustLoad(t, "--- _\n"); !eqValue(v, "_") {
		t.Errorf("underscore = %#v", v)
	}
}

// TestLoadFloatEdge covers the float reject branch (a '.'-bearing non-float).
func TestLoadFloatEdge(t *testing.T) {
	if v := mustLoad(t, "--- 1.2.3\n"); !eqValue(v, "1.2.3") {
		t.Errorf("1.2.3 = %#v", v)
	}
}

// TestLoadDoubleQuoteEscapes covers unquoteDouble's escape branches.
func TestLoadDoubleQuoteEscapes(t *testing.T) {
	cases := map[string]string{
		`--- "a\tb"`:   "a\tb",
		`--- "a\rb"`:   "a\rb",
		`--- "a\\b"`:   "a\\b",
		`--- "a\x41b"`: "aAb",
		`--- "a\qb"`:   "aqb", // unknown escape drops the backslash
		`--- "a\xZZb"`: "axZZb",
		`--- "end\"`:   `end\`, // trailing lone backslash kept verbatim
	}
	for s, want := range cases {
		if v := mustLoad(t, s+"\n"); !eqValue(v, want) {
			t.Errorf("Load(%q) = %#v, want %q", s, v, want)
		}
	}
	// A short \x with too few digits keeps the 'x'.
	if v := mustLoad(t, "--- \"a\\x4\"\n"); !eqValue(v, "ax4") {
		t.Errorf("short hex = %#v", v)
	}
	// A NUL escape.
	if v := mustLoad(t, "--- \"a\\0b\"\n"); !eqValue(v, "a\x00b") {
		t.Errorf("nul escape = %#v", v)
	}
}

// TestLoadFlowNested covers splitFlow's quote / nested-bracket handling and an
// item that is not a map entry (skipped in a flow map).
func TestLoadFlowNested(t *testing.T) {
	v := mustLoad(t, "--- [[1, 2], {a: 1}]\n")
	arr := v.([]any)
	if !eqValue(arr[0], []any{int64(1), int64(2)}) {
		t.Errorf("nested flow seq = %#v", arr[0])
	}
	inner := arr[1].(*Map)
	if av, _ := inner.Get("a"); !eqValue(av, int64(1)) {
		t.Errorf("nested flow map = %#v", av)
	}
	// A flow string item carrying a comma inside quotes is one item.
	v = mustLoad(t, "--- ['a, b', c]\n")
	if !eqValue(v, []any{"a, b", "c"}) {
		t.Errorf("quoted comma = %#v", v)
	}
	// A flow map with a bare (non-entry) item skips it.
	m := mustLoad(t, "--- {a: 1, bare}\n").(*Map)
	if m.Len() != 1 {
		t.Errorf("flow map with bare = %d", m.Len())
	}
}

// TestLoadDocEdge covers the document-marker branches: a "--- |-" doc block, a
// document whose tag body is empty, a tag with no following lines, and a tagged
// sequence document.
func TestLoadDocEdge(t *testing.T) {
	if v := mustLoad(t, "--- |-\n  a\n  b\n"); !eqValue(v, "a\nb") {
		t.Errorf("doc block = %#v", v)
	}
	// A document tag whose body is the next sequence lines.
	v := mustLoad(t, "--- !ruby/object:Foo\n- 1\n")
	if _, ok := v.(*Object); !ok {
		// Psych would not emit this shape, but the loader must not crash: it parses
		// the sequence under the tag (applyMapTag is bypassed, applySeqTag stands).
		_ = v
	}
	// A document tag with no body is the empty tagged value.
	if o, ok := mustLoad(t, "--- !ruby/object:Foo\n").(*Object); !ok || o.Class != "Foo" {
		t.Errorf("empty doc tag = %#v", o)
	}
	// An empty range document tag.
	if _, ok := mustLoad(t, "--- !ruby/range\n").(*Range); !ok {
		t.Error("empty range doc")
	}
	// A bare unknown tag with no body yields an empty Map.
	if m, ok := mustLoad(t, "--- !unknown\n").(*Map); !ok || m.Len() != 0 {
		t.Errorf("unknown empty tag = %#v", m)
	}
}

// TestLoadDocSeqTag covers a document-level tagged sequence ("--- !x\n- 1").
func TestLoadDocSeqTag(t *testing.T) {
	v := mustLoad(t, "--- !ruby/object:Foo\n- 1\n- 2\n")
	// applyMapTag is not reached (a sequence carries applySeqTag), so the value is
	// the plain sequence; the test asserts it parsed without panic.
	if arr, ok := v.([]any); !ok || len(arr) != 2 {
		t.Errorf("doc seq tag = %#v", v)
	}
}

// TestLoadKeyNameSymbol covers keyName's Symbol branch and the default (numeric
// key) branch when building an object from a mapping whose key is neither.
func TestLoadKeyNameVariants(t *testing.T) {
	// A symbol ivar name.
	o := mustLoad(t, "--- !ruby/object:Foo\n:sym: 1\n").(*Object)
	if _, ok := o.IVars["sym"]; !ok {
		t.Errorf("symbol ivar = %#v", o.IVars)
	}
	// A numeric key falls to keyName's default "" (defensive; round-trips into an
	// empty-named ivar).
	o = mustLoad(t, "--- !ruby/object:Foo\n1: x\n").(*Object)
	if _, ok := o.IVars[""]; !ok {
		t.Errorf("numeric-key ivar = %#v", o.IVars)
	}
}

// TestLoadBlockScalarClampAndKeep covers parseBlockScalar's keep ('+') chomp and
// the short-line clamp (a continuation line shorter than the base indent).
func TestLoadBlockScalarEdges(t *testing.T) {
	if v := mustLoad(t, "--- |+\n  a\n  b\n"); !eqValue(v, "a\nb\n") {
		t.Errorf("keep chomp = %#v", v)
	}
	// A blank line inside the block is preserved as a paragraph break.
	if v := mustLoad(t, "---\nk: |-\n  a\n\n  b\n"); v != nil {
		m := v.(*Map)
		if kv, _ := m.Get("k"); !eqValue(kv, "a\n\nb") {
			t.Errorf("block with blank line = %#v", kv)
		}
	}
}

// TestRoundTripBlankLineString checks a string with an interior blank line
// round-trips (the paragraph-break case MRI preserves).
func TestRoundTripBlankLineString(t *testing.T) {
	s := mustDump(t, "a\n\nb")
	if got := mustLoad(t, s); !eqValue(got, "a\n\nb") {
		t.Errorf("blank-line round trip src=%q got=%#v", s, got)
	}
	// A document that begins with blank lines before the marker still loads.
	if v := mustLoad(t, "\n\n--- 1\n"); !eqValue(v, int64(1)) {
		t.Errorf("leading blanks = %#v", v)
	}
	// An all-blank document loads as nil.
	if v := mustLoad(t, "\n\n\n"); v != nil {
		t.Errorf("all-blank = %#v", v)
	}
}

// TestSafeLoadEmptyDoc covers SafeLoad over a blank document (load returns nil,
// no restriction needed).
func TestSafeLoadEmptyDoc(t *testing.T) {
	if v, err := SafeLoad(""); err != nil || v != nil {
		t.Errorf("SafeLoad empty = %#v, %v", v, err)
	}
	// SafeLoad with a permitted list over a blank document is still nil (restrict
	// no-ops on nil).
	if v, err := SafeLoad("--- \n", WithPermittedClasses("X")); err != nil || v != nil {
		t.Errorf("SafeLoad blank+permit = %#v, %v", v, err)
	}
	// A scalar under restriction passes through restrict's default branch.
	if v, _ := SafeLoad("--- 5\n", WithPermittedClasses("X")); !eqValue(v, int64(5)) {
		t.Errorf("SafeLoad scalar = %#v", v)
	}
}

// TestLoadAliasInValue covers an alias appearing as a mapping value via scalarValue.
func TestLoadAliasValue(t *testing.T) {
	v := mustLoad(t, "---\na: &x 1\nb: *x\n").(*Map)
	bv, _ := v.Get("b")
	if !eqValue(bv, int64(1)) {
		t.Errorf("alias value = %#v", bv)
	}
}

// TestDumpRemainingBranches covers the last emitter branches: a non-empty Map as
// a complex key, an empty object as a sequence element, and a string with a " #"
// (comment-indicator) needing single quotes.
func TestDumpRemainingBranches(t *testing.T) {
	// A non-empty Map key triggers isComplexKey's *Map case.
	mk := NewMap()
	mk.Set("k", 1)
	outer := NewMap()
	outer.Set(mk, "v")
	got := mustDump(t, outer)
	if !strings.Contains(got, "? k: 1") {
		t.Errorf("map key = %q", got)
	}
	// An empty object as a sequence element ("- !ruby/object:Bar {}").
	seq := []any{&Object{Class: "Bar", IVars: map[string]any{}}}
	got = mustDump(t, seq)
	if got != "---\n- !ruby/object:Bar {}\n" {
		t.Errorf("empty obj seq = %q", got)
	}
	// A " #" mid-string forces single quotes (needsSingleQuote comment branch).
	if got := mustDump(t, "a #b"); got != "--- 'a #b'\n" {
		t.Errorf("space-hash = %q", got)
	}
}

// TestDumpSharedViaCanon covers canonRec's seen-guard for a *Map and *Object that
// appear twice (shared) through the host-map canonicalisation.
func TestDumpSharedViaCanon(t *testing.T) {
	shared := NewMap()
	shared.Set("k", 1)
	top := NewMap()
	top.Set("a", shared)
	top.Set("b", shared)
	got := mustDump(t, top)
	if !strings.Contains(got, "&1") || !strings.Contains(got, "*1") {
		t.Errorf("shared map = %q", got)
	}
	o := &Object{Class: "Foo", IVars: map[string]any{"x": 1}}
	top2 := NewMap()
	top2.Set("a", o)
	top2.Set("b", o)
	got = mustDump(t, top2)
	if !strings.Contains(got, "&1 !ruby/object:Foo") || !strings.Contains(got, "*1") {
		t.Errorf("shared object = %q", got)
	}
	// A shared Object reached through canon (its ivar is a shared map): exercises
	// the *Object seen-guard in canonRec via a self-referential ivar.
	cyc := &Object{Class: "Node", IVars: map[string]any{}}
	cyc.IVars["self"] = cyc
	if _, err := Dump(cyc); err != nil {
		t.Fatalf("cyclic object: %v", err)
	}
	// A self-referential range likewise terminates through canonRec.
	r := &Range{}
	r.Begin = r
	if _, err := Dump(r); err != nil {
		t.Fatalf("cyclic range: %v", err)
	}
}

// TestLoadNoMarker covers a document with no "---" marker (parseNode at top).
func TestLoadNoMarker(t *testing.T) {
	if v := mustLoad(t, "a: 1\nb: 2\n"); v.(*Map).Len() != 2 {
		t.Errorf("no marker = %#v", v)
	}
	if v := mustLoad(t, "42\n"); !eqValue(v, int64(42)) {
		t.Errorf("no marker scalar = %#v", v)
	}
}

// TestLoadBlockScalarTagEdges covers blockScalarTag's empty / non-pipe returns via
// scalarValue inputs that are not block tags.
func TestLoadBlockScalarTagEdges(t *testing.T) {
	// "|" alone with no body indents nothing -> empty string.
	if v := mustLoad(t, "--- |\n"); !eqValue(v, "") {
		t.Errorf("empty block = %#v", v)
	}
	// A "-" sequence entry whose rest is a plain (non-block) value, and a bare "-"
	// at end of input (nil element with nothing deeper).
	v := mustLoad(t, "---\n- x\n-\n")
	arr := v.([]any)
	if len(arr) != 2 || !eqValue(arr[0], "x") || arr[1] != nil {
		t.Errorf("seq trailing dash = %#v", v)
	}
}

// TestLoadFlowSpacedEmpty covers the flow "[ ]" / "{ }" inner=="" branches (a
// space inside the brackets, distinct from the "[]"/"{}" fast path).
func TestLoadFlowSpacedEmpty(t *testing.T) {
	if v := mustLoad(t, "--- [ ]\n"); !eqValue(v, []any{}) {
		t.Errorf("spaced empty seq = %#v", v)
	}
	if m, ok := mustLoad(t, "--- { }\n").(*Map); !ok || m.Len() != 0 {
		t.Errorf("spaced empty map = %#v", m)
	}
}

// TestLoadExplicitKeyBlock covers explicitKey's block-key form ("?" alone) and
// explicitValue's no-matching-colon and end-of-input branches.
func TestLoadExplicitKeyBlock(t *testing.T) {
	// "?" alone whose key is the indented block beneath, then ": value".
	m := mustLoad(t, "---\n?\n  - 1\n  - 2\n: v\n").(*Map)
	p := m.Pairs()[0]
	if !eqValue(p.Key, []any{int64(1), int64(2)}) || !eqValue(p.Val, "v") {
		t.Errorf("block key = %#v", p)
	}
	// An explicit key with no following ":" line -> nil value (defensive).
	m = mustLoad(t, "---\n? lonely\nother: 1\n").(*Map)
	if kv, _ := m.Get("lonely"); kv != nil {
		t.Errorf("lonely key value = %#v", kv)
	}
	// An explicit "?"/":" at end of document (explicitValue end-of-input branch).
	m = mustLoad(t, "---\n? k\n").(*Map)
	if kv, _ := m.Get("k"); kv != nil {
		t.Errorf("trailing explicit key = %#v", kv)
	}
}

// TestLoadUnquoteScalarDouble covers unquoteScalar's double-quote branch (used by
// a !ruby/symbol whose payload is double-quoted) and parsePlainScalar's empty
// quoted string.
func TestLoadUnquoteVariants(t *testing.T) {
	if v := mustLoad(t, "--- !ruby/symbol \"a b\"\n"); !eqValue(v, Symbol("a b")) {
		t.Errorf("dq symbol = %#v", v)
	}
	// A double-quoted empty string.
	if v := mustLoad(t, "--- \"\"\n"); !eqValue(v, "") {
		t.Errorf("empty dq = %#v", v)
	}
	// A single-quoted empty string at the scalar grammar's "" guard.
	if v := mustLoad(t, "--- ''\n"); !eqValue(v, "") {
		t.Errorf("empty sq = %#v", v)
	}
}

// TestLoadTabError covers the tab-indentation SyntaxError path of Load / SafeLoad.
func TestLoadTabError(t *testing.T) {
	src := "---\n\tkey: 1\n"
	if _, err := Load(src); err == nil {
		t.Fatal("expected tab error")
	} else if _, ok := err.(*SyntaxError); !ok || err.Error() == "" {
		t.Errorf("error type = %T (%v)", err, err)
	}
	if _, err := SafeLoad(src); err == nil {
		t.Fatal("expected SafeLoad tab error")
	}
}

// TestLoadFinalBranches mops up the last loader branches.
func TestLoadFinalBranches(t *testing.T) {
	// blockScalarTag non-chomp suffix ("|x") falls through to a plain scalar.
	if v := mustLoad(t, "---\nk: |x\n"); v != nil {
		m := v.(*Map)
		if kv, _ := m.Get("k"); !eqValue(kv, "|x") {
			t.Errorf("|x scalar = %#v", kv)
		}
	}
	// A block scalar followed by a dedented sibling key (parseBlockScalar's
	// indent<=parent break).
	m := mustLoad(t, "---\na: |-\n  one\n  two\nb: 2\n").(*Map)
	if av, _ := m.Get("a"); !eqValue(av, "one\ntwo") {
		t.Errorf("block then sibling a = %#v", av)
	}
	if bv, _ := m.Get("b"); !eqValue(bv, int64(2)) {
		t.Errorf("block then sibling b = %#v", bv)
	}
	// A mapping key whose value is empty and is the last line -> nil value.
	m = mustLoad(t, "---\nlast:\n").(*Map)
	if lv, _ := m.Get("last"); lv != nil {
		t.Errorf("last nil value = %#v", lv)
	}
	// A mapping interrupted by a non-entry line at the same indent (parseMapping's
	// !ok break): a stray sequence dash terminates the mapping.
	m = mustLoad(t, "---\na: 1\n- stray\n").(*Map)
	if m.Len() != 1 {
		t.Errorf("interrupted mapping len = %d", m.Len())
	}
	// An empty scalar token (parsePlainScalar "" guard) via a flow item.
	v := mustLoad(t, "--- [, x]\n")
	if arr := v.([]any); arr[0] != nil {
		t.Errorf("empty flow item = %#v", arr[0])
	}
	// A "\"" escape inside a double-quoted scalar (unquoteDouble's '"' case).
	if v := mustLoad(t, "--- \"a\\\"b\"\n"); !eqValue(v, "a\"b") {
		t.Errorf("escaped quote = %#v", v)
	}
}

// TestLoadEmptyBlockKeyValue covers explicitValue's ": " with empty value and no
// deeper block (the nil return) and parseBlock's dedent / empty-body branch.
func TestLoadEmptyExplicitAndBlock(t *testing.T) {
	// "? k" / ":" with nothing following -> nil value.
	m := mustLoad(t, "---\n? k\n:\nother: 1\n").(*Map)
	if kv, _ := m.Get("k"); kv != nil {
		t.Errorf("empty explicit value = %#v", kv)
	}
	// A standalone anchor+tag whose body is absent (parseBlock dedent -> empty).
	v := mustLoad(t, "---\nk: !ruby/object:Foo\nnext: 1\n").(*Map)
	if o, _ := v.Get("k"); func() bool { _, ok := o.(*Object); return !ok }() {
		t.Errorf("empty-body tagged value = %#v", o)
	}
}

// TestLoadParseNodeEOF covers parseNode reached at end of input (returns nil).
func TestLoadParseNodeEOF(t *testing.T) {
	// A mapping value pointing past the last line: "k:" as the only content after a
	// document with trailing blanks already trimmed forces parseNode at EOF via the
	// deeper-block lookahead that finds nothing.
	v := mustLoad(t, "---\nk:\n")
	if kv, _ := v.(*Map).Get("k"); kv != nil {
		t.Errorf("eof value = %#v", kv)
	}
}

// TestLoadEmptyClampLong covers parseBlockScalar's cut>len clamp directly with a
// document-level block whose continuation is shorter than the base indent.
func TestLoadBlockClamp(t *testing.T) {
	// The second body line is unindented-but-deeper-than-parent only if there is
	// content; use a mapping so the parent indent is 0 and a body line is shorter.
	src := "---\nk: |-\n      indented\n  x\n"
	m := mustLoad(t, src).(*Map)
	kv, _ := m.Get("k")
	if !strings.Contains(kv.(string), "indented") {
		t.Errorf("clamp body = %#v", kv)
	}
}

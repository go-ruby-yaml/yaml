// Copyright (c) the go-ruby-yaml/yaml authors
//
// SPDX-License-Identifier: BSD-3-Clause

package yaml

import (
	"math"
	"math/big"
	"reflect"
	"testing"
	"time"
)

// mustDump dumps v or fails the test.
func mustDump(t *testing.T, v Value) string {
	t.Helper()
	s, err := Dump(v)
	if err != nil {
		t.Fatalf("Dump(%#v): %v", v, err)
	}
	return s
}

// mustLoad loads s or fails the test.
func mustLoad(t *testing.T, s string) Value {
	t.Helper()
	v, err := Load(s)
	if err != nil {
		t.Fatalf("Load(%q): %v", s, err)
	}
	return v
}

// TestDumpScalars checks every scalar's emitted document form.
func TestDumpScalars(t *testing.T) {
	big1, _ := new(big.Int).SetString("123456789012345678901234567890", 10)
	cases := []struct {
		v    Value
		want string
	}{
		{nil, "--- \n"},
		{true, "--- true\n"},
		{false, "--- false\n"},
		{0, "--- 0\n"},
		{42, "--- 42\n"},
		{int64(-7), "--- -7\n"},
		{big1, "--- 123456789012345678901234567890\n"},
		{3.14, "--- 3.14\n"},
		{float32(1.5), "--- 1.5\n"},
		{2.0, "--- 2.0\n"},
		{1e20, "--- 1e+20\n"},
		{math.Inf(1), "--- .inf\n"},
		{math.Inf(-1), "--- -.inf\n"},
		{math.NaN(), "--- .nan\n"},
		{"hello", "--- hello\n"},
		{"", "--- ''\n"},
		{"true", "--- 'true'\n"},
		{"123", "--- '123'\n"},
		{"a: b", "--- 'a: b'\n"},
		{"# x", "--- \"# x\"\n"},
		{"-x", "--- \"-x\"\n"},
		{"line1\nline2", "--- |-\n  line1\n  line2\n"},
		{"line1\nline2\n", "--- |\n  line1\n  line2\n"},
		{"ctl\x01here", "--- \"ctl\\x01here\"\n"},
		{"tab\there", "--- \"tab\\there\"\n"},
		{"a#b", "--- a#b\n"},
		{"a b: c", "--- 'a b: c'\n"},
		{Symbol("foo"), "--- :foo\n"},
		{Symbol("a\nb"), "--- :\"a\\nb\"\n"},
		{Class("String"), "--- !ruby/class 'String'\n"},
		{Module("Comparable"), "--- !ruby/module 'Comparable'\n"},
		{&Regexp{Source: "ab", Flags: "i"}, "--- !ruby/regexp /ab/i\n"},
		{time.Date(2026, 6, 29, 5, 18, 32, 0, time.UTC), "--- 2026-06-29 05:18:32.000000000 Z\n"},
		{time.Date(2026, 6, 29, 5, 18, 32, 0, time.FixedZone("", -7*3600)), "--- 2026-06-29 05:18:32.000000000 -07:00\n"},
	}
	for _, c := range cases {
		if got := mustDump(t, c.v); got != c.want {
			t.Errorf("Dump(%#v) = %q, want %q", c.v, got, c.want)
		}
	}
}

// TestDumpCollections checks sequence / mapping emission shapes.
func TestDumpCollections(t *testing.T) {
	cases := []struct {
		v    Value
		want string
	}{
		{[]any{}, "--- []\n"},
		{NewMap(), "--- {}\n"},
		{[]any{1, 2, 3}, "---\n- 1\n- 2\n- 3\n"},
		{[]any{[]any{1}, []any{2}}, "---\n- - 1\n- - 2\n"},
		{[]any{[]any{}}, "---\n- []\n"},
		{[]any{NewMap()}, "---\n- {}\n"},
	}
	for _, c := range cases {
		if got := mustDump(t, c.v); got != c.want {
			t.Errorf("Dump(%#v) = %q, want %q", c.v, got, c.want)
		}
	}

	m := NewMap()
	m.Set("list", []any{1, 2, 3})
	inner := NewMap()
	inner.Set("x", 1)
	m.Set("map", inner)
	m.Set("nil", nil)
	got := mustDump(t, m)
	want := "---\nlist:\n- 1\n- 2\n- 3\nmap:\n  x: 1\nnil:\n"
	if got != want {
		t.Errorf("nested map = %q, want %q", got, want)
	}
}

// TestDumpMapVariants checks the host-friendly Go map inputs and []Pair via Set.
func TestDumpMapVariants(t *testing.T) {
	got := mustDump(t, map[string]any{"b": 2, "a": 1})
	if got != "---\na: 1\nb: 2\n" {
		t.Errorf("string map = %q", got)
	}
	got = mustDump(t, map[Symbol]any{"b": 2, "a": 1})
	if got != "---\n:a: 1\n:b: 2\n" {
		t.Errorf("symbol map = %q", got)
	}
	// []Value alias of []any is accepted by canon.
	got = mustDump(t, []Value{1, "x"})
	if got != "---\n- 1\n- x\n" {
		t.Errorf("[]Value = %q", got)
	}
}

// TestDumpComplex checks Object / Range emission, including empty and nested.
func TestDumpComplex(t *testing.T) {
	o := &Object{Class: "Foo", IVars: map[string]any{"name": "bob", "age": 7}}
	if got := mustDump(t, o); got != "--- !ruby/object:Foo\nage: 7\nname: bob\n" {
		t.Errorf("object = %q", got)
	}
	// Explicit Order overrides lexicographic.
	o.Order = []string{"name", "age"}
	if got := mustDump(t, o); got != "--- !ruby/object:Foo\nname: bob\nage: 7\n" {
		t.Errorf("ordered object = %q", got)
	}
	// Bare Object class.
	bare := &Object{Class: "Object", IVars: map[string]any{}}
	if got := mustDump(t, bare); got != "--- !ruby/object {}\n" {
		t.Errorf("bare empty object = %q", got)
	}
	empty := &Object{Class: "", IVars: map[string]any{}}
	if got := mustDump(t, empty); got != "--- !ruby/object {}\n" {
		t.Errorf("empty-class object = %q", got)
	}
	r := &Range{Begin: 1, End: 5, Exclusive: true}
	if got := mustDump(t, r); got != "--- !ruby/range\nbegin: 1\nend: 5\nexcl: true\n" {
		t.Errorf("range = %q", got)
	}
	// Beginless / endless range emits nil bounds.
	rOpen := &Range{Begin: nil, End: 5}
	if got := mustDump(t, rOpen); got != "--- !ruby/range\nbegin:\nend: 5\nexcl: false\n" {
		t.Errorf("open range = %q", got)
	}
}

// TestDumpComplexAsMapValue checks an object / range used as a mapping value and a
// sequence element (the indented-body paths).
func TestDumpComplexAsMapValue(t *testing.T) {
	m := NewMap()
	m.Set("o", &Object{Class: "Foo", IVars: map[string]any{"x": 1}})
	m.Set("r", &Range{Begin: 1, End: 2})
	m.Set("empty", &Object{Class: "Bar", IVars: map[string]any{}})
	got := mustDump(t, m)
	want := "---\no: !ruby/object:Foo\n  x: 1\nr: !ruby/range\n  begin: 1\n  end: 2\n  excl: false\nempty: !ruby/object:Bar {}\n"
	if got != want {
		t.Errorf("map of complex = %q\nwant %q", got, want)
	}
	seq := []any{&Object{Class: "Foo", IVars: map[string]any{"x": 1}}, &Range{Begin: 1, End: 2}}
	got = mustDump(t, seq)
	want = "---\n- !ruby/object:Foo\n  x: 1\n- !ruby/range\n  begin: 1\n  end: 2\n  excl: false\n"
	if got != want {
		t.Errorf("seq of complex = %q\nwant %q", got, want)
	}
}

// TestDumpComplexKeys checks the explicit "? key" / ": value" form.
func TestDumpComplexKeys(t *testing.T) {
	m := NewMap()
	m.Set([]any{1, 2}, "seqkey")
	got := mustDump(t, m)
	want := "---\n? - 1\n  - 2\n: seqkey\n"
	if got != want {
		t.Errorf("seq key = %q\nwant %q", got, want)
	}
	// nil key uses the "! ''" form.
	m2 := NewMap()
	m2.Set(nil, "v")
	if got := mustDump(t, m2); got != "---\n! '': v\n" {
		t.Errorf("nil key = %q", got)
	}
	// Object as a key.
	m3 := NewMap()
	m3.Set(&Range{Begin: 1, End: 2}, "rk")
	got = mustDump(t, m3)
	want = "---\n? !ruby/range\n  begin: 1\n  end: 2\n  excl: false\n: rk\n"
	if got != want {
		t.Errorf("range key = %q\nwant %q", got, want)
	}
}

// TestDumpAnchors checks shared and cyclic reference emission.
func TestDumpAnchors(t *testing.T) {
	a := []any{1, 2}
	if got := mustDump(t, []any{a, a}); got != "---\n- &1\n  - 1\n  - 2\n- *1\n" {
		t.Errorf("shared seq = %q", got)
	}
	// Shared as a mapping value.
	m := NewMap()
	m.Set("x", a)
	m.Set("y", a)
	got := mustDump(t, m)
	// "y" is a YAML reserved word, so the emitter quotes the key (MRI does too);
	// the single-quoted form round-trips to the string "y".
	want := "---\nx: &1\n  - 1\n  - 2\n'y': *1\n"
	if got != want {
		t.Errorf("shared map value = %q\nwant %q", got, want)
	}
	// Shared object.
	o := &Object{Class: "Foo", IVars: map[string]any{"x": 1}}
	got = mustDump(t, []any{o, o})
	want = "---\n- &1 !ruby/object:Foo\n  x: 1\n- *1\n"
	if got != want {
		t.Errorf("shared object = %q\nwant %q", got, want)
	}
	// Cyclic structure terminates.
	cyc := []any{nil}
	cyc[0] = cyc
	if _, err := Dump(cyc); err != nil {
		t.Fatalf("cyclic dump: %v", err)
	}
}

// TestDumpEmptySliceIdentity checks two distinct empty slices are not aliased.
func TestDumpEmptySliceIdentity(t *testing.T) {
	e1 := []any{}
	e2 := []any{}
	got := mustDump(t, []any{e1, e2})
	if got != "---\n- []\n- []\n" {
		t.Errorf("two empties = %q", got)
	}
	// Two distinct empty maps likewise are not aliased.
	got = mustDump(t, []any{NewMap(), NewMap()})
	if got != "---\n- {}\n- {}\n" {
		t.Errorf("two empty maps = %q", got)
	}
}

// TestDumpUnsupported checks an unrepresentable value returns an error.
func TestDumpUnsupported(t *testing.T) {
	if _, err := Dump(make(chan int)); err == nil {
		t.Fatal("expected error dumping a channel")
	}
	// Unsupported nested in a collection unwinds via the catch path.
	if _, err := Dump([]any{make(chan int)}); err == nil {
		t.Fatal("expected error dumping nested channel")
	}
}

// TestCatchRepanics checks catch re-panics a non-dumpErr value.
func TestCatchRepanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected re-panic")
		}
	}()
	_ = catch(func() { panic("boom") })
}

// TestLoadScalars checks scalar parsing.
func TestLoadScalars(t *testing.T) {
	big1, _ := new(big.Int).SetString("123456789012345678901234567890", 10)
	cases := []struct {
		s    string
		want Value
	}{
		{"", nil},
		{"--- \n", nil},
		{"---\n", nil},
		{"--- ~\n", nil},
		{"--- null\n", nil},
		{"--- true\n", true},
		{"--- false\n", false},
		{"--- 42\n", int64(42)},
		{"--- -7\n", int64(-7)},
		{"--- 0x1F\n", int64(31)},
		{"--- 0o17\n", int64(15)},
		{"--- 0b101\n", int64(5)},
		{"--- 1_000\n", int64(1000)},
		{"--- 123456789012345678901234567890\n", big1},
		{"--- 3.14\n", 3.14},
		{"--- 2.0\n", 2.0},
		{"--- .inf\n", math.Inf(1)},
		{"--- -.inf\n", math.Inf(-1)},
		{"--- hello\n", "hello"},
		{"--- 'quoted'\n", "quoted"},
		{"--- \"a\\nb\"\n", "a\nb"},
		{"--- :foo\n", Symbol("foo")},
	}
	for _, c := range cases {
		got := mustLoad(t, c.s)
		if !eqValue(got, c.want) {
			t.Errorf("Load(%q) = %#v, want %#v", c.s, got, c.want)
		}
	}
	// NaN parses to a NaN float.
	if v := mustLoad(t, "--- .nan\n"); !math.IsNaN(v.(float64)) {
		t.Errorf(".nan = %#v", v)
	}
}

// TestLoadTime checks timestamp parsing across the accepted layouts.
func TestLoadTime(t *testing.T) {
	for _, s := range []string{
		"--- 2026-06-29 05:18:32.000000000 Z\n",
		"--- 2026-06-29 05:18:32 Z\n",
		"--- 2026-06-29 05:18:32.000000000 -07:00\n",
		"--- 2026-06-29 05:18:32 -07:00\n",
		"--- 2026-06-29T05:18:32Z\n",
	} {
		v := mustLoad(t, s)
		if _, ok := v.(time.Time); !ok {
			t.Errorf("Load(%q) = %#v, not a Time", s, v)
		}
	}
}

// TestLoadCollections checks block and flow collections.
func TestLoadCollections(t *testing.T) {
	v := mustLoad(t, "---\n- 1\n- 2\n- 3\n")
	if !eqValue(v, []any{int64(1), int64(2), int64(3)}) {
		t.Errorf("block seq = %#v", v)
	}
	v = mustLoad(t, "--- [1, 2, 3]\n")
	if !eqValue(v, []any{int64(1), int64(2), int64(3)}) {
		t.Errorf("flow seq = %#v", v)
	}
	v = mustLoad(t, "--- []\n")
	if !eqValue(v, []any{}) {
		t.Errorf("empty flow seq = %#v", v)
	}
	m := mustLoad(t, "---\na: 1\nb: 2\n").(*Map)
	if av, _ := m.Get("a"); !eqValue(av, int64(1)) {
		t.Errorf("map a = %#v", av)
	}
	if m.Len() != 2 {
		t.Errorf("map len = %d", m.Len())
	}
	m = mustLoad(t, "--- {a: 1, b: 2}\n").(*Map)
	if m.Len() != 2 {
		t.Errorf("flow map len = %d", m.Len())
	}
	m = mustLoad(t, "--- {}\n").(*Map)
	if m.Len() != 0 {
		t.Errorf("empty flow map len = %d", m.Len())
	}
}

// TestLoadNested checks deep block nesting and sequence-of-mappings.
func TestLoadNested(t *testing.T) {
	src := "---\nlist:\n- 1\n- 2\nmap:\n  x: 1\n  y: 2\n"
	m := mustLoad(t, src).(*Map)
	lst, _ := m.Get("list")
	if !eqValue(lst, []any{int64(1), int64(2)}) {
		t.Errorf("list = %#v", lst)
	}
	inner, _ := m.Get("map")
	im := inner.(*Map)
	if xv, _ := im.Get("x"); !eqValue(xv, int64(1)) {
		t.Errorf("inner x = %#v", xv)
	}
	// Sequence of mappings (dash-line layout).
	v := mustLoad(t, "---\n- a: 1\n  b: 2\n- c: 3\n")
	arr := v.([]any)
	if len(arr) != 2 {
		t.Fatalf("seq of maps len = %d", len(arr))
	}
	first := arr[0].(*Map)
	if bv, _ := first.Get("b"); !eqValue(bv, int64(2)) {
		t.Errorf("first.b = %#v", bv)
	}
	// "- -" nested sequence.
	v = mustLoad(t, "---\n- - 1\n- - 2\n")
	if !eqValue(v, []any{[]any{int64(1)}, []any{int64(2)}}) {
		t.Errorf("nested seq = %#v", v)
	}
	// A bare "-" entry whose value is the indented block, and a "-" with nothing.
	v = mustLoad(t, "---\n-\n  a: 1\n-\n")
	arr = v.([]any)
	if len(arr) != 2 || arr[1] != nil {
		t.Errorf("bare dash = %#v", v)
	}
}

// TestLoadBlockScalars checks literal and folded block scalars with each chomp.
func TestLoadBlockScalars(t *testing.T) {
	cases := []struct {
		s    string
		want string
	}{
		{"--- |-\n  a\n  b\n", "a\nb"},
		{"--- |\n  a\n  b\n", "a\nb\n"},
		{"--- |+\n  a\n", "a\n"},
		{"--- >-\n  a\n  b\n", "a b"},
		{"--- >\n  a\n  b\n", "a b\n"},
	}
	for _, c := range cases {
		if v := mustLoad(t, c.s); !eqValue(v, c.want) {
			t.Errorf("Load(%q) = %#v, want %q", c.s, v, c.want)
		}
	}
	// Block scalar as a mapping value and a sequence element.
	m := mustLoad(t, "---\ntext: |-\n  line1\n  line2\n").(*Map)
	if tv, _ := m.Get("text"); !eqValue(tv, "line1\nline2") {
		t.Errorf("map block = %#v", tv)
	}
	v := mustLoad(t, "---\n- |-\n  x\n  y\n")
	if !eqValue(v, []any{"x\ny"}) {
		t.Errorf("seq block = %#v", v)
	}
}

// TestLoadTags checks the !ruby/* tags load into the model types.
func TestLoadTags(t *testing.T) {
	// Object.
	o := mustLoad(t, "--- !ruby/object:Foo\nname: bob\nage: 7\n").(*Object)
	if o.Class != "Foo" || o.IVars["name"] != "bob" {
		t.Errorf("object = %#v", o)
	}
	if !reflect.DeepEqual(o.Order, []string{"name", "age"}) {
		t.Errorf("object order = %#v", o.Order)
	}
	// Bare object and empty inline.
	o = mustLoad(t, "--- !ruby/object\n").(*Object)
	if o.Class != "Object" {
		t.Errorf("bare object class = %q", o.Class)
	}
	o = mustLoad(t, "--- !ruby/object:Bar {}\n").(*Object)
	if o.Class != "Bar" || len(o.IVars) != 0 {
		t.Errorf("inline empty object = %#v", o)
	}
	// Range.
	r := mustLoad(t, "--- !ruby/range\nbegin: 1\nend: 5\nexcl: true\n").(*Range)
	if !eqValue(r.Begin, int64(1)) || !eqValue(r.End, int64(5)) || !r.Exclusive {
		t.Errorf("range = %#v", r)
	}
	r = mustLoad(t, "--- !ruby/range\n").(*Range)
	if r.Begin != nil {
		t.Errorf("empty range begin = %#v", r.Begin)
	}
	// Symbol via tag, class, module, regexp.
	if v := mustLoad(t, "--- !ruby/symbol foo\n"); !eqValue(v, Symbol("foo")) {
		t.Errorf("symbol tag = %#v", v)
	}
	if v := mustLoad(t, "--- !ruby/class 'String'\n"); !eqValue(v, Class("String")) {
		t.Errorf("class tag = %#v", v)
	}
	if v := mustLoad(t, "--- !ruby/module 'Comparable'\n"); !eqValue(v, Module("Comparable")) {
		t.Errorf("module tag = %#v", v)
	}
	if v := mustLoad(t, "--- !ruby/string 5\n"); !eqValue(v, "5") {
		t.Errorf("string tag = %#v", v)
	}
	reg := mustLoad(t, "--- !ruby/regexp /ab/i\n").(*Regexp)
	if reg.Source != "ab" || reg.Flags != "i" {
		t.Errorf("regexp = %#v", reg)
	}
	reg = mustLoad(t, "--- !ruby/regexp plain\n").(*Regexp)
	if reg.Source != "plain" {
		t.Errorf("regexp plain = %#v", reg)
	}
}

// TestLoadAnchors checks anchor / alias resolution including a missing alias.
func TestLoadAnchors(t *testing.T) {
	v := mustLoad(t, "---\n- &1\n  - 1\n  - 2\n- *1\n")
	arr := v.([]any)
	if !eqValue(arr[0], arr[1]) {
		t.Errorf("alias mismatch: %#v", v)
	}
	// Missing alias resolves to nil.
	v = mustLoad(t, "--- *missing\n")
	if v != nil {
		t.Errorf("missing alias = %#v", v)
	}
}

// TestLoadExplicitKeys checks the "? key" / ": value" complex-key form.
func TestLoadExplicitKeys(t *testing.T) {
	m := mustLoad(t, "---\n? - 1\n  - 2\n: seqkey\n").(*Map)
	if m.Len() != 1 {
		t.Fatalf("explicit-key map len = %d", m.Len())
	}
	p := m.Pairs()[0]
	if !eqValue(p.Key, []any{int64(1), int64(2)}) || !eqValue(p.Val, "seqkey") {
		t.Errorf("explicit key/val = %#v", p)
	}
	// Inline "? k" / ": v".
	m = mustLoad(t, "---\n? key\n: val\n").(*Map)
	p = m.Pairs()[0]
	if !eqValue(p.Key, "key") || !eqValue(p.Val, "val") {
		t.Errorf("inline explicit = %#v", p)
	}
	// "? key" whose value block follows the ":".
	m = mustLoad(t, "---\n? key\n:\n  a: 1\n").(*Map)
	p = m.Pairs()[0]
	inner := p.Val.(*Map)
	if av, _ := inner.Get("a"); !eqValue(av, int64(1)) {
		t.Errorf("explicit block value = %#v", p.Val)
	}
}

// TestLoadComments checks comment and document-end handling.
func TestLoadComments(t *testing.T) {
	v := mustLoad(t, "# coding: UTF-8\n---\na: 1\n# trailing comment\nb: 2\n...\nc: 3\n")
	m := v.(*Map)
	if m.Len() != 2 {
		t.Errorf("comment/doc-end map len = %d", m.Len())
	}
}

// TestRoundTrip checks Load(Dump(x)) reproduces representative structures.
func TestRoundTrip(t *testing.T) {
	m := NewMap()
	m.Set(Symbol("checked"), true)
	m.Set("count", int64(3))
	m.Set("list", []any{int64(1), "two", nil})
	inner := NewMap()
	inner.Set("nested", "deep\nvalue")
	m.Set("inner", inner)
	s := mustDump(t, m)
	got := mustLoad(t, s)
	if !eqValue(got, m) {
		t.Errorf("round-trip mismatch:\nsrc %q\ngot %#v\nwant %#v", s, got, m)
	}
}

// eqValue compares two model values structurally (ordered maps by entry order,
// NaN never equal — callers test NaN directly).
func eqValue(a, b Value) bool {
	switch av := a.(type) {
	case nil:
		return b == nil
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !eqValue(av[i], bv[i]) {
				return false
			}
		}
		return true
	case *Map:
		bv, ok := b.(*Map)
		if !ok || av.Len() != bv.Len() {
			return false
		}
		for i, p := range av.pairs {
			q := bv.pairs[i]
			if !eqValue(p.Key, q.Key) || !eqValue(p.Val, q.Val) {
				return false
			}
		}
		return true
	case *big.Int:
		bv, ok := b.(*big.Int)
		return ok && av.Cmp(bv) == 0
	case *Object:
		bv, ok := b.(*Object)
		if !ok || av.Class != bv.Class || len(av.IVars) != len(bv.IVars) {
			return false
		}
		for k, val := range av.IVars {
			if !eqValue(val, bv.IVars[k]) {
				return false
			}
		}
		return true
	case *Range:
		bv, ok := b.(*Range)
		return ok && eqValue(av.Begin, bv.Begin) && eqValue(av.End, bv.End) && av.Exclusive == bv.Exclusive
	case *Regexp:
		bv, ok := b.(*Regexp)
		return ok && *av == *bv
	case time.Time:
		bv, ok := b.(time.Time)
		return ok && av.Equal(bv)
	}
	return reflect.DeepEqual(a, b)
}

// TestMapGetSet exercises the ordered-map identity index and complex-key append.
func TestMapGetSet(t *testing.T) {
	m := NewMap()
	m.Set("a", 1)
	m.Set("a", 2) // replace
	if v, _ := m.Get("a"); !eqValue(v, 2) {
		t.Errorf("replace failed: %#v", v)
	}
	if m.Len() != 1 {
		t.Errorf("len after replace = %d", m.Len())
	}
	if _, ok := m.Get("missing"); ok {
		t.Error("missing key reported present")
	}
	// Non-comparable key is always appended (no dedup, no Get hit).
	k := []any{1}
	m.Set(k, "x")
	m.Set(k, "y")
	if m.Len() != 3 {
		t.Errorf("len with complex keys = %d", m.Len())
	}
	if _, ok := m.Get(k); ok {
		t.Error("complex key unexpectedly retrievable")
	}
	// A zero-value Map (no NewMap) still accepts Set.
	var z Map
	z.Set("k", 1)
	if z.Len() != 1 {
		t.Errorf("zero map set: %d", z.Len())
	}
}

// TestAsBigInt covers the integer view helper for each integer spelling.
func TestAsBigInt(t *testing.T) {
	if asBigInt(7).Int64() != 7 {
		t.Error("int")
	}
	if asBigInt(int64(8)).Int64() != 8 {
		t.Error("int64")
	}
	if asBigInt(big.NewInt(9)).Int64() != 9 {
		t.Error("*big.Int")
	}
	if asBigInt("nope") != nil {
		t.Error("non-int should be nil")
	}
}

// TestSafeLoad checks the permitted-classes restriction and option plumbing.
func TestSafeLoad(t *testing.T) {
	src := "---\nobj: !ruby/object:Secret\n  x: 1\nok: !ruby/object:Allowed\n  y: 2\n"
	v, err := SafeLoad(src, WithPermittedClasses("Allowed"), WithAliases(true))
	if err != nil {
		t.Fatalf("SafeLoad: %v", err)
	}
	m := v.(*Map)
	secret, _ := m.Get("obj")
	if _, ok := secret.(*Map); !ok {
		t.Errorf("unpermitted Secret should be a Map, got %T", secret)
	}
	sm := secret.(*Map)
	if xv, _ := sm.Get(Symbol("x")); !eqValue(xv, int64(1)) {
		t.Errorf("restricted ivar = %#v", xv)
	}
	allowed, _ := m.Get("ok")
	if _, ok := allowed.(*Object); !ok {
		t.Errorf("permitted Allowed should stay an Object, got %T", allowed)
	}
	// Without permitted classes, all objects stay objects.
	v2, _ := SafeLoad(src)
	m2 := v2.(*Map)
	if o, _ := m2.Get("obj"); func() bool { _, ok := o.(*Object); return !ok }() {
		t.Error("no allow-list should keep objects")
	}
	// SafeLoad restriction recurses into sequences and ranges.
	src2 := "---\n- !ruby/object:Secret\n  z: 1\n"
	v3, _ := SafeLoad(src2, WithPermittedClasses("None"))
	if _, ok := v3.([]any)[0].(*Map); !ok {
		t.Error("seq element should be restricted to Map")
	}
}

// TestSafeLoadError checks SafeLoad propagates a load error (there is none for
// this loader, so we exercise the empty-permitted no-op path and a nested range).
func TestSafeLoadRangeRestrict(t *testing.T) {
	src := "--- !ruby/range\nbegin: !ruby/object:Secret\n  a: 1\nend: 2\nexcl: false\n"
	v, _ := SafeLoad(src, WithPermittedClasses("X"))
	r := v.(*Range)
	if _, ok := r.Begin.(*Map); !ok {
		t.Errorf("range begin should be restricted, got %T", r.Begin)
	}
}

// TestCanonForms checks canon converts the host map shapes nested in collections.
func TestCanonForms(t *testing.T) {
	// A map nested inside an object's ivar and a range bound is canonicalised.
	o := &Object{Class: "Foo", IVars: map[string]any{"m": map[string]any{"k": 1}}}
	got := mustDump(t, o)
	if got != "--- !ruby/object:Foo\nm:\n  k: 1\n" {
		t.Errorf("object ivar map = %q", got)
	}
	r := &Range{Begin: map[string]any{"k": 1}, End: 2}
	if _, err := Dump(r); err != nil {
		t.Fatalf("range with map bound: %v", err)
	}
	// A *Map at top level passes through canon (keys/vals recursed).
	m := NewMap()
	m.Set([]Value{1}, []Value{2})
	if _, err := Dump(m); err != nil {
		t.Fatalf("map with slice key/val: %v", err)
	}
}

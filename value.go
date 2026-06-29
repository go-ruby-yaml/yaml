// Copyright (c) the go-ruby-yaml/yaml authors
//
// SPDX-License-Identifier: BSD-3-Clause

// Package yaml is a pure-Go (CGO-free) Psych-compatible YAML emitter and loader
// for the Ruby value model. It is the deterministic, interpreter-independent
// core of MRI 4.0.5's Psych: Dump renders a tree of Ruby values to a
// Psych-compatible document and Load parses such a document back, so
// Load(Dump(x)) round-trips the structures Ruby programs (and Puppet's
// state/run-summary files) persist — without any Ruby runtime.
//
// # Ruby value model
//
// A Ruby value is represented by an [any] drawn from a small, fixed set of Go
// types so a host (such as go-embedded-ruby) can map its own object graph to and
// from this package:
//
//	Ruby            Go (in)                          Go (out, from Load)
//	----            -------                          -------------------
//	nil             nil                              nil
//	true / false    bool                             bool
//	Integer         int, int64, *big.Int             int64 or *big.Int
//	Float           float64, float32                 float64
//	String          string                           string
//	Symbol          Symbol                           Symbol
//	Array           []any, []Value                   []any
//	Hash            *Map (ordered), map[...]any      *Map (insertion order)
//	Time            time.Time                        time.Time
//	Range           *Range                           *Range
//	Object          *Object (tag + ivars)            *Object
//	Class / Module  Class / Module                   Class / Module
//	Regexp          *Regexp                          *Regexp
//
// Dump also accepts a plain Go map[string]any / map[Symbol]any (sorted by key
// for determinism); Load always yields an ordered [*Map] for a mapping so key
// order is preserved on round-trip.
package yaml

import (
	"math/big"
	"sort"
)

// Value is the interface satisfied by every Ruby value this package handles. It
// is purely documentary — the public API uses any — but a host may use it to
// constrain its own adapters.
type Value = any

// Symbol is a Ruby Symbol (`:name`). Dump emits the bare `:name` form (or the
// double-quoted `:"a\nb"` form when the name carries control characters), and
// Load maps both `:name` scalars and the `!ruby/symbol` tag back to a Symbol.
type Symbol string

// Class is a Ruby Class reference, emitted/loaded as the `!ruby/class 'Name'`
// tag. Its value is the fully-qualified class name (e.g. "String").
type Class string

// Module is a Ruby Module reference, emitted/loaded as `!ruby/module 'Name'`.
type Module string

// Regexp is a Ruby Regexp, emitted/loaded inline as `!ruby/regexp /source/flags`
// (Psych's representation). Flags is the trailing option letters (e.g. "mix").
type Regexp struct {
	Source string
	Flags  string
}

// Range is a Ruby Range, emitted/loaded as the `!ruby/range` mapping with
// begin / end / excl members. A nil Begin or End models a beginless / endless
// range.
type Range struct {
	Begin     Value
	End       Value
	Exclusive bool
}

// Object is a generic tagged Ruby object — anything Psych writes as a
// `!ruby/object:ClassName` mapping of instance variables. Class is the tag's
// class name ("" or "Object" emits the bare `!ruby/object`). IVars holds the
// instance variables by bare name (no leading "@"); Dump orders them by Order
// when set, else lexicographically (documented, deterministic). A host binds its
// own object instances to and from this shape.
type Object struct {
	Class string
	IVars map[string]Value
	// Order, when non-nil, fixes the emission order of IVars keys; names absent
	// from Order are appended in lexicographic order. When nil, all keys are
	// emitted in lexicographic order.
	Order []string
}

// orderedIVarKeys returns o's instance-variable names in emission order: the
// names listed in o.Order first (those that exist), then any remaining names
// lexicographically.
func (o *Object) orderedIVarKeys() []string {
	seen := map[string]bool{}
	var keys []string
	for _, k := range o.Order {
		if _, ok := o.IVars[k]; ok && !seen[k] {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	rest := make([]string, 0, len(o.IVars))
	for k := range o.IVars {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(keys, rest...)
}

// Pair is one entry of an ordered mapping.
type Pair struct {
	Key Value
	Val Value
}

// Map is an insertion-ordered Ruby Hash. Load returns mappings as *Map so key
// order round-trips; Dump accepts *Map, a plain Go map (emitted in sorted key
// order), or a []Pair via NewMap.
type Map struct {
	pairs []Pair
	index map[any]int // identity index for scalar (comparable) keys
}

// NewMap returns an empty ordered Map.
func NewMap() *Map { return &Map{index: map[any]int{}} }

// Len reports the number of entries.
func (m *Map) Len() int { return len(m.pairs) }

// Pairs returns the entries in insertion order. The slice must not be mutated.
func (m *Map) Pairs() []Pair { return m.pairs }

// Set inserts or replaces the entry for key. A comparable key replaces an
// existing equal key; a non-comparable key (slice / map / *Object …) is always
// appended (Psych's complex-key mappings do not deduplicate here).
func (m *Map) Set(key, val Value) {
	if m.index == nil {
		m.index = map[any]int{}
	}
	if comparableKey(key) {
		if i, ok := m.index[key]; ok {
			m.pairs[i].Val = val
			return
		}
		m.index[key] = len(m.pairs)
	}
	m.pairs = append(m.pairs, Pair{Key: key, Val: val})
}

// Get returns the value for a comparable key and whether it was present.
func (m *Map) Get(key Value) (Value, bool) {
	if comparableKey(key) {
		if i, ok := m.index[key]; ok {
			return m.pairs[i].Val, true
		}
	}
	return nil, false
}

// comparableKey reports whether key may be used as a Go map index (so Set can
// deduplicate it). Slices, maps and the pointer-backed *Object are the
// non-comparable shapes that occur as Psych complex keys.
func comparableKey(key Value) bool {
	switch key.(type) {
	case []any, *Object, *Range, *Map:
		return false
	}
	return true
}

// asBigInt is the canonical big-integer view of an integer Value, used by the
// emitter and by tests; it is never called with a non-integer.
func asBigInt(v Value) *big.Int {
	switch n := v.(type) {
	case int:
		return big.NewInt(int64(n))
	case int64:
		return big.NewInt(n)
	case *big.Int:
		return n
	}
	return nil
}

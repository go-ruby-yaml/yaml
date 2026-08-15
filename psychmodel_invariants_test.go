// Copyright (c) the go-ruby-yaml/yaml authors
//
// SPDX-License-Identifier: BSD-3-Clause

package yaml

import "testing"

// TestPsychModelInvariants pins the behaviours that make this package a
// Psych-compatible (MRI 4.0.5, YAML 1.1 core-schema) emitter+loader rather than a
// wrapper over the reference gopkg.in/yaml.v3 parser/emitter. Each was shown, by a
// control run against gopkg.in/yaml.v3 v3.0.1 over the emitter corpus and the full
// yaml/yaml-test-suite (402 tests), to have NO faithful yaml.v3 equivalent:
//
//  1. Float/Integer emit distinction (the load-bearing byte-exact invariant, on
//     which rbgo's `require "yaml"` state/run-summary persistence depends). A Ruby
//     Float 2.0 emits as "2.0" and an Integer 2 as "2". yaml.v3's emitter collapses
//     BOTH to "2" (yaml.Marshal(2.0) == yaml.Marshal(2) == "2\n"), which would
//     silently turn a persisted Float into an Integer on the next round-trip.
//  2. Ruby Symbols. A plain `:name` scalar loads as a Symbol and a Symbol dumps as
//     `:name`. yaml.v3 has no Symbol type: it decodes `:name` to the String ":name".
//  3. The Ruby object graph. A `!ruby/object:Class` mapping loads as an *Object of
//     instance variables (and `!ruby/range`, `!ruby/class`, `!ruby/regexp`, …).
//     yaml.v3 knows only the core-schema/Go-struct model and cannot reconstruct it.
//  4. Psych (YAML 1.1) ACCEPT leniency yaml.v3 lacks. Documents Psych accepts that
//     yaml.v3 rejects — a `%YAML 1.2` directive (suite 27NA: yaml.v3 raises "found
//     incompatible YAML document") and a nil-keyed explicit mapping (suite 2JQS:
//     yaml.v3 raises "did not find expected key"). A yaml.v3-backed loader would
//     wrongly reject these (50 such accept-axis divergences measured).
//  5. Block-structure REJECT strictness yaml.v3 lacks. Malformed documents this
//     loader rejects that yaml.v3 silently accepts — a stray flow close (suite
//     4H7K, "[ a, b, c ] ]") and junk after a flow collection (suite KS4U). A
//     yaml.v3-backed loader would wrongly accept these (11 such reject-axis
//     divergences measured).
//
// Net over yaml/yaml-test-suite: swapping the hand-rolled parser for yaml.v3 v3.0.1
// changed 115 accept/reject verdicts (61 regressions vs the required Psych verdict,
// 54 fixes) — a net conformance LOSS that also breaks the shrink-only ratchet. The
// reference is spec-compliant YAML 1.2; this package must be bug-for-bug Psych.
// These invariants are why the parser stays from-scratch. Do NOT "refactor to wrap
// gopkg.in/yaml.v3": it is structurally infeasible without breaking them. See
// README.md "Relationship to gopkg.in/yaml.v3".
func TestPsychModelInvariants(t *testing.T) {
	// Invariant 1: Float/Integer emit distinction (yaml.v3 collapses both to "2").
	if got := mustDump(t, 2.0); got != "--- 2.0\n" {
		t.Errorf("Dump(Float 2.0) = %q, want \"--- 2.0\\n\" (yaml.v3 emits \"2\")", got)
	}
	if got := mustDump(t, 2); got != "--- 2\n" {
		t.Errorf("Dump(Integer 2) = %q, want \"--- 2\\n\"", got)
	}

	// Invariant 2: Ruby Symbols (yaml.v3 has no Symbol type).
	if v, err := Load(":name\n"); err != nil || !eqValue(v, Symbol("name")) {
		t.Errorf("Load(\":name\") = %#v, err %v; want Symbol(\"name\")", v, err)
	}
	if got := mustDump(t, Symbol("checked")); got != "--- :checked\n" {
		t.Errorf("Dump(Symbol) = %q, want \"--- :checked\\n\"", got)
	}

	// Invariant 3: the Ruby object graph (yaml.v3 cannot reconstruct !ruby/object).
	v, err := Load("--- !ruby/object:Pt\nx: 1\ny: 2\n")
	o, ok := v.(*Object)
	if err != nil || !ok || o.Class != "Pt" || !eqValue(o.IVars["x"], int64(1)) || !eqValue(o.IVars["y"], int64(2)) {
		t.Errorf("Load(!ruby/object:Pt) = %#v, err %v; want *Object{Pt, x:1, y:2}", v, err)
	}

	// Invariant 4: Psych ACCEPT leniency yaml.v3 lacks (would wrongly reject).
	if v, err := Load("%YAML 1.2\n--- text\n"); err != nil || !eqValue(v, "text") {
		t.Errorf("Load(%%YAML 1.2 directive) = %#v, err %v; want \"text\" (yaml.v3 rejects)", v, err)
	}
	if v, err := Load(": a\n: b\n"); err != nil {
		t.Errorf("Load(nil-keyed explicit mapping) err %v; want accepted (yaml.v3 rejects), got %#v", err, v)
	}

	// Invariant 5: block-structure REJECT strictness yaml.v3 lacks (would wrongly accept).
	if _, err := Load("---\n[ a, b, c ] ]\n"); err == nil {
		t.Error("Load(stray flow close) accepted; want rejected (yaml.v3 accepts it)")
	}
	if _, err := Load("---\n[\nsequence item\n]\ninvalid item\n"); err == nil {
		t.Error("Load(junk after flow) accepted; want rejected (yaml.v3 accepts it)")
	}
}

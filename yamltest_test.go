// Copyright (c) the go-ruby-yaml/yaml authors
//
// SPDX-License-Identifier: BSD-3-Clause

package yaml

import (
	"embed"
	"io/fs"
	"sort"
	"strings"
	"testing"
)

// yamlTestSuite is the canonical cross-implementation YAML edge-case corpus
// yaml/yaml-test-suite (the stable data-2022-01-17 layout). Each test directory
// holds an `in.yaml` input and, when the input is meant to be rejected, an empty
// `error` marker file. Only those two files are vendored per test — the value/
// event expectations are deliberately not gated here: this package is a
// Psych-compatible loader (YAML 1.1 core-schema tag resolution), whereas the
// suite's JSON/event expectations use YAML 1.2 resolution, so a value-level
// comparison would score the 1.1-vs-1.2 schema difference rather than parser
// conformance. The accept/reject axis (does the loader parse well-formed YAML and
// reject ill-formed YAML) is schema-independent and is what a loader can be held
// to.
//
//go:embed yamltest
var yamlTestSuite embed.FS

// yamlSuiteKnownFailing is the frozen set of yaml-test-suite tests (keyed by test
// id, e.g. "229Q" or the variant "3RLN/03") whose accept/reject verdict this
// Psych-compatible loader does not match. It is a shrink-only conformance
// RATCHET: every test NOT listed here must get the required verdict — a plain
// test must load without error, an `error` test must be rejected — so no change
// may introduce a new divergence, and a listed test that starts matching is
// reported so the entry can be removed. Baseline captured 2026-08-03: 267/402
// pass (66.42%), 135 gaps.
//
// ROBUSTNESS phase (2026-08-03): the loader no longer panics on any input — the
// 21 slice-overrun panics on malformed flow collections were fixed (a lone '['
// or '{' now rejects cleanly). FLOW phase (2026-08-03): multi-line flow
// collections are now assembled across physical lines and validated — an
// unbalanced or under-indented flow, a stray closing bracket, junk (or a bare
// non-comment '#') after the close, and a missing ',' between entries all reject;
// valid multi-line flows load. TAB phase (2026-08-03): the coarse whole-source
// tab pre-scan (which rejected any tab after leading spaces, wrongly failing the
// many valid documents that carry a tab inside a scalar or block-scalar body) was
// replaced by a contextual check — a tab is rejected only when it indents a block
// mapping sibling, exactly where YAML forbids it. DIRECTIVE phase (2026-08-03):
// the leading "%…" directive block is now parsed and validated — a malformed or
// repeated %YAML version, a directive block with no document after it, and a
// %YAML/%TAG directive reopened after the first document's content (without the
// "..." end marker) are rejected; %TAG and unknown directives are tolerated.
// BLOCK-SCALAR-HEADER phase (2026-08-03): a "|"/">" block-scalar header is now
// fully parsed (explicit-indent digit 1–9 and chomping indicator in either order,
// then an optional comment) and a malformed header — text after the indicator, a
// 0 or multi-digit indent — is rejected (a plain scalar may not open with a block
// indicator). Baseline now 332/402 (82.59%), 0 panics, accept 305/308, 70 gaps.
// The remaining gaps break down as:
//   - Ill-formed input NOT rejected (67): the loader still accepts malformed
//     block-structure YAML it should reject (bad indentation, duplicate/compound
//     keys, tab after an indicator, trailing content, unterminated/lax scalars).
//     This input-validation gap is now the priority.
//   - Well-formed input NOT loaded (3): 6CA3 (a lone tab-indented flow bracket),
//     DK95/04 and Y79Y/002 (tab-only lines in constructs the line-based loader
//     resolves differently) — genuine tab/indentation corner cases.
//
// Each is a dedicated gap-closing target; the set may only shrink.
var yamlSuiteKnownFailing = map[string]bool{
	"236B": true, "2CMS": true, "3HFZ": true, "4HVU": true, "4JVG": true, "55WF": true,
	"5LLU": true, "5TRB": true, "5U3A": true, "6CA3": true, "6S55": true, "7LBH": true,
	"7MNF": true, "8XDJ": true, "9CWY": true, "9KBC": true, "9MAG": true, "9MQT/01": true,
	"BD7L": true, "BF9H": true, "BS4K": true, "C2SP": true, "CML9": true, "CQ3W": true,
	"CTN5": true, "CVW2": true, "CXX2": true, "D49Q": true, "DK4H": true, "DK95/04": true,
	"DMG6": true, "EW3V": true, "G5U8": true, "G7JE": true, "G9HC": true, "GDY7": true,
	"GT5M": true, "H7J7": true, "HRE5": true, "HU3P": true, "JKF3": true, "JY7Z": true,
	"LHL4": true, "MUS6/01": true, "N4JP": true, "Q4CL": true, "QB6E": true, "QLJ7": true,
	"RXY3": true, "S98Z": true, "SR86": true, "SU5Z": true, "SU74": true, "SY6V": true,
	"TD5N": true, "U44R": true, "U99R": true, "W9L4": true, "Y79Y/002": true, "Y79Y/004": true,
	"Y79Y/005": true, "Y79Y/006": true, "Y79Y/007": true, "Y79Y/008": true, "Y79Y/009": true, "YJV2": true,
	"ZCZ6": true, "ZL4Z": true, "ZVH3": true, "ZXT5": true,
}

// safeLoad calls Load, converting a panic into a flagged result so one broken
// input cannot abort the whole differential sweep. A panic is recorded as a
// robustness failure, never a graceful rejection.
func safeLoad(s string) (err error, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
		}
	}()
	_, err = Load(s)
	return err, false
}

// TestYAMLTestSuiteConformance is the differential accept/reject gate against the
// canonical yaml/yaml-test-suite corpus. Every test outside the knownFailing set
// must reach the required verdict; a new divergence fails CI and a listed test
// that now matches is reported so the ratchet can be tightened.
func TestYAMLTestSuiteConformance(t *testing.T) {
	var ids []string
	hasError := map[string]bool{}
	err := fs.WalkDir(yamlTestSuite, "yamltest", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == "in.yaml" {
			id := strings.TrimPrefix(strings.TrimSuffix(p, "/in.yaml"), "yamltest/")
			ids = append(ids, id)
		}
		if d.Name() == "error" {
			id := strings.TrimPrefix(strings.TrimSuffix(p, "/error"), "yamltest/")
			hasError[id] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk corpus: %v", err)
	}
	sort.Strings(ids)
	if len(ids) < 380 {
		t.Fatalf("expected ~402 yaml-test-suite tests, found %d", len(ids))
	}

	pass, acceptPass, acceptTot, rejectPass, rejectTot, panics := 0, 0, 0, 0, 0, 0
	var newFail, fixed []string
	for _, id := range ids {
		src, err := yamlTestSuite.ReadFile("yamltest/" + id + "/in.yaml")
		if err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		lerr, panicked := safeLoad(string(src))
		if panicked {
			panics++
		}
		wantErr := hasError[id]
		var ok bool
		switch {
		case panicked:
			// A panic is never the graceful (Value, error) contract; it fails the
			// verdict whatever the expectation, surfacing the robustness gap.
			ok = false
		case wantErr:
			ok = lerr != nil // ill-formed input MUST be rejected
		default:
			ok = lerr == nil // well-formed input MUST load
		}
		if wantErr {
			rejectTot++
			if ok {
				rejectPass++
			}
		} else {
			acceptTot++
			if ok {
				acceptPass++
			}
		}
		if ok {
			pass++
		}
		switch {
		case ok && yamlSuiteKnownFailing[id]:
			fixed = append(fixed, id)
		case !ok && !yamlSuiteKnownFailing[id]:
			newFail = append(newFail, id)
		}
	}
	t.Logf("yaml-test-suite (accept/reject): %d/%d tests pass (%.2f%%) — accept "+
		"%d/%d well-formed loaded, reject %d/%d ill-formed rejected (%d panicked); "+
		"%d known gaps", pass, len(ids), 100*float64(pass)/float64(len(ids)),
		acceptPass, acceptTot, rejectPass, rejectTot, panics, len(yamlSuiteKnownFailing))
	if len(fixed) > 0 {
		sort.Strings(fixed)
		t.Errorf("tests now getting the required verdict that are still listed in "+
			"yamlSuiteKnownFailing: %v\nremove them to tighten the ratchet", fixed)
	}
	if len(newFail) > 0 {
		sort.Strings(newFail)
		t.Errorf("REGRESSION: %d yaml-test-suite test(s) with the wrong verdict: %v",
			len(newFail), newFail)
	}
}

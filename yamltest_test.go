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
// This is the LARGEST hidden gap surfaced by this ratchet batch and warrants a
// dedicated closing campaign. The 135 gaps break down as:
//   - Ill-formed input NOT rejected (89): the loader accepts malformed YAML it
//     should reject — reject conformance is only 5/94 (5.3%). This lax-parsing /
//     input-validation gap is the priority: a loader that accepts almost any
//     byte stream cannot flag corrupt documents. (Includes 4 that panic on the
//     bad input: KS4U, N782, T833, VJP3/00.)
//   - Well-formed input NOT loaded (46): valid YAML that errors or panics —
//     genuine parser gaps plus a few YAML 1.1/Psych corner cases (anchors,
//     complex keys, tag/directive edge cases, multi-document streams).
//   - Parser panics (21 total): the loader panics instead of returning an error
//     on 21 inputs (17 well-formed, 4 ill-formed) — a robustness defect that
//     should be the first thing fixed, independent of the schema questions.
//
// Each is a dedicated gap-closing target; the set may only shrink.
var yamlSuiteKnownFailing = map[string]bool{
	"236B": true, "2CMS": true, "2G84/00": true, "2G84/01": true, "3HFZ": true, "3RLN/02": true,
	"3RLN/05": true, "4ABK": true, "4FJ6": true, "4H7K": true, "4HVU": true, "4JVG": true,
	"4ZYM": true, "55WF": true, "5GBF": true, "5LLU": true, "5TRB": true, "5U3A": true,
	"62EZ": true, "6CA3": true, "6HB6": true, "6JTT": true, "6S55": true, "7A4E": true,
	"7LBH": true, "7MNF": true, "87E4": true, "8UDB": true, "8XDJ": true, "96NN/00": true,
	"96NN/01": true, "9C9N": true, "9CWY": true, "9HCY": true, "9JBA": true, "9KBC": true,
	"9MAG": true, "9MMA": true, "9MQT/01": true, "B63P": true, "BD7L": true, "BF9H": true,
	"BS4K": true, "C2DT": true, "C2SP": true, "CML9": true, "CN3R": true, "CQ3W": true,
	"CT4Q": true, "CTN5": true, "CVW2": true, "CXX2": true, "D49Q": true, "DFF7": true,
	"DK4H": true, "DK95/00": true, "DK95/02": true, "DK95/03": true, "DK95/04": true, "DK95/05": true,
	"DK95/07": true, "DK95/08": true, "DMG6": true, "EB22": true, "EHF6": true, "EW3V": true,
	"FRK4": true, "G5U8": true, "G7JE": true, "G9HC": true, "GDY7": true, "GT5M": true,
	"H7J7": true, "H7TQ": true, "HRE5": true, "HS5T": true, "HU3P": true, "J3BT": true,
	"JKF3": true, "JY7Z": true, "KS4U": true, "L9U5": true, "LHL4": true, "LQZ7": true,
	"M7NX": true, "M9B4": true, "MJS9": true, "MUS6/00": true, "MUS6/01": true, "N4JP": true,
	"N782": true, "NB6Z": true, "P2EQ": true, "PRH3": true, "Q4CL": true, "Q5MG": true,
	"QB6E": true, "QF4Y": true, "QLJ7": true, "R4YG": true, "RHX7": true, "RXY3": true,
	"S4GJ": true, "S98Z": true, "SF5V": true, "SR86": true, "SU5Z": true, "SU74": true,
	"SY6V": true, "T5N4": true, "T833": true, "TD5N": true, "TL85": true, "U44R": true,
	"U99R": true, "UV7Q": true, "VJP3/00": true, "VJP3/01": true, "W9L4": true, "WZ62": true,
	"X4QW": true, "Y79Y/001": true, "Y79Y/002": true, "Y79Y/004": true, "Y79Y/005": true, "Y79Y/006": true,
	"Y79Y/007": true, "Y79Y/008": true, "Y79Y/009": true, "YJV2": true, "ZCZ6": true, "ZF4X": true,
	"ZL4Z": true, "ZVH3": true, "ZXT5": true,
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

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
// COMPOUND-KEY phase (2026-08-03): an inline mapping value that still carries a
// top-level "key: " separator ("a: b: c", "a: 'b': c") — a second entry fused on
// without a line break — is now rejected, using a flow- and quote-aware colon scan
// so nested flow collections and quoted scalars are not misread. Baseline 335/402
// (83.33%), 0 panics, accept 305/308, 67 gaps.
// MULTILINE-SCALAR-FOLDING phase (2026-08-04): plain and single/double-quoted
// scalars now fold across continuation lines (a single break to a space, blank
// lines to newlines, per-line whitespace stripped, a double-quote "\" eliding the
// break), so their tails are consumed rather than dropped. This closed the classes
// the fold makes reachable: a quoted scalar that never closes or is broken by a
// document marker is rejected as unterminated (5TRB, 9MQT/01, CQ3W, QB6E, RXY3),
// junk after a closing quote rejects (Q4CL, SU5Z), a "key: " fused into a folded
// plain scalar rejects (2CMS, EW3V, HU3P, JKF3), and the value-level colon/quote
// scans were hardened (a mid-scalar quote is no longer read as opening a quoted
// span; DK95/04, 6CA3, 9KBC, Y79Y/002 also fixed via whitespace-only-line and
// block-scalar-tab handling). Baseline 350/402 (87.06%), 0 panics, accept 308/308.
// DQ-ESCAPE phase (2026-08-04): the double-quoted escape decoder now validates
// against the full YAML escape table (\a \b \v \f \e \  \/ \N \_ \L \P \u \U and an
// escaped tab, in addition to the common few) and rejects an escape indicator
// outside it ("\." , "\'"), matching Psych/libyaml; a malformed \x/\u/\U hex
// payload stays lenient. This graduates 55WF and HRE5. Baseline now 352/402
// (87.56%), 0 panics, accept 308/308 well-formed loaded, 50 gaps.
// MULTILINE-NODE-PROPERTY phase (2026-08-04): a node's &anchor / !tag may be
// written on its own (possibly more-indented) line, spread over several lines,
// ahead of the node they annotate; the loader now merges those property lines and
// attaches them to the following node (a scalar, a block scalar, a mapping, or a
// zero-indent sequence), consuming tails it previously dropped. No corpus verdict
// changed yet — this unblocks the trailing-content gate.
// COMMENTS-AND-WHITESPACE phase (2026-08-04): a "#" comment is now consumed in
// every non-content position — a comment-only value after "key:" / "? " / ": " /
// "- ", a "--- # comment" or "... # comment" marker suffix — and a tab is accepted
// as the whitespace after a "key:" or "-" indicator (only tab INDENTATION stays
// forbidden). A column-0 "%…" line reached after a plain scalar's content has begun
// folds into the scalar rather than reopening a directive (yaml-test-suite XLQ9).
// This graduates Y79Y/009. Baseline now 353/402 (87.81%), accept 308/308, 49 gaps.
// FLOW-BLOCK-KEY phase (2026-08-04): a flow collection ("{a: 1}", "[x]") used as a
// complex block-mapping key — its ": value" (or block value beneath) following the
// closed collection — now parses as a mapping (splitMapEntry finds the separator
// past the flow interior via topColon), and a compact block sequence introduced
// inline after ": " keeps its wrapped entries aligned under the "-" column. Both
// are accept cases (Q9WF, 5WE3) already loading; consuming their full extent is the
// last tail the trailing-content gate needs. Corpus steady at 353/402, accept
// 308/308.
// TRAILING-CONTENT-GATE phase (2026-08-04): now that the property/comment/flow
// features above consume the tails a well-formed document legitimately carries,
// load() calls checkStreamEnd after the first document — any unconsumed line that
// is not a following "---" document marker is trailing garbage and rejects the
// stream. This graduates 19 ill-formed cases the loader had accepted: trailing
// content after a block document (236B, TD5N, 6S55, 9CWY, BD7L, 7MNF), inconsistent
// block indentation (4HVU, DMG6, N4JP, U44R, ZVH3), a bad block-scalar indent
// (BF9H), anchor/tag misuse (G9HC, GT5M, H7J7), a stray comment (BS4K), and
// multi-line key misuse (7LBH, D49Q, G7JE).
// The gate was confirmed to keep accept at 308/308 (a full-corpus diff showed every
// previously-valid document still loads). Baseline now 372/402 (92.54%), accept
// 308/308, reject 64/94, 30 gaps.
// ANCHOR-TAG-VALIDATION phase (2026-08-04): finer structural checks on node
// properties now reject the anchor/tag-misuse cluster: a node carrying two anchors
// (4JVG — an anchor on the "key:" value line and another on the value's own line
// both landing on one scalar), an anchor on an alias node in value or key position
// (SR86 "&b *a", SU74 "&b *alias : v"), an anchor followed inline by a sequence
// entry ("&anchor - x", SY6V), an anchor on the "---" header line ahead of an inline
// mapping ("--- &a k: v", CXX2), and a shorthand/named tag carrying a flow-indicator
// character ("!!str," U99R, "!invalid{}tag" LHL4; verbatim "!<...>" tags stay
// exempt). Each rule was verified narrow against the full valid set: the near-twin
// valid documents 7BMT/U3XV (a value anchor over a mapping whose key is separately
// anchored — one anchor per node), FTA2/KSS4 (a header anchor over a sequence/scalar),
// 5KJE/UDR7 (trailing flow commas), and 7FWL/UGM3 (verbatim tags with commas) all
// still load. Baseline now 379/402 (94.28%), accept 308/308, reject 71/94, 23 gaps.
// FLOW-EDGE-CASE phase (2026-08-04): flow-collection validation now rejects an empty
// entry between commas ("[ , a ]" 9MAG, "[ a, b, , ]" CTN5) while still allowing a
// single trailing comma ("[ a, b, ]" 5KJE/UDR7), a lone plain dash in a flow sequence
// ("[-]" YJV2, "[-, -]" G5U8), a comment character wedged against a comma ("c,#invalid"
// CVW2), a multi-line flow collection used as an implicit mapping key ("[23\n]: 42"
// C2SP), and a flow-SEQUENCE implicit key split from its ":" by a line break ("[ key\n
// : value ]" DK4H, ZXT5). The last check is gated on the innermost bracket being a
// sequence, so the many valid flow MAPPINGS that legitimately break a key from its
// ":" ("{\"foo\"\n: \"bar\"}" 4MUZ/00-02, 5MUD, K3WX, VJP3/01, FRK4) still load.
// Baseline now 387/402 (96.27%), accept 308/308, reject 79/94, 15 gaps.
// TAB-INDICATOR phase (2026-08-04): a tab used as separation-then-indentation right
// after a block indicator now rejects — a "-" entry whose tab separator precedes a
// NESTED block indicator ("-\t-" Y79Y/004, "- \t-" Y79Y/005), a "?" explicit-key
// indicator followed by a tab ("?\t-" Y79Y/006, "?\tkey:" Y79Y/008), and a ":"
// explicit-value indicator followed by a tab (":\t-" Y79Y/007). A tab before a plain
// scalar or an empty entry ("- \tfoo", "- \t" Y79Y/010, "foo: |\n \t" Y79Y/001) stays
// valid separation. Baseline now 392/402 (97.51%), accept 308/308, reject 84/94,
// 10 gaps.
// BLOCK-SCALAR-INDENT phase (2026-08-04): with auto-detected block-scalar
// indentation (no explicit indent indicator), a leading empty line more-indented than
// the first non-empty content line is now rejected ("|"/">" then spaces-only lines
// wider than the first content line — 5LLU, W9L4). The block-scalar header parse now
// reports whether an explicit indent digit was present so the check is skipped there
// (an explicit indicator pins the content indent instead of auto-detecting it).
// Baseline now 394/402 (98.01%), accept 308/308, reject 86/94, 8 gaps.
// BLOCK-BOUNDARY phase (2026-08-04): two block-structure boundary checks now reject a
// block sequence opened on the same line as its "key:" ("key: - a" 5U3A — the "- x"
// must start on the following line; the explicit "? key" / ": value" compact-sequence
// construct KK5P stays valid on its own path) and any content other than a comment
// after the document-end marker ("... invalid" 3HFZ). Baseline now 396/402 (98.51%),
// accept 308/308, reject 88/94, 6 gaps. The remaining gaps break down as:
//   - Ill-formed input NOT rejected (~30): anchor/tag misuse (4JVG, CXX2, SR86,
//     SU74, SY6V, U99R, LHL4), flow-collection edge cases (9MAG, CTN5, CVW2, G5U8,
//     YJV2, C2SP, DK4H, ZXT5, CML9), stray comments (8XDJ, GDY7), directives
//     (MUS6/01, QLJ7), bad block-scalar content indentation (5LLU, S98Z, W9L4, 3HFZ),
//     inconsistent indentation (5U3A) and the Y79Y/004-008 variants. These need
//     finer structural validation than the coarse trailing-content gate.
//   - Well-formed input NOT loaded: none — every well-formed corpus document now
//     loads (accept 308/308).
//
// Each is a dedicated gap-closing target; the set may only shrink.
var yamlSuiteKnownFailing = map[string]bool{
	"8XDJ":    true,
	"CML9":    true,
	"GDY7":    true,
	"MUS6/01": true, "QLJ7": true,
	"S98Z": true,
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

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

// parseError is the sentinel a loader method panics with to reject a malformed
// document. load recovers it and returns it as a *SyntaxError, so the parser can
// bail out of any depth without threading an error return through every method.
// It is the ONLY value the loader may panic with: a runtime panic (e.g. a slice
// overrun on adversarial input) is a robustness bug, not a rejection, and must be
// fixed at its source rather than masked here — the fuzz corpus guards that.
type parseError struct{ msg string }

// fail aborts the current parse, rejecting the document with the given message.
func (l *loader) fail(msg string) { panic(parseError{msg: msg}) }

// load parses a complete YAML document string into a Ruby value. A blank or
// marker-only document loads as nil (Psych's empty-document behaviour). A
// malformed document is rejected with a *SyntaxError; the parser never panics on
// input, however adversarial (see parseError).
func load(src string) (v Value, err error) {
	l := &loader{anchors: map[string]Value{}}
	defer func() {
		if r := recover(); r != nil {
			// Only a parseError is a graceful rejection; the unchecked assertion
			// re-raises anything else so a genuine bug is never silently swallowed.
			v, err = nil, &SyntaxError{Message: r.(parseError).msg}
		}
	}()
	l.tokenize(src)
	l.skipBlanks()
	l.parseDirectives()
	if l.pos >= len(l.lines) {
		return nil, nil
	}
	v = l.parseDocument()
	l.checkNoTrailingDirective()
	l.checkStreamEnd()
	return v, nil
}

// parseDirectives consumes a leading run of "%…" directive lines (the directive
// block that precedes the first document) and validates it. A %YAML directive
// must carry exactly one well-formed version (optionally trailed by a comment)
// and may not repeat; other directives (%TAG, unknown) are tolerated. A directive
// block must be closed by a "---" document-start marker — directives with no
// document following are rejected. The cursor is left on that marker (or, when
// there were no directives, wherever it started).
func (l *loader) parseDirectives() {
	seenYAML, saw := false, false
	for l.pos < len(l.lines) && strings.HasPrefix(l.lines[l.pos].content, "%") {
		l.checkDirective(l.lines[l.pos].content, &seenYAML)
		saw = true
		l.pos++
		for l.pos < len(l.lines) && isBlankContent(l.lines[l.pos].content) {
			l.pos++ // a blank or whitespace-only (e.g. a lone tab) line separates
		}
	}
	if !saw {
		return
	}
	if l.pos >= len(l.lines) {
		l.fail("directives must be followed by a document")
	}
	if c := l.lines[l.pos].content; c != "---" && !strings.HasPrefix(c, "--- ") {
		l.fail("directives must be followed by a document")
	}
}

// isBlankContent reports whether a line's content is empty or only whitespace
// (spaces or tabs) — a line that carries no node.
func isBlankContent(s string) bool {
	return strings.TrimLeft(s, " \t") == ""
}

// checkDirective validates one directive line. Only %YAML is constrained; %TAG
// and unknown directives are accepted verbatim (Psych ignores unknown directives
// with a warning).
func (l *loader) checkDirective(content string, seenYAML *bool) {
	fields := strings.Fields(content)
	if fields[0] != "%YAML" {
		return
	}
	if *seenYAML {
		l.fail("%YAML directive may not repeat")
	}
	*seenYAML = true
	if len(fields) < 2 || !isYAMLVersion(fields[1]) {
		l.fail("malformed %YAML directive")
	}
	if len(fields) > 2 && !strings.HasPrefix(fields[2], "#") {
		l.fail("malformed %YAML directive")
	}
}

// isYAMLVersion reports whether s is a "major.minor" version number.
func isYAMLVersion(s string) bool {
	dot := strings.IndexByte(s, '.')
	if dot <= 0 || dot >= len(s)-1 {
		return false
	}
	return allDigits(s[:dot]) && allDigits(s[dot+1:])
}

// allDigits reports whether s is non-empty and entirely ASCII digits.
func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// checkNoTrailingDirective rejects a %YAML / %TAG directive that opens a second
// directive+document block right after the first document's content, without the
// intervening "..." end marker YAML requires (e.g. "scalar\n%YAML 1.2\n---").
// It fires only when a "---" marker follows the stray directive; a lone trailing
// "%…" line with no document after it is a plain-scalar continuation, not a
// directive, and is left alone.
func (l *loader) checkNoTrailingDirective() {
	l.skipBlanks()
	if l.pos >= len(l.lines) || !isKnownDirective(l.lines[l.pos].content) {
		return
	}
	for i := l.pos + 1; i < len(l.lines); i++ {
		if c := l.lines[i].content; c == "---" || strings.HasPrefix(c, "--- ") {
			l.fail("directive after document content")
		}
	}
}

// isKnownDirective reports whether content is a %YAML or %TAG directive line.
func isKnownDirective(content string) bool {
	for _, name := range []string{"%YAML", "%TAG"} {
		if content == name || strings.HasPrefix(content, name+" ") {
			return true
		}
	}
	return false
}

// parseDocument parses the first document of the stream, honouring a leading
// "---" directives-end marker, and leaves the cursor just past it.
func (l *loader) parseDocument() Value {
	markerPos := l.pos
	first := l.lines[markerPos]
	if first.content == "---" || strings.HasPrefix(first.content, "--- ") {
		rest := ""
		if len(first.content) > 4 {
			rest = strings.TrimSpace(first.content[4:])
		}
		if strings.HasPrefix(rest, "#") {
			// A "--- # comment" marker carries only a trailing comment: the document's
			// node, if any, follows on the next line, exactly as for a bare "---".
			rest = ""
		}
		if rest == "" {
			l.pos = markerPos + 1
			if l.peek() == nil {
				return nil
			}
			return l.parseNode(0)
		}
		if style, chomp, ok := l.blockScalarHeader(rest); ok {
			l.pos = markerPos + 1
			// A document-root block scalar has no parent node, so its body may sit at
			// column 0; -1 lets a body line at any indentation (including 0) count.
			return l.parseBlockScalar(style, chomp, -1)
		}
		if tag, _, content := splitTagAnchor(rest); tag != "" && content == "" {
			l.pos = markerPos + 1
			if l.peek() == nil {
				return l.taggedEmpty(tag)
			}
			if isSeqEntry(l.peek().content) {
				return l.parseSequence(l.peek().indent, tag)
			}
			return l.parseMapping(l.peek().indent, tag)
		}
		l.lines[markerPos].content = rest
		l.lines[markerPos].indent = first.indent
		return l.parseNode(0)
	}
	return l.parseNode(0)
}

// checkStreamEnd rejects content left over after the first document was parsed.
// A following "---" marker starts a second document — a well-formed multi-document
// stream, of which this loader materialises only the first — and is allowed; any
// other unconsumed line is trailing garbage and rejects the stream.
func (l *loader) checkStreamEnd() {
	l.skipBlanks()
	if l.pos >= len(l.lines) {
		return
	}
	c := l.lines[l.pos].content
	if c == "---" || strings.HasPrefix(c, "--- ") {
		return
	}
	l.fail("trailing content after document")
}

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
		if strings.TrimLeft(content, "\t") == "" {
			// A whitespace-only line (spaces and/or tabs) carries no node — it is a
			// blank line, significant only as a paragraph break inside a block scalar.
			l.lines = append(l.lines, line{blank: true, raw: trimmed})
			continue
		}
		if content == "..." || strings.HasPrefix(content, "... ") {
			// The document-end marker "..." ends the stream this loader materialises; a
			// trailing "# comment" after it (the only well-formed suffix) is discarded
			// with it.
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

// blockScalarTag parses a block-scalar header, reporting the style byte ('|' or
// '>') and chomp ('-', '+', or 0) of a WELL-FORMED header. The header is the
// indicator optionally followed, in either order, by a single explicit-indent
// digit (1–9) and a single chomping indicator, then optional trailing whitespace
// and a "# comment". ok is false when content does not open with '|'/'>' at all OR
// when it does but the header is malformed (a 0 or multi-digit indent, a repeated
// indicator, or trailing junk). content must open with '|' or '>' (the caller,
// blockScalarHeader, guarantees it).
func blockScalarTag(content string) (style, chomp byte, ok bool) {
	style = content[0]
	rest := content[1:]
	i, sawIndent, sawChomp := 0, false, false
	for i < len(rest) {
		c := rest[i]
		if (c == '+' || c == '-') && !sawChomp {
			sawChomp, chomp = true, c
			i++
			continue
		}
		if c >= '1' && c <= '9' && !sawIndent {
			sawIndent = true
			i++
			continue
		}
		break
	}
	tail := rest[i:]
	trimmed := strings.TrimLeft(tail, " \t")
	if trimmed == "" || (strings.HasPrefix(trimmed, "#") && len(tail) > len(trimmed)) {
		return style, chomp, true
	}
	return 0, 0, false
}

// blockScalarHeader parses a block-scalar header on a value/node line, rejecting a
// malformed one. ok is false only when content does not open a block scalar at all
// (no leading '|'/'>'); a '|'/'>' followed by an invalid header is rejected, since
// a plain scalar may not begin with a block indicator.
func (l *loader) blockScalarHeader(content string) (style, chomp byte, ok bool) {
	if content == "" || (content[0] != '|' && content[0] != '>') {
		return 0, 0, false
	}
	style, chomp, valid := blockScalarTag(content)
	if !valid {
		l.fail("malformed block scalar header")
	}
	return style, chomp, true
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
			if bodyIndent < 0 && strings.ContainsRune(ln.raw, '\t') &&
				leadingSpaces(ln.raw) <= parentIndent {
				// A tab within the block scalar's indentation zone — no deeper than the
				// parent's indent, before any content line has fixed the body indent — is
				// a tab used for indentation, which YAML forbids. A tab further right (on
				// a more-indented line) is ordinary content.
				l.fail("found a tab character used for indentation")
			}
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

// parseNode parses the node whose first line is l.lines[l.pos]. minIndent is the
// least indentation a continuation of this node may occupy: a multi-line flow
// collection folded here must keep its wrapped lines at or beyond it, so a flow
// value dedented to (or past) its own block key is rejected.
func (l *loader) parseNode(minIndent int) Value {
	return l.parseNodeProps(minIndent, "", nil)
}

// parseNodeProps parses the node at the cursor, carrying an inTag / inAnchors set
// accumulated from preceding property-only lines. YAML lets a node's properties
// (its `&anchor` and/or `!tag`) sit on their own line — even more-indented — ahead
// of the node they annotate, and lets them spread over several such lines; this
// method merges the current line's own leading tag/anchor with the inherited ones
// so an anchor and a tag written on separate lines both attach to the single node
// that follows.
func (l *loader) parseNodeProps(minIndent int, inTag string, inAnchors []string) Value {
	l.skipBlanks()
	ln := l.lines[l.pos]
	tag, anchorName, content := splitTagAnchor(ln.content)
	if tag == "" {
		tag = inTag // an inherited tag survives a following property line that has none
	}
	anchors := inAnchors
	if anchorName != "" {
		anchors = append(anchors, anchorName)
	}
	if strings.HasPrefix(content, "#") {
		// What is left of a node line after its tag/anchor cannot begin with a bare
		// "#": a whitespace-preceded "#" opened a comment, so the node has no inline
		// content (e.g. "key: &a # comment" carries only the anchor).
		content = ""
	}
	if content == "" {
		l.lines[l.pos].content = ""
		l.pos++
		return l.parseBlockProps(ln.indent, tag, anchors)
	}
	l.lines[l.pos].content = content
	if isSeqEntry(content) {
		v := l.parseSequence(ln.indent, tag)
		l.bindAll(anchors, v)
		return v
	}
	if _, _, isMap := splitMapEntry(content); isMap || isExplicitKey(content) {
		// A mapping entry — including one whose key is a flow collection used as a
		// complex key ("{a: 1}: value"), which splitMapEntry now recognises.
		v := l.parseMapping(ln.indent, tag)
		l.bindAll(anchors, v)
		return v
	}
	l.pos++
	if isFlowStart(content) {
		var isKey bool
		content, isKey = l.gatherFlow(content, minIndent)
		if minIndent == 0 && !isKey {
			// A flow collection standing as the whole document consumes its closing
			// bracket exactly; anything left but a following "---" is trailing garbage.
			// (When the flow is a mapping key its ": value" legitimately follows.)
			l.checkStreamEnd()
		}
	} else if !strings.HasPrefix(content, "*") {
		// A plain or quoted scalar may fold across continuation lines; an alias
		// (`*name`) is always a single token and is left untouched.
		content = l.gatherScalar(content, minIndent)
	}
	v := l.scalarValue(content, tag)
	l.bindAll(anchors, v)
	return v
}

// isDocMarker reports whether a line's content is a document-boundary marker —
// "---" / "..." or those followed by more text ("--- x", "... x"). A quoted or
// plain scalar may not fold across such a line.
func isDocMarker(content string) bool {
	for _, m := range []string{"---", "..."} {
		if content == m || strings.HasPrefix(content, m+" ") {
			return true
		}
	}
	return false
}

// gatherScalar folds the continuation lines of the plain or single/double-quoted
// scalar that opened on the just-consumed line into one logical scalar token,
// advancing the cursor past every line it absorbs. minIndent is the least
// indentation a continuation line may occupy (the containing block's indent, or 0
// at the document root). A quoted scalar that never closes, and a plain
// continuation line that reintroduces a "key: " mapping pair, reject the document.
func (l *loader) gatherScalar(first string, minIndent int) string {
	if first[0] == '"' || first[0] == '\'' {
		return l.gatherQuoted(first, first[0], minIndent)
	}
	return l.gatherPlain(first, minIndent)
}

// gatherPlain folds a plain (unquoted) multi-line scalar. Per the YAML line-folding
// rules a single line break between non-empty lines folds to a space and each blank
// line in a run contributes one literal newline; leading and trailing whitespace on
// each folded line is stripped. A whitespace-preceded "#" opens a comment that ends
// the scalar. A continuation line carrying a top-level "key: " separator is a
// mapping fused into the scalar, which block YAML forbids, and is rejected.
func (l *loader) gatherPlain(first string, minIndent int) string {
	head, sawComment := splitPlainComment(first)
	var b strings.Builder
	b.WriteString(strings.TrimRight(head, " \t"))
	breaks := 0
	for !sawComment && l.pos < len(l.lines) {
		ln := l.lines[l.pos]
		if ln.blank {
			breaks++
			l.pos++
			continue
		}
		if ln.indent < minIndent || isDocMarker(ln.content) {
			// A dedent ends the scalar, as does a document-boundary marker. A column-0
			// "%…" line, by contrast, is NOT a directive once document content has begun
			// (directives precede the first "---"): here it is ordinary plain-scalar
			// continuation and is folded in.
			break
		}
		body, cmt := splitPlainComment(ln.content)
		body = strings.TrimRight(strings.TrimLeft(body, " \t"), " \t")
		if topColon(body) >= 0 {
			l.fail("mapping key in a multi-line plain scalar")
		}
		if breaks == 0 {
			b.WriteByte(' ')
		} else {
			b.WriteString(strings.Repeat("\n", breaks))
		}
		b.WriteString(body)
		breaks = 0
		sawComment = cmt
		l.pos++
	}
	return b.String()
}

// splitPlainComment splits a plain-scalar line at a whitespace-preceded "#"
// comment, returning the content before it and whether a comment was found. A "#"
// not preceded by whitespace is an ordinary scalar character.
func splitPlainComment(s string) (head string, hasComment bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '#' && i > 0 && (s[i-1] == ' ' || s[i-1] == '\t') {
			return s[:i], true
		}
	}
	return s, false
}

// gatherQuoted folds a single- or double-quoted scalar (quote byte q) that may
// span several physical lines, returning a synthetic single-line token — the
// interior with each physical break folded to a space (blank lines to newlines,
// and, for a double-quoted scalar, a trailing "\" eliding the break entirely) — re-
// wrapped in its quotes for the scalar decoder. A scalar that never closes, or one
// broken by a document marker, is rejected as unterminated; junk after the closing
// quote on its line is rejected as trailing content.
func (l *loader) gatherQuoted(first string, q byte, minIndent int) string {
	interior, trailer, closed := scanQuote(first[1:], q)
	if closed {
		l.checkQuoteTrailer(trailer)
		return string(q) + interior + string(q)
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(interior, " \t"))
	breaks := 0
	for {
		if l.pos >= len(l.lines) {
			l.fail("unterminated quoted scalar")
		}
		ln := l.lines[l.pos]
		if ln.blank {
			breaks++
			l.pos++
			continue
		}
		if ln.indent < minIndent || isDocMarker(ln.content) {
			l.fail("unterminated quoted scalar")
		}
		l.pos++
		lineText := strings.TrimLeft(ln.content, " \t")
		seg, trail, done := scanQuote(lineText, q)
		if q == '"' && breaks == 0 && strings.HasSuffix(b.String(), "\\") && !endsEscaped(b.String()) {
			// A double-quoted line ending in a lone backslash is an escaped line
			// break: drop the backslash and join with no separator.
			cur := b.String()
			b.Reset()
			b.WriteString(cur[:len(cur)-1])
		} else if breaks == 0 {
			b.WriteByte(' ')
		} else {
			b.WriteString(strings.Repeat("\n", breaks))
		}
		breaks = 0
		if done {
			b.WriteString(strings.TrimRight(seg, " \t"))
			// Preserve trailing space directly before the closing quote.
			b.WriteString(trailingSpace(seg))
			l.checkQuoteTrailer(trail)
			return string(q) + b.String() + string(q)
		}
		b.WriteString(strings.TrimRight(seg, " \t"))
	}
}

// leadingSpaces counts the run of spaces at the start of s (its space indentation,
// stopping at the first non-space — a tab included).
func leadingSpaces(s string) int {
	n := 0
	for n < len(s) && s[n] == ' ' {
		n++
	}
	return n
}

// trailingSpace returns the run of spaces/tabs at the end of s (the whitespace a
// quoted scalar preserves immediately before its closing quote).
func trailingSpace(s string) string {
	i := len(s)
	for i > 0 && (s[i-1] == ' ' || s[i-1] == '\t') {
		i--
	}
	return s[i:]
}

// endsEscaped reports whether s ends with an escaped backslash ("\\"), i.e. the
// final backslash is itself escaped and so is not a line-continuation marker.
func endsEscaped(s string) bool {
	n := 0
	for i := len(s) - 1; i >= 0 && s[i] == '\\'; i-- {
		n++
	}
	return n%2 == 0
}

// scanQuote scans a quoted-scalar fragment (text is the line content after the
// opening quote on the first line, or a whole continuation line) for the closing
// quote q, honouring "\\" / '\"' escapes in double quotes and "”" in single
// quotes. It returns the interior up to (excluding) the close, the trailer after
// it, and whether the quote closed on this fragment.
func scanQuote(text string, q byte) (interior, trailer string, closed bool) {
	for i := 0; i < len(text); i++ {
		c := text[i]
		if q == '"' && c == '\\' {
			i++ // skip the escaped character
			continue
		}
		if c == q {
			if q == '\'' && i+1 < len(text) && text[i+1] == '\'' {
				i++ // "''" is an escaped quote, not a close
				continue
			}
			return text[:i], text[i+1:], true
		}
	}
	return text, "", false
}

// checkQuoteTrailer rejects junk after a quoted scalar's closing quote. Only
// trailing whitespace and a whitespace-preceded "# comment" are allowed; a mapping
// "key: value" whose key is quoted is split before this point, so a ":" trailer
// never reaches here.
func (l *loader) checkQuoteTrailer(after string) {
	trimmed := strings.TrimLeft(after, " \t")
	if trimmed == "" || (trimmed[0] == '#' && len(after) > len(trimmed)) {
		return
	}
	l.fail("trailing content after quoted scalar")
}

// parseBlock parses the block that follows a standalone tag / anchor with no
// carried-over anchors (the sequence-entry and explicit-key callers).
func (l *loader) parseBlock(parentIndent int, tag string) Value {
	return l.parseBlockProps(parentIndent, tag, nil)
}

// parseBlockProps parses the block body of a node whose properties (tag / anchors)
// were on the preceding line(s), binding those anchors to the result. The body is
// the block beneath the property line: a sequence (which YAML lets sit at, or in
// from, the property/key column, so it is taken whatever its indent), a block
// scalar, a mapping, a further property-only line (recursed into so a multi-line
// property run collapses onto one node), or a scalar / flow node. A non-sequence
// body that dedents below the property line is not this node's content but a
// sibling construct, leaving the node empty.
func (l *loader) parseBlockProps(parentIndent int, tag string, anchors []string) Value {
	l.skipBlanks()
	if l.pos >= len(l.lines) {
		return l.bindEmpty(tag, anchors)
	}
	child := l.lines[l.pos]
	if isSeqEntry(child.content) {
		v := l.parseSequence(child.indent, tag)
		l.bindAll(anchors, v)
		return v
	}
	if style, chomp, ok := l.blockScalarHeader(child.content); ok {
		// A block scalar forms the body. Its header may sit more-indented than its own
		// (explicitly-indented) content, so the body floor is taken two columns in from
		// the header line, letting a body dedented below the header still be captured.
		l.pos++
		v := l.parseBlockScalar(style, chomp, max(child.indent-2, -1))
		l.bindAll(anchors, v)
		return v
	}
	if child.indent < parentIndent {
		return l.bindEmpty(tag, anchors)
	}
	if _, _, isMap := splitMapEntry(child.content); isMap || isExplicitKey(child.content) {
		v := l.parseMapping(child.indent, tag)
		l.bindAll(anchors, v)
		return v
	}
	// A scalar / flow node, or a further property-only line: recurse, carrying the
	// tag and anchors so they land on the eventual node.
	return l.parseNodeProps(parentIndent+1, tag, anchors)
}

// bindEmpty binds the carried anchors to an empty (possibly tagged) node — the node
// a property line annotates when no body follows.
func (l *loader) bindEmpty(tag string, anchors []string) Value {
	v := l.taggedEmpty(tag)
	l.bindAll(anchors, v)
	return v
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
		rest := strings.TrimLeft(strings.TrimPrefix(ln.content, "-"), " \t")
		if strings.HasPrefix(rest, "#") {
			rest = "" // a "- # comment" entry carries only a comment: the value is below
		}
		if rest == "" {
			l.pos++
			if l.peek() != nil && l.peek().indent > indent {
				arr = append(arr, l.parseBlock(indent, ""))
			} else {
				arr = append(arr, nil)
			}
			continue
		}
		if style, chomp, ok := l.blockScalarHeader(rest); ok {
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
		if ln.content != "" && ln.content[0] == '\t' {
			// The tokenizer strips only leading spaces, so a sibling entry whose
			// content still opens with a tab used that tab for indentation — which
			// YAML forbids. (Tabs inside a scalar or block-scalar body reach this
			// point never: those lines are consumed by their own reader.)
			l.fail("found a tab character used for indentation")
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
		if val == "" || val[0] == '#' {
			// An empty value — or one that is only a "# comment" (a plain scalar may
			// not open with '#', so a leading '#' after "key:" is always a comment) —
			// means the real value, if any, is the indented block beneath the key.
			l.pos++
			if next := l.peek(); next != nil && (next.indent > indent || (next.indent == indent && isSeqEntry(next.content))) {
				h.Set(key, l.parseNode(indent+1))
			} else {
				h.Set(key, nil)
			}
			continue
		}
		val = strings.TrimSpace(val)
		if _, _, body := splitTagAnchor(val); !strings.HasPrefix(body, "*") && topColon(body) >= 0 {
			// An inline value that still holds a "key: " separator at the top level is
			// a second mapping entry fused onto the first without a line break —
			// "a: b: c", "a: 'b': c" — which block YAML does not allow. topColon skips
			// quoted spans and flow-collection interiors (which carry their own pairs),
			// so a flow value or a quoted scalar is not misread.
			l.fail("nested mapping in a plain scalar value")
		}
		if style, chomp, ok := l.blockScalarHeader(val); ok {
			l.pos++
			h.Set(key, l.parseBlockScalar(style, chomp, indent))
			continue
		}
		l.lines[l.pos].content = val
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
	if strings.HasPrefix(rest, "#") {
		rest = "" // "? # comment": the key node is the indented block beneath
	}
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
	if strings.HasPrefix(rest, "#") {
		rest = "" // ": # comment": the value node is the indented block beneath
	}
	if rest == "" {
		l.pos++
		if next := l.peek(); next != nil && (next.indent > indent || (next.indent == indent && isSeqEntry(next.content))) {
			return l.parseNode(indent + 1)
		}
		return nil
	}
	l.lines[l.pos].content = rest
	if isSeqEntry(rest) {
		// A compact block sequence introduced inline (": - one") continues with its
		// wrapped entries aligned under the "-" column, not the key indent+1; use that
		// real column so a following "- two" at the same column joins the sequence.
		l.lines[l.pos].indent = inlineNodeIndent(ln, ":")
		return l.parseNode(l.lines[l.pos].indent)
	}
	l.lines[l.pos].indent = indent + 1
	return l.parseNode(indent + 1)
}

// inlineNodeIndent returns the column at which the inline node text (the part after
// `marker` and its trailing whitespace) begins within the marker line — the real
// indentation a compact block collection folded onto that line occupies.
func inlineNodeIndent(ln line, marker string) int {
	after := strings.TrimPrefix(ln.content, marker)
	return ln.indent + len(marker) + (len(after) - len(strings.TrimLeft(after, " \t")))
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

// gatherFlow assembles a complete flow collection that may span several physical
// lines. first is the flow-start line's content (beginning with '[' or '{') and
// the cursor is already positioned on the following line; gatherFlow consumes
// continuation lines until the brackets balance, joining them with a space, and
// returns the single-line flow string that parseFlowSeq / parseFlowMap then parse.
//
// It is where flow input is validated: an unbalanced collection (running off the
// end of the document), a stray closing bracket, junk after the closing bracket,
// or a bare '#' that is not a whitespace-preceded comment all reject the document
// with a *SyntaxError instead of being silently mis-parsed.
func (l *loader) gatherFlow(first string, minIndent int) (string, bool) {
	var out strings.Builder
	depth := 0
	var quote byte
	line := first
	for {
		i := 0
		for i < len(line) {
			c := line[i]
			if quote != 0 {
				out.WriteByte(c)
				if c == quote {
					if quote == '\'' && i+1 < len(line) && line[i+1] == '\'' {
						out.WriteByte('\'') // '' is an escaped quote, not a close
						i += 2
						continue
					}
					quote = 0
				}
				i++
				continue
			}
			switch c {
			case '\'', '"':
				quote = c
			case '[', '{':
				depth++
			case ']', '}':
				depth--
				if depth == 0 {
					out.WriteByte(c)
					return out.String(), l.checkFlowTrailer(line[i+1:])
				}
			case '#':
				if i == 0 || line[i-1] == ' ' || line[i-1] == '\t' {
					i = len(line) // a whitespace-preceded '#' comments out the rest
					continue
				}
			}
			out.WriteByte(c)
			i++
		}
		l.skipBlanks()
		if l.pos >= len(l.lines) {
			l.fail("unterminated flow collection")
		}
		if l.lines[l.pos].indent < minIndent {
			// A wrapped flow line dedented below its node's block context is not a
			// continuation but an indentation error (e.g. a flow value pulled back to
			// its own mapping key's column).
			l.fail("insufficient indentation in multi-line flow collection")
		}
		out.WriteByte(' ') // fold the physical line break into a space
		line = l.lines[l.pos].content
		l.pos++
	}
}

// checkFlowTrailer validates what follows a flow collection's closing bracket on
// the same line and reports whether the flow is a mapping KEY. Trailing whitespace
// and a whitespace-preceded "# comment" are fine; a ": " (or bare ":") marks the
// flow as a mapping key (isKey=true) whose ": value" the caller leaves for the
// block parser. Anything else is junk and rejects the document.
func (l *loader) checkFlowTrailer(after string) (isKey bool) {
	trimmed := strings.TrimLeft(after, " \t")
	switch {
	case trimmed == "":
	case trimmed[0] == '#' && len(after) > len(trimmed):
		// a real comment: at least one space separated it from the bracket
	case trimmed == ":" || strings.HasPrefix(trimmed, ": "):
		return true
	default:
		l.fail("trailing content after flow collection")
	}
	return false
}

// parseFlowSeq parses a single-line flow sequence "[a, b, c]". A lone '[' with no
// body is rejected rather than sliced blindly (the slice would overrun).
func (l *loader) parseFlowSeq(s string) Value {
	if len(s) < 2 {
		l.fail("unterminated flow sequence")
	}
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

// parseFlowMap parses a single-line flow mapping "{a: 1, b: 2}". A lone '{' with
// no body is rejected rather than sliced blindly.
func (l *loader) parseFlowMap(s string) Value {
	if len(s) < 2 {
		l.fail("unterminated flow mapping")
	}
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
		if isPlainFlowScalar(v) && mapColon(v) >= 0 {
			// A plain value that still holds a "key: " separator means two entries
			// ran together without the comma between them.
			l.fail("missing ',' between flow-mapping entries")
		}
		h.Set(l.scalarValue(k, ""), l.scalarValue(v, ""))
	}
	return h
}

// isPlainFlowScalar reports whether s is an unquoted, non-collection, non-alias
// flow scalar — the shape whose internals a flow parser may safely scan for a
// stray key/value separator.
func isPlainFlowScalar(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	switch s[0] {
	case '"', '\'', '[', '{', '*', '&', '!':
		return false
	}
	return true
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

// bindAll records v under each anchor name for later *alias references. Multiple
// names arise when a property run spreads several `&anchor` lines over one node;
// the caller only ever collects non-empty names.
func (l *loader) bindAll(anchors []string, v Value) {
	for _, name := range anchors {
		l.anchors[name] = v
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

// isSeqEntry reports whether content is a block-sequence entry — a "-" indicator
// alone or followed by whitespace (a space or a tab) then the entry node.
func isSeqEntry(content string) bool {
	return content == "-" || strings.HasPrefix(content, "- ") || strings.HasPrefix(content, "-\t")
}

// isFlowStart reports whether content opens a flow collection.
func isFlowStart(content string) bool {
	return strings.HasPrefix(content, "[") || strings.HasPrefix(content, "{")
}

// splitMapEntry splits "key: value" into its key and (possibly empty) value,
// honouring quoted keys.
func splitMapEntry(content string) (key, value string, ok bool) {
	i := mapColon(content)
	if isFlowStart(content) {
		// A line opening with a flow collection is a mapping entry only when a ": "
		// separator follows the CLOSED collection; topColon skips the flow interior
		// (whose own "k: v" pairs are not the block separator) to find it.
		i = topColon(content)
	}
	if i < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(content[:i])
	value = strings.TrimSpace(content[i+1:])
	return key, value, true
}

// mapColon returns the index of the key/value separator ": " (or a trailing ":").
// A quoted key is skipped as a whole leading span, so a ": " inside it is not
// mistaken for the separator; a bare quote later in the line is an ordinary plain-
// scalar character and is not treated as opening a quoted span.
func mapColon(content string) int {
	for i := skipQuotedPrefix(content); i < len(content); i++ {
		if content[i] == ':' && (i == len(content)-1 || content[i+1] == ' ' || content[i+1] == '\t') {
			return i
		}
	}
	return -1
}

// skipQuotedPrefix returns the index just past a leading single- or double-quoted
// span (honouring "\\"/'\"' and "”" escapes), or 0 when content does not open
// with a quote. An unterminated leading quote consumes the whole string.
func skipQuotedPrefix(content string) int {
	if content == "" || (content[0] != '\'' && content[0] != '"') {
		return 0
	}
	q := content[0]
	for i := 1; i < len(content); i++ {
		if q == '"' && content[i] == '\\' {
			i++
			continue
		}
		if content[i] == q {
			if q == '\'' && i+1 < len(content) && content[i+1] == '\'' {
				i++
				continue
			}
			return i + 1
		}
	}
	return len(content)
}

// topColon is like mapColon but also skips flow-collection interiors: it returns
// the index of a "key: " (or trailing ":") separator that sits at flow depth 0 and
// outside any quote, or -1. It is used to spot a second mapping entry fused into a
// plain inline value without counting the pairs inside a nested flow collection.
func topColon(content string) int {
	depth := 0
	for i := skipQuotedPrefix(content); i < len(content); i++ {
		switch content[i] {
		case '[', '{':
			depth++
		case ']', '}':
			depth--
		case ':':
			if depth == 0 && (i == len(content)-1 || content[i+1] == ' ' || content[i+1] == '\t') {
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

// unquoteDouble decodes a double-quoted YAML scalar. Every escape is validated
// against the YAML 1.1/1.2 double-quoted escape table; an escape indicator outside
// it (e.g. "\." or "\'") is not valid and rejects the document, matching
// Psych/libyaml. A malformed \x / \u / \U hex payload is tolerated leniently (the
// indicator letter is kept), preserving the loader's existing forgiving behaviour.
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
		switch e := body[i]; e {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '0':
			b.WriteByte(0)
		case 'a':
			b.WriteByte(7)
		case 'b':
			b.WriteByte(8)
		case 'v':
			b.WriteByte(0xB)
		case 'f':
			b.WriteByte(0xC)
		case 'e':
			b.WriteByte(0x1B)
		case ' ', '\t':
			b.WriteByte(e) // escaped space / tab is that whitespace, literally
		case '"':
			b.WriteByte('"')
		case '/':
			b.WriteByte('/')
		case '\\':
			b.WriteByte('\\')
		case 'N':
			b.WriteRune(0x85)
		case '_':
			b.WriteRune(0xA0)
		case 'L':
			b.WriteRune(0x2028)
		case 'P':
			b.WriteRune(0x2029)
		case 'x':
			if i+2 < len(body) {
				if v, err := strconv.ParseUint(body[i+1:i+3], 16, 8); err == nil {
					b.WriteByte(byte(v)) // \xNN is an 8-bit byte escape
					i += 2
					continue
				}
			}
			b.WriteByte('x')
		case 'u':
			i = writeRuneEscape(&b, body, i, 4)
		case 'U':
			i = writeRuneEscape(&b, body, i, 8)
		default:
			panic(parseError{msg: "invalid double-quote escape"})
		}
	}
	return b.String()
}

// writeRuneEscape decodes the n-hex-digit payload of a "\u" / "\U" Unicode escape at
// body[i] (i indexes the indicator letter) and writes the resulting rune. A short
// or non-hex payload is left verbatim — the indicator letter is emitted and i is
// unchanged — so malformed hex is tolerated rather than rejected. It returns the
// index of the last consumed byte.
func writeRuneEscape(b *strings.Builder, body string, i, n int) int {
	if i+n < len(body) {
		if v, err := strconv.ParseUint(body[i+1:i+1+n], 16, 32); err == nil {
			b.WriteRune(rune(v))
			return i + n
		}
	}
	b.WriteByte(body[i])
	return i
}

// Copyright (c) the go-ruby-yaml/yaml authors
//
// SPDX-License-Identifier: BSD-3-Clause

package yaml

import (
	"math"
	"math/big"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// rubyBin locates a usable `ruby` once. The oracle tests skip themselves when it
// is absent (the qemu cross-arch lanes and the Windows lane), so the deterministic
// suite alone drives the 100% gate there.
func rubyBin(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("ruby")
	if err != nil {
		t.Skip("ruby not on PATH; skipping MRI oracle")
	}
	return path
}

// rubyEval runs a Ruby script and returns its stdout. The script must $stdout.binmode
// itself so Windows text-mode does not pollute the bytes (the go-ruby-erb lesson);
// every oracle script below does so via the shared preamble.
func rubyEval(t *testing.T, bin, script string) string {
	t.Helper()
	cmd := exec.Command(bin, "-ryaml", "-e", "$stdout.binmode\n"+script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ruby error: %v\nscript:\n%s\noutput:\n%s", err, script, out)
	}
	return string(out)
}

// TestOracleDumpParsesInMRI dumps a corpus here and checks MRI's Psych accepts the
// document (Psych.parse succeeds) and, where the value has a defined class, that
// YAML.unsafe_load(dump) reproduces it. The corpus spans every supported shape.
func TestOracleDumpParsesInMRI(t *testing.T) {
	bin := rubyBin(t)
	bi, _ := new(big.Int).SetString("123456789012345678901234567890", 10)

	// Each case is dumped here, then handed to MRI as a heredoc; MRI parses it (so
	// the output is valid YAML) and prints its inspected value, which we assert.
	cases := []struct {
		name      string
		v         Value
		mriInspct string // expected Ruby `p` of YAML.unsafe_load(<dump>)
		preamble  string // optional Ruby class definitions
	}{
		{name: "nil", v: nil, mriInspct: "nil"},
		{name: "true", v: true, mriInspct: "true"},
		{name: "int", v: 42, mriInspct: "42"},
		{name: "negint", v: -7, mriInspct: "-7"},
		{name: "bignum", v: bi, mriInspct: "123456789012345678901234567890"},
		{name: "float", v: 3.14, mriInspct: "3.14"},
		{name: "intfloat", v: 2.0, mriInspct: "2.0"},
		{name: "inf", v: math.Inf(1), mriInspct: "Infinity"},
		{name: "string", v: "hello", mriInspct: `"hello"`},
		{name: "empty", v: "", mriInspct: `""`},
		{name: "tricky", v: "true", mriInspct: `"true"`},
		{name: "numlike", v: "123", mriInspct: `"123"`},
		{name: "multiline", v: "a\nb\nc", mriInspct: `"a\nb\nc"`},
		{name: "multilineNL", v: "a\nb\n", mriInspct: `"a\nb\n"`},
		{name: "symbol", v: Symbol("checked"), mriInspct: ":checked"},
		{name: "seq", v: []any{1, 2, 3}, mriInspct: "[1, 2, 3]"},
		{name: "nestedseq", v: []any{[]any{1}, []any{2}}, mriInspct: "[[1], [2]]"},
		{name: "emptyseq", v: []any{}, mriInspct: "[]"},
		{name: "range", v: &Range{Begin: 1, End: 5, Exclusive: false}, mriInspct: "1..5"},
		{name: "exclrange", v: &Range{Begin: 1, End: 5, Exclusive: true}, mriInspct: "1...5"},
		{name: "class", v: Class("String"), mriInspct: "String"},
		{
			name:      "time",
			v:         time.Date(2026, 6, 29, 5, 18, 32, 0, time.UTC),
			mriInspct: "2026-06-29 05:18:32 UTC",
		},
		{
			name:      "object",
			v:         &Object{Class: "Pt", IVars: map[string]any{"x": 1, "y": 2}, Order: []string{"x", "y"}},
			mriInspct: `#<Pt:* @x=1, @y=2>`,
			preamble:  "class Pt; def inspect; \"#<Pt:* @x=#{@x}, @y=#{@y}>\"; end; end\n",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := mustDump(t, c.v)
			script := c.preamble +
				"doc = <<'__Y__'\n" + doc + "__Y__\n" +
				"Psych.parse(doc)\n" + // assert it is structurally valid YAML
				"p YAML.unsafe_load(doc)\n"
			got := strings.TrimRight(rubyEval(t, bin, script), "\n")
			if c.name == "object" {
				// The inspect carries a volatile object id; compare prefix/suffix.
				if !strings.HasPrefix(got, "#<Pt:") || !strings.HasSuffix(got, "@x=1, @y=2>") {
					t.Errorf("MRI load(dump) = %q, want a Pt with x=1,y=2", got)
				}
				return
			}
			if got != c.mriInspct {
				t.Errorf("MRI load(dump %s) = %q, want %q\ndoc:\n%s", c.name, got, c.mriInspct, doc)
			}
		})
	}
}

// TestOracleSharedRefParsesInMRI checks a shared / aliased structure dumps to an
// anchor/alias document MRI loads with the sharing intact (same object id).
func TestOracleSharedRefParsesInMRI(t *testing.T) {
	bin := rubyBin(t)
	a := []any{1, 2}
	doc := mustDump(t, []any{a, a})
	script := "v = YAML.load(<<'__Y__', aliases: true)\n" + doc + "__Y__\n" +
		"p v[0].equal?(v[1])\np v\n"
	got := rubyEval(t, bin, script)
	if !strings.Contains(got, "true") || !strings.Contains(got, "[[1, 2], [1, 2]]") {
		t.Errorf("shared ref oracle = %q\ndoc:\n%s", got, doc)
	}
}

// TestOracleRoundTripViaMRIDump checks the reverse direction: MRI dumps a value,
// this package's Load parses it, and the loaded shape matches what we expect — so
// the loader accepts genuine Psych output, not only our own emitter's.
func TestOracleRoundTripViaMRIDump(t *testing.T) {
	bin := rubyBin(t)
	scripts := map[string]func(t *testing.T, v Value){
		`print YAML.dump({"a"=>1, "b"=>[1,2,3], "c"=>{"d"=>true}})`: func(t *testing.T, v Value) {
			m := v.(*Map)
			if bv, _ := m.Get("b"); !eqValue(bv, []any{int64(1), int64(2), int64(3)}) {
				t.Errorf("MRI-dumped b = %#v", bv)
			}
			cv, _ := m.Get("c")
			if dv, _ := cv.(*Map).Get("d"); !eqValue(dv, true) {
				t.Errorf("MRI-dumped c.d = %#v", dv)
			}
		},
		`print YAML.dump({sym: :val, "s" => "multi\nline"})`: func(t *testing.T, v Value) {
			m := v.(*Map)
			if sv, _ := m.Get(Symbol("sym")); !eqValue(sv, Symbol("val")) {
				t.Errorf("MRI-dumped sym = %#v", sv)
			}
			if msv, _ := m.Get("s"); !eqValue(msv, "multi\nline") {
				t.Errorf("MRI-dumped multiline = %#v", msv)
			}
		},
		`print YAML.dump(1..5)`: func(t *testing.T, v Value) {
			r := v.(*Range)
			if !eqValue(r.Begin, int64(1)) || !eqValue(r.End, int64(5)) || r.Exclusive {
				t.Errorf("MRI-dumped range = %#v", r)
			}
		},
		`print YAML.dump(Time.utc(2026,6,29,5,18,32))`: func(t *testing.T, v Value) {
			if _, ok := v.(time.Time); !ok {
				t.Errorf("MRI-dumped time = %#v", v)
			}
		},
		`print YAML.dump(123456789012345678901234567890)`: func(t *testing.T, v Value) {
			if _, ok := v.(*big.Int); !ok {
				t.Errorf("MRI-dumped bignum = %#v", v)
			}
		},
	}
	for script, check := range scripts {
		doc := rubyEval(t, bin, script)
		v, err := Load(doc)
		if err != nil {
			t.Fatalf("Load(MRI %q): %v\ndoc:\n%s", script, err, doc)
		}
		check(t, v)
	}
}

// TestOracleObjectRoundTrip dumps an object with MRI (a defined class), loads it
// here, re-dumps here, and has MRI load it back to the same object — the full
// !ruby/object round trip both directions.
func TestOracleObjectRoundTrip(t *testing.T) {
	bin := rubyBin(t)
	preamble := "class Cfg; attr_accessor :name, :level; def initialize(n,l); @name=n; @level=l; end; " +
		"def ==(o); o.is_a?(Cfg) && o.name==name && o.level==level; end; end\n"
	mriDoc := rubyEval(t, bin, preamble+`print YAML.dump(Cfg.new("web", 3))`)
	v, err := Load(mriDoc)
	if err != nil {
		t.Fatalf("Load object: %v", err)
	}
	o, ok := v.(*Object)
	if !ok || o.Class != "Cfg" || !eqValue(o.IVars["name"], "web") || !eqValue(o.IVars["level"], int64(3)) {
		t.Fatalf("loaded object = %#v", v)
	}
	// Re-dump here and let MRI confirm equality with the original.
	redoc := mustDump(t, o)
	script := preamble +
		"orig = Cfg.new(\"web\", 3)\n" +
		"got = YAML.unsafe_load(<<'__Y__')\n" + redoc + "__Y__\n" +
		"p(orig == got)\n"
	if out := strings.TrimSpace(rubyEval(t, bin, script)); out != "true" {
		t.Errorf("object round-trip equality = %q\nredoc:\n%s", out, redoc)
	}
}

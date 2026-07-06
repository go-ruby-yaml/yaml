<p align="center"><img src="https://raw.githubusercontent.com/go-ruby-yaml/brand/main/social/go-ruby-yaml-yaml.png" alt="go-ruby-yaml/yaml" width="720"></p>

# yaml — go-ruby-yaml

[![Docs](https://img.shields.io/badge/docs-mkdocs--material-DC2626)](https://go-ruby-yaml.github.io/docs/)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-blue)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.26.4%2B-00ADD8)](https://go.dev/dl/)
[![Coverage](https://img.shields.io/badge/coverage-100%25-1a7f37)](#tests--coverage)

**A pure-Go (no cgo) reimplementation of Ruby's [Psych](https://docs.ruby-lang.org/en/master/Psych.html)
YAML emitter and loader** — the deterministic, interpreter-independent core of
MRI 4.0.5's `Psych.dump` / `Psych.load`. It serialises a tree of Ruby values to a
Psych-compatible YAML document and parses one back, so `Load(Dump(x))` round-trips
the structures Ruby programs (and Puppet's `state.yaml` / `last_run_summary.yaml`)
persist — **without any Ruby runtime**.

It is the YAML backend for
[go-embedded-ruby](https://github.com/go-embedded-ruby/ruby), but is a
**standalone, reusable** module with no dependency on the Ruby runtime — a sibling
of [go-ruby-regexp](https://github.com/go-ruby-regexp/regexp) (the Onigmo engine)
and [go-ruby-erb](https://github.com/go-ruby-erb/erb) (the ERB compiler).

> **What it is — and isn't.** Emitting and parsing YAML for the Ruby value model
> (scalar typing, block/flow layout, anchors/aliases, the `!ruby/object:` tag
> grammar) is fully deterministic and needs **no interpreter**, so it lives here
> as pure Go. Binding the documents to live Ruby objects — instantiating a class,
> reading an object's instance variables — is the host's job; this library hands
> back a small, explicit value model (`*Object`, `*Range`, `Symbol`, …) the host
> maps to and from its own objects.

## Features

Faithful port of Psych's emit + load, validated against the `ruby` binary on
every supported platform:

- **Block + flow** mappings and sequences, with Psych's default layout (a block
  sequence under a key aligns its dashes with the key; nested mappings indent two
  deeper; a dash child continues on the dash line).
- **Every scalar style** — plain, single-quoted, double-quoted (with the `\n \t
  \r \0 \xNN \" \\` escapes), and literal (`|` / `|-` / `|+`) and folded (`>`)
  block scalars, indent-correct at any depth.
- **Psych implicit typing** — `null` / `~`, booleans, integers (decimal, `0x`
  hex, `0o` octal, `0b` binary, `_` separators, and big integers), floats
  (`.inf` / `-.inf` / `.nan`, exponents), and ISO-8601 timestamps.
- **Symbols** — the bare `:name` form and the `!ruby/symbol` tag.
- **Anchors & aliases** — a value shared (or cyclic) in the graph is emitted once
  behind `&N` and referenced thereafter with `*N`; the loader resolves them.
- **`!ruby/object:Class`** mappings of instance variables (deterministic key
  order — host-supplied `Order`, else lexicographic), plus `!ruby/range`,
  `!ruby/class` / `!ruby/module`, and `!ruby/regexp`.
- **Complex / nil mapping keys** via the explicit `? key` / `: value` form.
- **Document markers** (`---`, `...`), comments, and `#coding` headers.

CGO-free, dependency-free, **100% test coverage**, `gofmt` + `go vet` clean, and
green across the six 64-bit Go targets (amd64, arm64, riscv64, loong64, ppc64le,
s390x).

## Install

```sh
go get github.com/go-ruby-yaml/yaml
```

## Usage

```go
package main

import (
	"fmt"

	"github.com/go-ruby-yaml/yaml"
)

func main() {
	// Build a Ruby value tree from the package's value model.
	m := yaml.NewMap()
	m.Set(yaml.Symbol("checked"), true)
	m.Set("hosts", []any{"web", "db"})
	m.Set("range", &yaml.Range{Begin: 1, End: 5})

	doc, _ := yaml.Dump(m) // Psych.dump
	fmt.Print(doc)
	// ---
	// :checked: true
	// hosts:
	// - web
	// - db
	// range: !ruby/range
	//   begin: 1
	//   end: 5
	//   excl: false

	v, _ := yaml.Load(doc) // Psych.load — mappings come back as *yaml.Map
	fmt.Printf("%T\n", v)  // *yaml.Map
}
```

## Ruby value model

YAML round-trips an `any` drawn from a small, fixed set of Go types, so a host can
map its own object graph to and from this package:

| Ruby             | Go (Dump accepts)                | Go (Load returns)   |
| ---------------- | -------------------------------- | ------------------- |
| `nil`            | `nil`                            | `nil`               |
| `true` / `false` | `bool`                           | `bool`              |
| `Integer`        | `int`, `int64`, `*big.Int`       | `int64` / `*big.Int`|
| `Float`          | `float64`, `float32`             | `float64`           |
| `String`         | `string`                         | `string`            |
| `Symbol`         | `yaml.Symbol`                    | `yaml.Symbol`       |
| `Array`          | `[]any`                          | `[]any`             |
| `Hash`           | `*yaml.Map`, `map[string]any`, `map[Symbol]any` | `*yaml.Map` (ordered) |
| `Time`           | `time.Time`                      | `time.Time`         |
| `Range`          | `*yaml.Range`                    | `*yaml.Range`       |
| object           | `*yaml.Object`                   | `*yaml.Object`      |
| `Class`/`Module` | `yaml.Class` / `yaml.Module`     | `yaml.Class` / `yaml.Module` |
| `Regexp`         | `*yaml.Regexp`                   | `*yaml.Regexp`      |

A plain Go map is emitted in sorted-key order; a `*yaml.Map` preserves insertion
order, and `Load` always returns mappings as `*yaml.Map` so key order round-trips.

## API

```go
// Dump serialises a Ruby value to a Psych-compatible document (Psych.dump /
// Object#to_yaml). A value outside the model returns an error.
func Dump(v any, opts ...Option) (string, error)

// Load parses a Psych-compatible document (Psych.load / YAML.load).
func Load(s string) (any, error)

// SafeLoad parses like Load but honours a permitted-class allow-list; this loader
// never evaluates code, so it is safe by construction.
func SafeLoad(s string, opts ...Option) (any, error)

func WithPermittedClasses(names ...string) Option // Psych permitted_classes:
func WithAliases(allowed bool) Option             // Psych aliases:

type Symbol string
type Class  string
type Module string
type Regexp struct { Source, Flags string }
type Range  struct { Begin, End any; Exclusive bool }
type Object struct { Class string; IVars map[string]any; Order []string }
type Map    struct { /* insertion-ordered Hash */ }
func NewMap() *Map
func (m *Map) Set(key, val any)
func (m *Map) Get(key any) (any, bool)
func (m *Map) Pairs() []Pair
func (m *Map) Len() int
```

## Tests & coverage

The suite pairs deterministic, ruby-free tests (which alone hold coverage at
100%, so the qemu cross-arch and Windows lanes pass the gate) with a **differential
MRI oracle**: a wide corpus is dumped here and parsed by the system `ruby`
(`Psych.parse` + `YAML.unsafe_load`), and MRI-dumped documents are loaded here —
round-tripping anchors, `!ruby/object`, `Time`, big integers, and ranges in both
directions. The oracle scripts `$stdout.binmode` so Windows text-mode never
pollutes the bytes, and skip themselves where `ruby` is absent.

```sh
COVERPKG=$(go list ./... | paste -sd, -)
go test -race -coverpkg="$COVERPKG" -coverprofile=cover.out ./...
go tool cover -func=cover.out | tail -1   # 100.0%
```

## License

BSD-3-Clause — see [LICENSE](LICENSE). Copyright the go-ruby-yaml/yaml authors.

## WebAssembly

Being pure Go (CGO=0), this library also compiles to **WebAssembly** — both
`GOOS=js GOARCH=wasm` (browser / Node.js) and `GOOS=wasip1 GOARCH=wasm` (WASI).
CI builds both targets on every push, alongside the six 64-bit native/qemu arches.

```sh
GOOS=js     GOARCH=wasm go build ./...   # browser / Node
GOOS=wasip1 GOARCH=wasm go build ./...   # WASI (wasmtime, wasmer, wasmedge, …)
```

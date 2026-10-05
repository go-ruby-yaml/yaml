// Copyright (c) the go-ruby-yaml/yaml authors
//
// SPDX-License-Identifier: BSD-3-Clause

package yaml

import "time"

// Option configures Dump / SafeLoad, mirroring the keyword options of
// Psych.dump / Psych.safe_load.
type Option func(*options)

// options holds the resolved configuration for an operation.
type options struct {
	// permitted is the set of class names SafeLoad may materialise. It is ALWAYS
	// non-nil for a SafeLoad -- there is no "no policy" state -- and empty means
	// exactly what it says: permit nothing.
	//
	// It used to be a []string with the guard `if o.permittedClasses != nil`, and
	// WithPermittedClasses appended to it. Appending ZERO names left the slice
	// nil, so WithPermittedClasses() -- "deny everything", the most restrictive
	// request the API can express -- arrived at the same nil as "no option
	// passed" and took the no-policy branch, permitting every class. Making
	// SafeLoad always restrict deletes that state rather than merely making it
	// distinguishable: Load is the unrestricted entry point.
	permitted map[string]bool
	// permittedSymbols narrows WHICH Symbol names may be interned. nil means no
	// narrowing. Unlike permitted, an EMPTY set here also means no narrowing,
	// matching Psych::ClassLoader::Restricted#symbolize, which short-circuits on
	// `@symbols.empty?` (psych/class_loader.rb:84-92). Narrowing never substitutes
	// for the class check: "Symbol" must be permitted as well.
	permittedSymbols map[string]bool
	// aliasesAllowed mirrors Psych.safe_load's aliases:. SafeLoad refuses a
	// document that dereferences an alias unless it is set, as Psych does.
	aliasesAllowed bool
}

// WithPermittedClasses adds names to SafeLoad's allow-list (Psych's
// permitted_classes:). Calling it with no names is meaningful and is NOT the
// same as not calling it: SafeLoad restricts either way, so both deny every
// class. Repeated calls accumulate.
func WithPermittedClasses(names ...string) Option {
	return func(o *options) {
		// No nil check: resolve establishes the non-nil allow-list before any
		// Option runs, and it is the only thing that builds an options value. The
		// 100% coverage gate named a nil branch here as unreachable, which it was
		// -- and an unreachable "just in case" is how the nil crept back in last
		// time.
		for _, n := range names {
			o.permitted[n] = true
		}
	}
}

// WithPermittedSymbols narrows which Symbol names SafeLoad may intern (Psych's
// permitted_symbols:). With no names it narrows nothing, as Psych's empty
// permitted_symbols does. It is additional to the class check: a document's
// symbols load only when "Symbol" is also permitted.
func WithPermittedSymbols(names ...string) Option {
	return func(o *options) {
		if len(names) == 0 {
			return
		}
		if o.permittedSymbols == nil {
			o.permittedSymbols = map[string]bool{}
		}
		for _, n := range names {
			o.permittedSymbols[n] = true
		}
	}
}

// WithAliases mirrors Psych.safe_load(aliases: true), permitting a document to
// dereference `*alias` references. SafeLoad refuses them by default.
func WithAliases(allowed bool) Option {
	return func(o *options) { o.aliasesAllowed = allowed }
}

// resolve applies opts to a fresh options value whose allow-list is the empty
// (deny-everything) policy, never a nil one. It is the ONLY constructor of an
// options value, which is what lets WithPermittedClasses write to the map
// without a nil check.
func resolve(opts []Option) options {
	o := options{permitted: map[string]bool{}}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// DisallowedClassError is returned by SafeLoad for a class the document names
// and the options do not permit. Its message is Psych::DisallowedClass's
// (psych/exception.rb:23-27), so a host binding can surface it verbatim.
//
// SafeLoad used to REWRITE an unpermitted object into its bare ivar mapping
// instead of reporting it. A caller that asked for a refusal got a Map, which
// is type confusion in place of an error -- and silent, because the document
// had loaded "successfully".
type DisallowedClassError struct {
	// Name is the class the document asked for.
	Name string
}

func (e *DisallowedClassError) Error() string {
	return "Tried to load unspecified class: " + e.Name
}

// AliasesNotEnabledError is returned by SafeLoad for a document that
// dereferences an alias without WithAliases(true), matching
// Psych::AliasesNotEnabled.
type AliasesNotEnabledError struct{}

func (e *AliasesNotEnabledError) Error() string {
	return "Alias parsing was not enabled. To enable it, pass `aliases: true` to `Psych::load` or `Psych::safe_load`."
}

// AnchorNotDefinedError is returned for an alias naming an anchor the document
// never defines, matching Psych::AnchorNotDefined. The loader resolves such an
// alias to nil, which silently turns a broken reference into a null value.
type AnchorNotDefinedError struct {
	// Anchor is the name the alias referenced.
	Anchor string
}

func (e *AnchorNotDefinedError) Error() string {
	return "An alias referenced an unknown anchor: " + e.Anchor
}

// Dump serialises a Ruby value to a Psych-compatible YAML document string,
// matching Psych.dump / Object#to_yaml. v is drawn from the package value model
// (see the package doc); a value outside that model returns an error rather than
// panicking. The opts are accepted for Psych parity and do not affect emission.
func Dump(v Value, opts ...Option) (string, error) {
	_ = resolve(opts)
	return dump(v)
}

// Load parses a Psych-compatible YAML document into a Ruby value, matching
// Psych.load / YAML.load. Mappings load as an ordered *Map (key order preserved),
// sequences as []any, and the Psych tags into the package's Symbol / Object /
// Range / Class / Module / Regexp / time.Time shapes. A blank document loads as
// nil.
//
// Load applies NO allow-list: it is Psych.unsafe_load's counterpart and will
// materialise any class a document names. Use SafeLoad for a document you did
// not write.
func Load(s string) (Value, error) {
	return load(s)
}

// SafeLoad parses like Load and then enforces Psych.safe_load's restriction,
// returning a *DisallowedClassError for the first class the document names that
// opts do not permit.
//
// SafeLoad ALWAYS restricts. With no options it permits no class at all, which
// is Psych.safe_load's own default (its signature is permitted_classes: [], and
// Psych::ClassLoader::Restricted seeds its allow-list from that list alone), and
// it is why this function has no "no policy" state to get wrong.
//
// The restriction is wider than `!ruby/object:` tags, because Psych's class
// loader is: Symbol (in values and in keys), time.Time, Range, Regexp, and the
// class NAMED by a `!ruby/class` / `!ruby/module` tag all have to be permitted
// too. Plain scalars and the Map / sequence containers never consult it, which
// is precisely why psych.rb can document String / Integer / Array / Hash and
// friends as permitted by default while Restricted holds none of their names.
func SafeLoad(s string, opts ...Option) (Value, error) {
	o := resolve(opts)
	v, ar, err := loadReport(s)
	if err != nil {
		return nil, err
	}
	if ar.undefined != "" {
		return nil, &AnchorNotDefinedError{Anchor: ar.undefined}
	}
	if ar.used && !o.aliasesAllowed {
		return nil, &AliasesNotEnabledError{}
	}
	if err := (&permitWalk{opts: o, seen: map[Value]bool{}}).check(v); err != nil {
		return nil, err
	}
	return v, nil
}

// permitWalk checks a loaded document against an allow-list, reporting the
// FIRST class it may not materialise. seen holds the pointer-shaped nodes
// already cleared, so a node reached twice through an alias is walked once (and
// a shared graph cannot send the walk round in circles).
type permitWalk struct {
	opts options
	seen map[Value]bool
}

// check enforces the restriction on one value and its children, in document
// order: Psych resolves a mapping's class before reviving its members
// (to_ruby.rb:244) and accepts a pair's key before its value, so the error names
// the outermost, earliest offender -- which is the one a caller can act on.
func (w *permitWalk) check(v Value) error {
	switch n := v.(type) {
	case Symbol:
		return w.checkSymbol(string(n))
	case time.Time:
		return w.checkClass("Time")
	case Class:
		// `!ruby/class 'String'` is gated on the NAMED class, not on "Class":
		// to_ruby.rb:98 hands the scalar's own value to resolve_class.
		return w.checkClass(string(n))
	case Module:
		return w.checkClass(string(n))
	case *Regexp:
		return w.checkClass("Regexp")
	case []any:
		for _, el := range n {
			if err := w.check(el); err != nil {
				return err
			}
		}
	case *Map:
		if w.mark(n) {
			for i := range n.pairs {
				if err := w.check(n.pairs[i].Key); err != nil {
					return err
				}
				if err := w.check(n.pairs[i].Val); err != nil {
					return err
				}
			}
		}
	case *Range:
		if w.mark(n) {
			if err := w.checkClass("Range"); err != nil {
				return err
			}
			if err := w.check(n.Begin); err != nil {
				return err
			}
			return w.check(n.End)
		}
	case *Object:
		if w.mark(n) {
			return w.checkObject(n)
		}
	}
	return nil
}

// checkObject gates an object on its class name, then walks its ivars in
// emission order -- the order matters because it decides WHICH offender the
// error names, and an unordered walk of the IVars map would make that answer
// depend on Go's randomised map iteration.
func (w *permitWalk) checkObject(o *Object) error {
	name := o.Class
	if name == "" {
		name = "Object"
	}
	if err := w.checkClass(name); err != nil {
		return err
	}
	for _, k := range o.orderedIVarKeys() {
		if err := w.check(o.IVars[k]); err != nil {
			return err
		}
	}
	return nil
}

// checkClass reports a DisallowedClassError unless name is permitted.
func (w *permitWalk) checkClass(name string) error {
	if w.opts.permitted[name] {
		return nil
	}
	return &DisallowedClassError{Name: name}
}

// checkSymbol applies Psych's two-part symbol rule: the name must pass the
// permitted-symbols narrowing when one is in force, and "Symbol" must itself be
// permitted. Either refusal names "Symbol", as both Restricted#symbolize and
// Restricted#find do.
func (w *permitWalk) checkSymbol(name string) error {
	if w.opts.permittedSymbols != nil && !w.opts.permittedSymbols[name] {
		return &DisallowedClassError{Name: "Symbol"}
	}
	return w.checkClass("Symbol")
}

// mark records a pointer-shaped node as visited, reporting whether this is its
// first visit. Only pointer shapes are recorded: a []any is not comparable and
// would panic as a map key.
func (w *permitWalk) mark(v Value) bool {
	if w.seen[v] {
		return false
	}
	w.seen[v] = true
	return true
}

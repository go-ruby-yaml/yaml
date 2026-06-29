// Copyright (c) the go-ruby-yaml/yaml authors
//
// SPDX-License-Identifier: BSD-3-Clause

package yaml

// Option configures Dump / SafeLoad. Options are accepted for parity with
// Psych.dump / Psych.safe_load (which take keyword options); this loader is safe
// by construction — it instantiates only the *Object shapes named by the
// `!ruby/object:` tags actually present and never evaluates anything — so the
// options are tolerated and currently advisory. They exist so callers (and a
// host binding) can pass Psych-style configuration without breaking.
type Option func(*options)

// options holds the resolved configuration for an operation.
type options struct {
	// permittedClasses, when non-nil, restricts which `!ruby/object:` class names
	// SafeLoad will materialise into an *Object; an unpermitted tag yields the bare
	// mapping instead. A nil slice permits all (the default).
	permittedClasses []string
	// aliasesAllowed mirrors Psych.safe_load's aliases: keyword. Aliases are always
	// resolved by this loader; the flag is retained for API parity.
	aliasesAllowed bool
}

// WithPermittedClasses restricts SafeLoad to materialise `!ruby/object:` tags only
// for the named classes (Psych's permitted_classes:). Other tagged mappings load
// as their plain ordered mapping.
func WithPermittedClasses(names ...string) Option {
	return func(o *options) { o.permittedClasses = append(o.permittedClasses, names...) }
}

// WithAliases mirrors Psych.safe_load(aliases: true). Aliases are resolved
// regardless; the option is accepted for parity.
func WithAliases(allowed bool) Option {
	return func(o *options) { o.aliasesAllowed = allowed }
}

// resolve applies opts to a fresh options value.
func resolve(opts []Option) options {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	return o
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
func Load(s string) (Value, error) {
	return load(s)
}

// SafeLoad parses like Load but honours the safe-load options (permitted classes).
// This loader is already safe by construction — it never evaluates code and only
// builds the *Object shapes whose tags appear — so SafeLoad and Load differ only
// in the optional class allow-list.
func SafeLoad(s string, opts ...Option) (Value, error) {
	o := resolve(opts)
	v, err := load(s)
	if err != nil {
		return nil, err
	}
	if o.permittedClasses != nil {
		v = restrictClasses(v, o.permittedClasses)
	}
	return v, nil
}

// restrictClasses walks v and replaces any *Object whose Class is not in permitted
// with its plain ordered mapping of ivars (Symbol keys), so SafeLoad with a class
// allow-list does not surface unpermitted object types.
func restrictClasses(v Value, permitted []string) Value {
	allow := map[string]bool{}
	for _, n := range permitted {
		allow[n] = true
	}
	return restrict(v, allow)
}

// restrict is the recursive worker for restrictClasses.
func restrict(v Value, allow map[string]bool) Value {
	switch n := v.(type) {
	case []any:
		for i := range n {
			n[i] = restrict(n[i], allow)
		}
		return n
	case *Map:
		for i := range n.pairs {
			n.pairs[i].Val = restrict(n.pairs[i].Val, allow)
		}
		return n
	case *Object:
		for k := range n.IVars {
			n.IVars[k] = restrict(n.IVars[k], allow)
		}
		if allow[n.Class] {
			return n
		}
		m := NewMap()
		for _, k := range n.orderedIVarKeys() {
			m.Set(Symbol(k), n.IVars[k])
		}
		return m
	case *Range:
		n.Begin = restrict(n.Begin, allow)
		n.End = restrict(n.End, allow)
		return n
	}
	return v
}

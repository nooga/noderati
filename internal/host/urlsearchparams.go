package host

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// urlSearchParams backs the WHATWG `URLSearchParams` class real code
// constructs directly (`new URLSearchParams(...)`), not just reads off a
// `URL` instance's `.searchParams` (`url.go`'s `jsURL` doesn't expose that
// either — same "add it when something does" note applies).
//
// Found missing via noderati's `pi-ai`/`pi-agent-core` group-B fakes: once
// #198/#199 (paserati) cleared their real blockers, `pi-coding-agent`'s
// real `-p` invocation got one step further and hit `URLSearchParams is
// not defined` outright — `typeof URLSearchParams` was `undefined`
// globally *and* `node:url` didn't export it either. Real usage: the
// Anthropic SDK's own `client.js` does `body instanceof URLSearchParams`
// (needs the identity to exist, not full behavior); pi-ai's OAuth device-
// and authorization-code flows (`utils/oauth/{anthropic,openai-codex,
// github-copilot}.js`) construct one from a plain object to build a
// `application/x-www-form-urlencoded` request body, and separately parse
// one from a raw query string (`new URLSearchParams(location.search)`)
// to pull `code`/`state` back out with `.get()`.
//
// Backed by an ordered `[][2]string` rather than Go's `net/url.Values`
// (a map) on purpose: WHATWG `URLSearchParams` preserves insertion order,
// including duplicate names, for iteration and `.toString()` — a map
// would silently reorder pairs. `url.QueryEscape`/`url.QueryUnescape`
// are still reused for the actual percent-encoding (their "+" for space
// is exactly `application/x-www-form-urlencoded`'s rule, not
// `encodeURIComponent`'s "%20").
//
// Scoped to what real code above actually exercises: construction from a
// query string, a plain object, or an array of `[name, value]` pairs;
// `append`/`delete`/`get`/`getAll`/`has`/`set`/`sort`/`toString`, plus
// (installURLSearchParamsIteration, below) `[Symbol.iterator]`/
// `entries`/`keys`/`values`/`forEach`/`.size` - found missing the hard
// way chasing the real Bedrock investigation
// (docs/real-node-plan.md, round 99): real, unmodified `@smithy/core`'s
// own `dist-cjs/submodules/protocols/index.js` does
// `for (const [key, value] of new URLSearchParams(search))` at a real
// request-serialization call site, not a hypothetical one. Still no
// `new URLSearchParams(existingInstance)` copy form - it would need to
// distinguish "another URLSearchParams instance" from "a plain object
// that happens to have the same shape," which nothing here needs yet.
// Add real support once something does, same as `url.go`'s own
// documented gap.
type urlSearchParams struct {
	pairs [][2]string
}

func newURLSearchParams(init vm.Value) (*urlSearchParams, error) {
	u := &urlSearchParams{}
	if init.IsUndefined() || init.IsNull() {
		return u, nil
	}
	if init.IsArray() {
		arr := init.AsArray()
		for i := 0; i < arr.Length(); i++ {
			entry := arr.Get(i)
			if !entry.IsArray() {
				return nil, fmt.Errorf("Failed to construct 'URLSearchParams': parameter 1 sequence's element does not contain exactly two elements")
			}
			pair := entry.AsArray()
			if pair.Length() != 2 {
				return nil, fmt.Errorf("Failed to construct 'URLSearchParams': parameter 1 sequence's element does not contain exactly two elements")
			}
			u.pairs = append(u.pairs, [2]string{pair.Get(0).ToString(), pair.Get(1).ToString()})
		}
		return u, nil
	}
	if init.IsString() {
		u.parseQueryString(strings.TrimPrefix(init.AsString(), "?"))
		return u, nil
	}
	if init.IsObject() {
		if obj := init.AsPlainObject(); obj != nil {
			for _, name := range obj.OwnPropertyNames() {
				val, ok := obj.GetOwn(name)
				if !ok || val.IsCallable() {
					continue
				}
				u.pairs = append(u.pairs, [2]string{name, val.ToString()})
			}
			return u, nil
		}
	}
	// Anything else (a number, boolean, etc.) stringifies per spec same as
	// the query-string form.
	u.parseQueryString(strings.TrimPrefix(init.ToString(), "?"))
	return u, nil
}

// parseQueryString splits on "&" and "=" itself rather than using Go's
// net/url.ParseQuery, which returns an unordered map — order (including
// which of several same-named pairs comes first) is spec-observable via
// .get()/.toString().
func (u *urlSearchParams) parseQueryString(qs string) {
	if qs == "" {
		return
	}
	for _, part := range strings.Split(qs, "&") {
		if part == "" {
			continue
		}
		name, value, _ := strings.Cut(part, "=")
		decodedName, err := url.QueryUnescape(name)
		if err != nil {
			decodedName = name
		}
		decodedValue, err := url.QueryUnescape(value)
		if err != nil {
			decodedValue = value
		}
		u.pairs = append(u.pairs, [2]string{decodedName, decodedValue})
	}
}

func (u *urlSearchParams) Append(name, value string) {
	u.pairs = append(u.pairs, [2]string{name, value})
}

func (u *urlSearchParams) Delete(name string) {
	kept := u.pairs[:0]
	for _, p := range u.pairs {
		if p[0] != name {
			kept = append(kept, p)
		}
	}
	u.pairs = kept
}

func (u *urlSearchParams) Get(name string) vm.Value {
	for _, p := range u.pairs {
		if p[0] == name {
			return vm.NewString(p[1])
		}
	}
	return vm.Null
}

func (u *urlSearchParams) GetAll(name string) []string {
	var values []string
	for _, p := range u.pairs {
		if p[0] == name {
			values = append(values, p[1])
		}
	}
	return values
}

func (u *urlSearchParams) Has(name string) bool {
	for _, p := range u.pairs {
		if p[0] == name {
			return true
		}
	}
	return false
}

// Set replaces the first pair named name with value and removes any
// other pairs with that name, or appends a new pair if none existed —
// matching the WHATWG algorithm exactly (not just "delete then append",
// which would move the pair to the end instead of preserving its
// original position).
func (u *urlSearchParams) Set(name, value string) {
	found := false
	kept := u.pairs[:0]
	for _, p := range u.pairs {
		if p[0] != name {
			kept = append(kept, p)
			continue
		}
		if !found {
			kept = append(kept, [2]string{name, value})
			found = true
		}
	}
	u.pairs = kept
	if !found {
		u.pairs = append(u.pairs, [2]string{name, value})
	}
}

// Sort reorders pairs by name using a stable sort on UTF-16 code unit
// order, matching the spec's requirement to preserve relative order
// between pairs sharing a name.
func (u *urlSearchParams) Sort() {
	sort.SliceStable(u.pairs, func(i, j int) bool {
		return u.pairs[i][0] < u.pairs[j][0]
	})
}

func (u *urlSearchParams) ToString() string {
	parts := make([]string, len(u.pairs))
	for i, p := range u.pairs {
		parts[i] = formURLEncode(p[0]) + "=" + formURLEncode(p[1])
	}
	return strings.Join(parts, "&")
}

// RawPairs exposes every [name, value] pair in insertion order - bound
// automatically as `.rawPairs()` by `ModuleBuilder.Class`'s reflection,
// same as every other method here. Not itself a real Node method (real
// URLSearchParams has no such name); it exists purely so
// installURLSearchParamsIteration (below) can get at a specific
// instance's own pairs from a shared prototype-level function, which
// `ModuleBuilder.Class`'s per-instance method binding has no other way
// to do - see that function's own doc comment for the full story.
func (u *urlSearchParams) RawPairs() [][]string {
	pairs := make([][]string, len(u.pairs))
	for i, p := range u.pairs {
		pairs[i] = []string{p[0], p[1]}
	}
	return pairs
}

// formURLEncode percent-encodes s per the WHATWG URL Standard's
// application/x-www-form-urlencoded serializer, NOT Go's url.QueryEscape —
// the two disagree on which bytes are "unreserved" and shipping the wrong
// one means silently wrong bytes on the wire for real request bodies (see
// the package doc comment: pi-ai's OAuth flows build exactly this kind of
// body). The spec's unreserved set is ASCII alphanumeric plus `*`, `-`,
// `.`, `_`; space becomes `+`; everything else is percent-encoded UTF-8
// bytes with uppercase hex. Go's QueryEscape instead treats `~` as
// unreserved and `*` as reserved — the opposite of the spec on both —
// confirmed by diffing against real Node's URLSearchParams output on
// OAuth-shaped values (colons, tildes, stars, parens, bangs, quotes).
func formURLEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '*' || c == '-' || c == '.' || c == '_':
			b.WriteByte(c)
		case c == ' ':
			b.WriteByte('+')
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// installURLSearchParamsIteration adds `[Symbol.iterator]`/`entries`/
// `keys`/`values`/`forEach`/`.size` to `URLSearchParams.prototype` -
// found missing entirely, the hard way, chasing the real Bedrock
// investigation (docs/real-node-plan.md, round 99): real, unmodified
// `@smithy/core`'s own `dist-cjs/submodules/protocols/index.js` does
// `for (const [key, value] of new URLSearchParams(search))` at a real
// request-serialization call site.
//
// Why this can't be built the same way `Get`/`Set`/`Append`/etc. are:
// `ModuleBuilder.Class` (paserati's `pkg/driver`) binds every Go method
// as an *instance*-owned property, each one a closure created fresh at
// construction time over that specific instance's own Go struct - there
// is no shared prototype-level equivalent, and no generic way from
// outside that mechanism to recover a `*urlSearchParams` back out of an
// arbitrary `this` value. `Symbol.iterator` (and friends) genuinely
// need to live on the shared prototype, though - real code does
// `Object.getPrototypeOf(params)[Symbol.iterator]`-shaped checks, and a
// per-instance own-property version would also incorrectly show up in
// `for...in`/`Object.keys()`.
//
// The way around it: `RawPairs()` (urlsearchparams.go, just above) is
// itself one of these ordinary per-instance-bound methods, so it's
// already reachable from JS as `params.rawPairs()`. Each function below
// uses `vmInst.GetThis()` - the same mechanism paserati's own native
// function call path already threads through for exactly this reason
// (see pkg/vm/call.go's `vm.currentThis`) - to find out *which*
// instance is being iterated, then calls that instance's own
// `rawPairs()` through a real `vmInst.Call(...)`, and builds the
// iterator/behavior from the result. No paserati changes needed.
func installURLSearchParamsIteration(p *driver.Paserati) {
	vmInst := p.GetVM()
	if vmInst == nil {
		return
	}
	ctor, ok := vmInst.GetGlobal("URLSearchParams")
	if !ok {
		return
	}
	ctorProps := ctor.AsNativeFunctionWithProps()
	if ctorProps == nil || ctorProps.Properties == nil {
		return
	}
	protoVal, ok := ctorProps.Properties.GetOwn("prototype")
	if !ok {
		return
	}
	proto := protoVal.AsPlainObject()
	if proto == nil {
		return
	}

	rawPairsOf := func(this vm.Value) ([][2]string, error) {
		obj := this.AsPlainObject()
		if obj == nil {
			return nil, fmt.Errorf("not a URLSearchParams instance")
		}
		fn, ok := obj.GetOwn("rawPairs")
		if !ok || !fn.IsCallable() {
			return nil, fmt.Errorf("not a URLSearchParams instance")
		}
		result, err := vmInst.Call(fn, this, nil)
		if err != nil {
			return nil, err
		}
		arr := result.AsArray()
		if arr == nil {
			return nil, nil
		}
		pairs := make([][2]string, arr.Length())
		for i := 0; i < arr.Length(); i++ {
			pair := arr.Get(i).AsArray()
			if pair == nil || pair.Length() != 2 {
				continue
			}
			pairs[i] = [2]string{pair.Get(0).ToString(), pair.Get(1).ToString()}
		}
		return pairs, nil
	}

	// makeIterator builds a real, spec-shaped iterator object (a
	// `.next()` that returns `{value, done}`, self-iterable via its own
	// `[Symbol.iterator]` returning itself) over whatever `project`
	// turns each raw pair into - `[k, v]` for entries, `k` for keys, `v`
	// for values.
	makeIterator := func(pairs [][2]string, project func(k, v string) vm.Value) vm.Value {
		i := 0
		next := vm.NewNativeFunction(0, false, "next", func(_ []vm.Value) (vm.Value, error) {
			result := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
			if i >= len(pairs) {
				result.SetOwn("value", vm.Undefined)
				result.SetOwn("done", vm.True)
				return vm.NewValueFromPlainObject(result), nil
			}
			p := pairs[i]
			i++
			result.SetOwn("value", project(p[0], p[1]))
			result.SetOwn("done", vm.False)
			return vm.NewValueFromPlainObject(result), nil
		})
		iter := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
		iter.SetOwn("next", next)
		iterVal := vm.NewValueFromPlainObject(iter)
		selfIter := vm.NewNativeFunction(0, false, "[Symbol.iterator]", func(_ []vm.Value) (vm.Value, error) {
			return iterVal, nil
		})
		writable, enumerable, configurable := true, false, true
		iter.DefineOwnPropertyByKey(vm.NewSymbolKey(vmInst.SymbolIterator), selfIter, &writable, &enumerable, &configurable)
		return iterVal
	}

	pairProject := func(k, v string) vm.Value {
		arr := vm.NewArrayWithArgs([]vm.Value{vm.NewString(k), vm.NewString(v)})
		return arr
	}
	keyProject := func(k, _ string) vm.Value { return vm.NewString(k) }
	valueProject := func(_, v string) vm.Value { return vm.NewString(v) }

	entriesFn := vm.NewNativeFunction(0, false, "entries", func(_ []vm.Value) (vm.Value, error) {
		pairs, err := rawPairsOf(vmInst.GetThis())
		if err != nil {
			return vm.Undefined, err
		}
		return makeIterator(pairs, pairProject), nil
	})
	keysFn := vm.NewNativeFunction(0, false, "keys", func(_ []vm.Value) (vm.Value, error) {
		pairs, err := rawPairsOf(vmInst.GetThis())
		if err != nil {
			return vm.Undefined, err
		}
		return makeIterator(pairs, keyProject), nil
	})
	valuesFn := vm.NewNativeFunction(0, false, "values", func(_ []vm.Value) (vm.Value, error) {
		pairs, err := rawPairsOf(vmInst.GetThis())
		if err != nil {
			return vm.Undefined, err
		}
		return makeIterator(pairs, valueProject), nil
	})
	// forEach(callback[, thisArg]): real Node calls callback(value, key,
	// searchParams) - value before key, matching Map.prototype.forEach's
	// own argument order, not the more intuitive (key, value).
	forEachFn := vm.NewNativeFunction(1, false, "forEach", func(args []vm.Value) (vm.Value, error) {
		this := vmInst.GetThis()
		pairs, err := rawPairsOf(this)
		if err != nil {
			return vm.Undefined, err
		}
		if len(args) == 0 || !args[0].IsCallable() {
			return vm.Undefined, vmInst.NewTypeError("callback must be a function")
		}
		callback := args[0]
		var thisArg vm.Value = vm.Undefined
		if len(args) > 1 {
			thisArg = args[1]
		}
		for _, p := range pairs {
			if _, err := vmInst.Call(callback, thisArg, []vm.Value{vm.NewString(p[1]), vm.NewString(p[0]), this}); err != nil {
				return vm.Undefined, err
			}
		}
		return vm.Undefined, nil
	})
	sizeGetter := vm.NewNativeFunction(0, false, "size", func(_ []vm.Value) (vm.Value, error) {
		pairs, err := rawPairsOf(vmInst.GetThis())
		if err != nil {
			return vm.Undefined, err
		}
		return vm.NumberValue(float64(len(pairs))), nil
	})

	proto.SetOwnNonEnumerable("entries", entriesFn)
	proto.SetOwnNonEnumerable("keys", keysFn)
	proto.SetOwnNonEnumerable("values", valuesFn)
	proto.SetOwnNonEnumerable("forEach", forEachFn)
	writable, enumerable, configurable := true, false, true
	proto.DefineOwnPropertyByKey(vm.NewSymbolKey(vmInst.SymbolIterator), entriesFn, &writable, &enumerable, &configurable)
	sizeEnumerable, sizeConfigurable := false, true
	proto.DefineAccessorProperty("size", sizeGetter, true, vm.Undefined, false, &sizeEnumerable, &sizeConfigurable)
}

package host

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

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
// `append`/`delete`/`get`/`getAll`/`has`/`set`/`sort`/`toString`. No
// `.size` getter, no `Symbol.iterator`/`entries`/`keys`/`values`/
// `forEach`, and no `new URLSearchParams(existingInstance)` copy form —
// `ModuleBuilder.Class`'s reflection has no getter or well-known-symbol
// support to hang the first two off of, and the copy form would need to
// distinguish "another URLSearchParams instance" from "a plain object
// that happens to have the same shape," which nothing here needs yet.
// Add real support for any of these once something does, same as
// `url.go`'s own documented gap.
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

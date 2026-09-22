package host

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
	"golang.org/x/net/idna"
)

// specialSchemes are the schemes WHATWG's URL origin algorithm treats as
// having a real (tuple) origin — everything else gets the opaque origin
// "null" (the literal string, matching real Node/browsers), not the
// scheme://host string a naive implementation might produce.
var specialSchemes = map[string]bool{
	"http": true, "https": true, "ws": true, "wss": true,
	"ftp": true, "file": true,
}

// jsURL is a read-only snapshot of a WHATWG URL, exposed to JS as the
// `URL` class. Every field is computed once at construction time (own
// data properties, per ModuleBuilder.Class/bindStructFields — there is
// no live getter/setter support, so mutating an instance's properties
// afterward doesn't recompute href the way real Node's URL does). No
// `.searchParams` property either — nothing needs a *live* link between
// a URL instance and a URLSearchParams yet (see urlsearchparams.go for
// the standalone `new URLSearchParams(...)` class itself, added
// 2026-09-02).
type jsURL struct {
	Href     string `json:"href"`
	Origin   string `json:"origin"`
	Protocol string `json:"protocol"`
	Username string `json:"username"`
	Password string `json:"password"`
	Host     string `json:"host"`
	Hostname string `json:"hostname"`
	Port     string `json:"port"`
	Pathname string `json:"pathname"`
	Search   string `json:"search"`
	Hash     string `json:"hash"`
}

func (u *jsURL) ToString() string { return u.Href }
func (u *jsURL) ToJSON() string   { return u.Href }

// newJSURL parses href as an absolute URL, WHATWG-style: an empty scheme
// (a relative or otherwise not-obviously-a-URL string) is a hard error,
// same as real `new URL(str)` without a base. This matters beyond
// correctness for its own sake — real packages (hosted-git-info's
// parse-url.js is what surfaced this) construct a URL specifically to
// detect malformed/non-URL input via the throw, e.g. to fall through to
// an scp-style-URL correction path. Go's net/url.Parse is far more
// permissive than WHATWG and rarely errors on its own; the empty-scheme
// check is what makes this throw where it needs to.
//
// The optional `base` second argument (real `new URL(href, base)`) is
// what makes a *relative* href resolve instead of hard-erroring — needed
// for e.g. an ESM loader resolving "./helper.ts" against a parent file's
// URL, found via jiti's own relative-import resolution (see
// docs/real-node-plan.md's round 50 entry). Resolved via Go's
// url.URL.ResolveReference, which implements RFC 3986 relative
// resolution — the same rule WHATWG's URL spec's relative-resolution
// step is built on, close enough for every real base+relative-path
// combination found so far (no dot-segment/scheme-relative edge case
// yet needed a WHATWG-exact implementation).
// removeDotSegments applies the WHATWG URL path parser's dot-segment
// rules to an already-escaped absolute path: "." and ".." (in any
// %2e-encoded spelling) are dropped/pop a segment, a trailing one leaves a
// trailing slash, and a file: URL's Windows drive letter is never popped.
func removeDotSegments(p string, isFile bool) string {
	segs := strings.Split(p[1:], "/")
	out := make([]string, 0, len(segs))
	for i, s := range segs {
		last := i == len(segs)-1
		switch strings.ToLower(s) {
		case ".", "%2e":
			if last {
				out = append(out, "")
			}
		case "..", ".%2e", "%2e.", "%2e%2e":
			if len(out) > 0 && !(isFile && len(out) == 1 && isWindowsDriveLetter(out[0])) {
				out = out[:len(out)-1]
			}
			if last {
				out = append(out, "")
			}
		default:
			out = append(out, s)
		}
	}
	return "/" + strings.Join(out, "/")
}

func isWindowsDriveLetter(s string) bool {
	return len(s) == 2 && (s[1] == ':' || s[1] == '|') &&
		((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z'))
}

func newJSURL(href string, base vm.Value) (*jsURL, error) {
	var parsed *url.URL
	var err error
	// Any base is stringified, as in Node: a URL object's href, or ToString.
	baseStr := ""
	if href, ok := hrefFromURLLike(base); ok {
		baseStr = href
	} else if !base.IsUndefined() {
		baseStr = base.ToString()
	}
	if baseStr != "" {
		baseURL, berr := url.Parse(baseStr)
		if berr != nil || baseURL.Scheme == "" {
			return nil, fmt.Errorf("Invalid base URL: %s", baseStr)
		}
		ref, rerr := url.Parse(href)
		if rerr != nil {
			return nil, fmt.Errorf("Invalid URL: %s", href)
		}
		parsed = baseURL.ResolveReference(ref)
	} else {
		parsed, err = url.Parse(href)
		if err != nil {
			return nil, fmt.Errorf("Invalid URL: %s", href)
		}
	}
	if parsed.Scheme == "" {
		return nil, fmt.Errorf("Invalid URL: %s", href)
	}

	protocol := parsed.Scheme + ":"
	origin := "null"
	if specialSchemes[parsed.Scheme] {
		origin = protocol + "//" + parsed.Host
	}
	// WHATWG's URL parser gives a special-scheme URL with no path
	// component a single-slash path, never an empty one - real Node:
	// new URL("http://x").pathname === "/", not "". Go's net/url.Parse
	// leaves Path empty for "http://x" (no error, just an empty string),
	// so this needs an explicit normalization step. Found the hard way
	// while re-probing real undici's fetch() after paserati#302 was
	// fixed (round 75, docs/real-node-plan.md): undici's own
	// lib/core/util.js#parseOrigin re-parses the dispatcher's origin
	// URL and throws InvalidArgumentError('invalid url') unless
	// pathname === '/' exactly - a real, unconditional check on a real
	// call path (every request through Pool/Client construction), not
	// an edge case. Mutating parsed.Path before it feeds Href below
	// also fixes href (real Node: same URL's .href is
	// "http://x/", not "http://x") for the same reason.
	if specialSchemes[parsed.Scheme] && parsed.Path == "" {
		parsed.Path = "/"
	}
	// WHATWG path parsing resolves "." and ".." segments; Go's url.Parse
	// keeps them verbatim (new URL("file:///a/b/../c").pathname must be
	// "/a/c").
	if escaped := parsed.EscapedPath(); strings.HasPrefix(escaped, "/") {
		if cleaned := removeDotSegments(escaped, parsed.Scheme == "file"); cleaned != escaped {
			if unescaped, uerr := url.PathUnescape(cleaned); uerr == nil {
				parsed.Path = unescaped
				parsed.RawPath = cleaned
			}
		}
	}
	search := ""
	if parsed.RawQuery != "" {
		search = "?" + parsed.RawQuery
	}
	hash := ""
	if parsed.Fragment != "" {
		hash = "#" + parsed.Fragment
	}
	password := ""
	if pw, ok := parsed.User.Password(); ok {
		password = pw
	}

	return &jsURL{
		Href:     parsed.String(),
		Origin:   origin,
		Protocol: protocol,
		Username: parsed.User.Username(),
		Password: password,
		Host:     parsed.Host,
		Hostname: parsed.Hostname(),
		Port:     parsed.Port(),
		Pathname: parsed.Path,
		Search:   search,
		Hash:     hash,
	}, nil
}

// hrefFromURLLike duck-types v as a URL instance (own `href`+`protocol`
// string properties - real Node's own internal isURLInstance check is
// a brand check this host has no equivalent for, since a returned
// *jsURL value has no reliable link back to the URL class's own
// prototype; see pathToFileURL's own doc comment), returning its href
// if so. Shared by fs.go's pathArg (a real fs path argument) and
// fileURLToPath above (both real Node APIs that accept a URL instance
// or a string interchangeably).
func hrefFromURLLike(v vm.Value) (string, bool) {
	// vm.Value.AsPlainObject() panics for anything other than
	// TypeObject (it is not a safe "nil for the wrong type" cast) -
	// found the hard way here: a plain string or number argument
	// reaching this duck-type check crashed the whole VM run with a Go
	// panic (recovered, but as an opaque "[VM PANIC] recovered: value
	// is not an object" rather than the ordinary ToString fallback this
	// function's callers expect for a non-URL value.
	if v.Type() != vm.TypeObject {
		return "", false
	}
	obj := v.AsPlainObject()
	if obj == nil {
		return "", false
	}
	hrefVal, ok := obj.GetOwn("href")
	if !ok || !hrefVal.IsString() {
		return "", false
	}
	if _, ok := obj.GetOwn("protocol"); !ok {
		return "", false
	}
	return hrefVal.ToString(), true
}

// fileURLStringToPath is url.fileURLToPath's real logic, factored out
// so fs.go's pathArg (accepting a URL object as a real Node fs path
// argument, e.g. readFileSync(new URL(..., import.meta.url))) can
// share it instead of duplicating the scheme check/conversion.
func fileURLStringToPath(fileURL string) (string, error) {
	u, err := url.Parse(fileURL)
	if err != nil {
		return "", err
	}
	if u.Scheme != "file" {
		return "", fmt.Errorf("fileURLToPath: must be a file URL")
	}
	p := u.Path
	if escaped := u.EscapedPath(); strings.HasPrefix(escaped, "/") {
		if unescaped, uerr := url.PathUnescape(removeDotSegments(escaped, true)); uerr == nil {
			p = unescaped
		}
	}
	return filepath.FromSlash(p), nil
}

func declareURL(p *driver.Paserati) {
	p.DeclareModule("url", func(m *driver.ModuleBuilder) {
		m.Class("URL", &jsURL{}, newJSURL)
		m.Class("URLSearchParams", &urlSearchParams{}, newURLSearchParams)
		// Real Node's url.fileURLToPath() accepts a real URL instance or
		// a string alike - this used to take a plain Go `string`
		// parameter, so a URL object argument (the exact
		// `fileURLToPath(pathToFileURL(p))` round trip real Node code
		// uses) got auto-stringified generically instead of read via
		// its `href`, breaking the moment pathToFileURL below was fixed
		// to return a real URL object rather than a bare string.
		m.Function("fileURLToPath", func(fileURLVal vm.Value) (string, error) {
			if href, ok := hrefFromURLLike(fileURLVal); ok {
				return fileURLStringToPath(href)
			}
			return fileURLStringToPath(fileURLVal.ToString())
		})
		m.Function("pathToFileURL", func(p string) (*jsURL, error) {
			// Real Node's url.pathToFileURL() returns a real URL
			// instance, not a plain string - this used to return a bare
			// Go string, which JS code that calls .href/.pathname/etc.
			// on the result (a very common idiom: `require('url')
			// .pathToFileURL(__filename).href` as a CJS-transpiled
			// stand-in for `import.meta.url`) silently gets `undefined`
			// back from, since a JS string has no such properties.
			// Found chasing real vite's own CJS build
			// (dist/node-cjs/publicUtils.cjs) under noderati: the
			// resulting `undefined` became the *base* argument to a
			// `new URL(relativePath, undefined)`, which WHATWG URL
			// parsing treats as "no base at all" - correctly throwing
			// "Invalid URL" for a relative-only string, but for the
			// wrong reason (a silently-lost base, not a genuinely
			// invalid path).
			if !filepath.IsAbs(p) {
				abs, err := filepath.Abs(p)
				if err != nil {
					return nil, err
				}
				p = abs
			}
			u := url.URL{
				Scheme: "file",
				Path:   filepath.ToSlash(p),
			}
			return newJSURL(u.String(), vm.Undefined)
		})
		m.Function("domainToASCII", func(domain string) (string, error) {
			return idna.ToASCII(domain)
		})
		m.Function("domainToUnicode", func(domain string) (string, error) {
			return idna.ToUnicode(domain)
		})
		m.Function("parse", func(href string) (map[string]string, error) {
			u, err := url.Parse(href)
			if err != nil {
				return nil, err
			}
			result := map[string]string{
				"hostname": u.Hostname(),
				"pathname": u.Path,
				"href":     u.String(),
				"host":     u.Host,
				"port":     u.Port(),
			}
			if u.Scheme != "" {
				result["protocol"] = u.Scheme + ":"
			} else {
				result["protocol"] = ""
			}
			if u.RawQuery != "" {
				result["search"] = "?" + u.RawQuery
			} else {
				result["search"] = ""
			}
			if u.Fragment != "" {
				result["hash"] = "#" + u.Fragment
			} else {
				result["hash"] = ""
			}
			return result, nil
		})
		m.Function("resolve", func(from, to string) (string, error) {
			base, err := url.Parse(from)
			if err != nil {
				return "", err
			}
			ref, err := url.Parse(to)
			if err != nil {
				return "", err
			}
			return base.ResolveReference(ref).String(), nil
		})
		m.Default(nil)
	})
	_ = p.DeclareModuleAlias("node:url", "url")
}

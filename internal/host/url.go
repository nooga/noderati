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

// hrefFromURLLike returns a URL instance's href: paserati's WHATWG URL
// (its [[URLHref]] internal slot), or, duck-typed, any object with own
// string `href` and `protocol` properties. Shared by fs.go's pathArg and
// fileURLToPath, both of which accept a URL instance or a string.
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
	if href, ok := obj.GetInternal("[[URLHref]]"); ok && href.IsString() {
		return href.ToString(), true
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
	vmInst := p.GetVM()
	p.DeclareModule("url", func(m *driver.ModuleBuilder) {
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
		m.Function("pathToFileURL", func(p string) (vm.Value, error) {
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
					return vm.Undefined, err
				}
				p = abs
			}
			u := url.URL{
				Scheme: "file",
				Path:   filepath.ToSlash(p),
			}
			ctor, ok := vmInst.GetGlobal("URL")
			if !ok {
				return vm.Undefined, fmt.Errorf("pathToFileURL: no URL global")
			}
			return vmInst.Construct(ctor, []vm.Value{vm.NewString(u.String())})
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

// installURLExports exports paserati's own WHATWG URL and URLSearchParams
// globals from node:url, as Node does (require('url').URL === URL).
func installURLExports(p *driver.Paserati) {
	vmInst := p.GetVM()
	for _, name := range []string{"URL", "URLSearchParams"} {
		if v, ok := vmInst.GetGlobal(name); ok {
			setNativeExport(p, "url", name, v)
		}
	}
}

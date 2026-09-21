package host

import (
	"os"
	gopath "path"
	"path/filepath"
	"strings"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// newPathParseResult builds the object path.parse() returns, in real
// Node's own property order (root, dir, base, ext, name) - a Go map
// would round-trip the same key/value pairs but iterate (and so
// JSON.stringify/Object.keys) in random order, since Go map iteration
// order isn't stable. Property order is part of path.parse()'s real,
// observable contract.
func newPathParseResult(root, dir, base, ext, name string) vm.Value {
	obj := vm.NewObject(vm.DefaultObjectPrototype).AsPlainObject()
	obj.SetOwn("root", vm.NewString(root))
	obj.SetOwn("dir", vm.NewString(dir))
	obj.SetOwn("base", vm.NewString(base))
	obj.SetOwn("ext", vm.NewString(ext))
	obj.SetOwn("name", vm.NewString(name))
	return vm.NewValueFromPlainObject(obj)
}

func declarePath(p *driver.Paserati) {
	p.DeclareModule("path", func(m *driver.ModuleBuilder) {
		m.Const("sep", string(os.PathSeparator))
		m.Const("delimiter", string(os.PathListSeparator))
		m.Function("join", func(parts ...string) string {
			return filepath.Join(parts...)
		})
		m.Function("dirname", filepath.Dir)
		m.Function("basename", func(p string) string {
			return filepath.Base(p)
		})
		m.Function("extname", filepath.Ext)
		m.Function("isAbsolute", filepath.IsAbs)
		m.Function("normalize", filepath.Clean)
		m.Function("resolve", func(parts ...string) string {
			cwd, err := os.Getwd()
			if err != nil {
				cwd = "/"
			}
			// Real Node's path.resolve processes its arguments right to
			// left, prepending each until an absolute path has been
			// constructed (falling back to cwd if none of them were
			// absolute) -- so a later absolute argument must WIN over an
			// earlier one, not get joined onto it. The previous
			// implementation used filepath.Join(parts...) first, which has
			// no such reset and instead concatenates every segment
			// regardless of absoluteness -- confirmed as the root cause of
			// a real webpack failure (docs/real-node-plan.md, this round):
			// enhanced-resolve's own directory-walking calls
			// path.resolve(cwd, someAbsolutePath), and the join bug
			// produced a doubled path like "<cwd>/<cwd>/wp-fixture/index.js"
			// instead of the real, unmodified absolute path.
			resolved := cwd
			for _, part := range parts {
				if part == "" {
					continue
				}
				if filepath.IsAbs(part) {
					resolved = part
				} else {
					resolved = filepath.Join(resolved, part)
				}
			}
			return filepath.Clean(resolved)
		})
		m.Function("relative", func(from, to string) string {
			rel, err := filepath.Rel(from, to)
			if err != nil {
				return to
			}
			return rel
		})
		m.Function("toNamespacedPath", func(p string) string { return p })
		m.Function("parse", func(p string) vm.Value {
			if os.PathSeparator == '\\' {
				return win32Parse(p)
			}
			return posixParse(p)
		})
		m.Function("format", func(obj map[string]interface{}) string {
			if os.PathSeparator == '\\' {
				return formatPath(`\`, obj)
			}
			return formatPath("/", obj)
		})
		m.Namespace("win32", func(ns *driver.NamespaceBuilder) {
			ns.Const("sep", `\`)
			ns.Const("delimiter", ";")
			ns.Function("basename", win32Basename)
			ns.Function("dirname", win32Dirname)
			ns.Function("extname", win32Extname)
			ns.Function("isAbsolute", win32IsAbsolute)
			ns.Function("join", win32Join)
			ns.Function("normalize", win32Normalize)
			ns.Function("resolve", win32Resolve)
			ns.Function("relative", win32Relative)
			ns.Function("toNamespacedPath", func(p string) string { return p })
			ns.Function("parse", win32Parse)
			ns.Function("format", func(obj map[string]interface{}) string { return formatPath(`\`, obj) })
		})
		m.Namespace("posix", func(ns *driver.NamespaceBuilder) {
			ns.Const("sep", "/")
			ns.Const("delimiter", ":")
			ns.Function("basename", func(p string) string { return gopath.Base(p) })
			ns.Function("dirname", gopath.Dir)
			ns.Function("extname", gopath.Ext)
			ns.Function("isAbsolute", gopath.IsAbs)
			ns.Function("join", func(parts ...string) string { return gopath.Join(parts...) })
			ns.Function("normalize", posixNormalize)
			ns.Function("resolve", posixResolve)
			ns.Function("relative", posixRelative)
			ns.Function("toNamespacedPath", func(p string) string { return p })
			ns.Function("parse", posixParse)
			ns.Function("format", func(obj map[string]interface{}) string { return formatPath("/", obj) })
		})
		m.Default(nil)
	})
	_ = p.DeclareModuleAlias("node:path", "path")
}

// The top-level `path` module's functions delegate to Go's path/filepath,
// which is itself OS-dependent (backslash-separated on Windows, slash-
// separated everywhere else) — correct for it, since real Node's
// unqualified path.* also follows the running platform. path.posix and
// path.win32 are different: real Node guarantees them platform-
// INDEPENDENT (path.posix.* always forward-slash, path.win32.* always
// backslash, regardless of what OS is actually running), specifically so
// cross-platform-aware code can force one behavior or the other. Reusing
// path/filepath for these would silently break that guarantee on any
// non-matching host — e.g. path.win32.join running on Linux would
// produce forward-slash output. posix.* uses Go's platform-independent
// "path" package (always "/"); win32.* is hand-rolled since Go's stdlib
// has no backslash-path equivalent.
//
// Found via path-scurry (glob's real dependency): its PathScurryPosix
// constructor calls posix.resolve(cwd) unconditionally (it's choosing
// the posix implementation deliberately, not because the host happens to
// be posix) — path.posix.resolve not existing at all (previously only
// sep/basename/dirname were implemented here) broke it outright.

func posixNormalize(p string) string {
	if p == "" {
		return "."
	}
	trailingSlash := strings.HasSuffix(p, "/") && p != "/"
	cleaned := gopath.Clean(p)
	if trailingSlash && !strings.HasSuffix(cleaned, "/") {
		cleaned += "/"
	}
	return cleaned
}

func posixResolve(parts ...string) string {
	cwd := "/"
	if wd, err := os.Getwd(); err == nil {
		cwd = filepath.ToSlash(wd)
	}
	resolved := cwd
	for _, part := range parts {
		if part == "" {
			continue
		}
		if gopath.IsAbs(part) {
			resolved = part
		} else {
			resolved = gopath.Join(resolved, part)
		}
	}
	resolved = gopath.Clean(resolved)
	if !gopath.IsAbs(resolved) {
		resolved = gopath.Join(cwd, resolved)
	}
	return resolved
}

func posixRelative(from, to string) string {
	from = posixResolve(from)
	to = posixResolve(to)
	if from == to {
		return ""
	}
	fromParts := splitNonEmpty(from, "/")
	toParts := splitNonEmpty(to, "/")
	i := 0
	for i < len(fromParts) && i < len(toParts) && fromParts[i] == toParts[i] {
		i++
	}
	var segments []string
	for range fromParts[i:] {
		segments = append(segments, "..")
	}
	segments = append(segments, toParts[i:]...)
	if len(segments) == 0 {
		return ""
	}
	return strings.Join(segments, "/")
}

func splitNonEmpty(p, sep string) []string {
	var out []string
	for _, part := range strings.Split(p, sep) {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func win32Basename(p string) string {
	p = strings.ReplaceAll(p, `/`, `\`)
	i := strings.LastIndex(p, `\`)
	if i < 0 {
		return p
	}
	return p[i+1:]
}

func win32Dirname(p string) string {
	p = strings.ReplaceAll(p, `/`, `\`)
	i := strings.LastIndex(p, `\`)
	if i <= 0 {
		if i == 0 {
			return `\`
		}
		return "."
	}
	return p[:i]
}

func win32Extname(p string) string {
	base := win32Basename(p)
	i := strings.LastIndex(base, ".")
	if i <= 0 {
		return ""
	}
	return base[i:]
}

// win32IsAbsolute recognizes `C:\...`, `\\server\share`, and a bare
// leading `\`/`/` (drive-relative-to-root, still "absolute" per Node's
// own path.win32.isAbsolute semantics).
func win32IsAbsolute(p string) bool {
	if len(p) >= 3 && isDriveLetter(p[0]) && p[1] == ':' && (p[2] == '\\' || p[2] == '/') {
		return true
	}
	return strings.HasPrefix(p, `\`) || strings.HasPrefix(p, `/`)
}

func isDriveLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func win32Join(parts ...string) string {
	var nonEmpty []string
	for _, part := range parts {
		if part != "" {
			nonEmpty = append(nonEmpty, strings.ReplaceAll(part, "/", `\`))
		}
	}
	if len(nonEmpty) == 0 {
		return "."
	}
	return win32Normalize(strings.Join(nonEmpty, `\`))
}

func win32Normalize(p string) string {
	p = strings.ReplaceAll(p, "/", `\`)
	if p == "" {
		return "."
	}
	prefix := ""
	rest := p
	if len(p) >= 2 && isDriveLetter(p[0]) && p[1] == ':' {
		prefix = p[:2]
		rest = p[2:]
	}
	leadingSep := strings.HasPrefix(rest, `\`)
	trailingSep := strings.HasSuffix(rest, `\`) && rest != `\`
	segments := splitNonEmpty(rest, `\`)
	var out []string
	for _, seg := range segments {
		switch seg {
		case ".":
			continue
		case "..":
			if len(out) > 0 && out[len(out)-1] != ".." {
				out = out[:len(out)-1]
			} else if !leadingSep {
				out = append(out, "..")
			}
		default:
			out = append(out, seg)
		}
	}
	result := strings.Join(out, `\`)
	if leadingSep {
		result = `\` + result
	} else if result == "" {
		result = "."
	}
	if trailingSep && result != `\` {
		result += `\`
	}
	return prefix + result
}

func win32Resolve(parts ...string) string {
	cwd := `C:\`
	if wd, err := os.Getwd(); err == nil {
		cwd = strings.ReplaceAll(wd, "/", `\`)
	}
	resolved := cwd
	for _, part := range parts {
		if part == "" {
			continue
		}
		if win32IsAbsolute(part) {
			resolved = part
		} else {
			resolved = resolved + `\` + part
		}
	}
	return win32Normalize(resolved)
}

// posixParse is a direct transliteration of real Node's lib/path.js
// posix.parse, kept close to the original so it stays obviously
// correct against the reference implementation rather than an
// independent (and easy to get subtly wrong) reimplementation.
func posixParse(p string) vm.Value {
	root, dir, base, ext, name := "", "", "", "", ""
	if p == "" {
		return newPathParseResult(root, dir, base, ext, name)
	}
	isAbsolute := p[0] == '/'
	start := 0
	if isAbsolute {
		root = "/"
		start = 1
	}

	startDot := -1
	startPart := 0
	end := -1
	matchedSlash := true
	preDotState := 0

	for i := len(p) - 1; i >= start; i-- {
		c := p[i]
		if c == '/' {
			if !matchedSlash {
				startPart = i + 1
				break
			}
			continue
		}
		if end == -1 {
			matchedSlash = false
			end = i + 1
		}
		if c == '.' {
			if startDot == -1 {
				startDot = i
			} else if preDotState != 1 {
				preDotState = 1
			}
		} else if startDot != -1 {
			preDotState = -1
		}
	}

	if startDot == -1 || end == -1 || preDotState == 0 ||
		(preDotState == 1 && startDot == end-1 && startDot == startPart+1) {
		if end != -1 {
			if startPart == 0 && isAbsolute {
				base = p[1:end]
				name = p[1:end]
			} else {
				base = p[startPart:end]
				name = p[startPart:end]
			}
		}
	} else {
		if startPart == 0 && isAbsolute {
			name = p[1:startDot]
			base = p[1:end]
		} else {
			name = p[startPart:startDot]
			base = p[startPart:end]
		}
		ext = p[startDot:end]
	}

	if startPart > 0 {
		dir = p[:startPart-1]
	} else if isAbsolute {
		dir = "/"
	}

	return newPathParseResult(root, dir, base, ext, name)
}

// win32Parse is a direct transliteration of real Node's lib/path.js
// win32.parse (root/UNC detection, then the same dot/slash walk as
// posixParse but bounded by rootEnd instead of a fixed 0/1 start).
func win32Parse(p string) vm.Value {
	root, dir, base, ext, name := "", "", "", "", ""
	length := len(p)
	if length == 0 {
		return newPathParseResult(root, dir, base, ext, name)
	}

	isSep := func(b byte) bool { return b == '\\' || b == '/' }

	rootEnd := 0
	code := p[0]

	if length == 1 {
		if isSep(code) {
			root = p
			dir = p
		} else {
			base = p
			name = p
		}
		return newPathParseResult(root, dir, base, ext, name)
	}

	if isSep(code) {
		rootEnd = 1
		if isSep(p[1]) {
			j := 2
			last := j
			for j < length && !isSep(p[j]) {
				j++
			}
			if j < length && j != last {
				last = j
				for j < length && isSep(p[j]) {
					j++
				}
				if j < length && j != last {
					last = j
					for j < length && !isSep(p[j]) {
						j++
					}
					if j == length {
						rootEnd = j
					} else if j != last {
						rootEnd = j + 1
					}
				}
			}
		}
	} else if isDriveLetter(code) && length > 1 && p[1] == ':' {
		rootEnd = 2
		if length > 2 {
			if isSep(p[2]) {
				if length == 3 {
					root = p
					dir = p
					return newPathParseResult(root, dir, base, ext, name)
				}
				rootEnd = 3
			}
		} else {
			root = p
			dir = p
			return newPathParseResult(root, dir, base, ext, name)
		}
	}
	if rootEnd > 0 {
		root = p[:rootEnd]
	}

	startDot := -1
	startPart := rootEnd
	end := -1
	matchedSlash := true
	preDotState := 0

	for i := length - 1; i >= rootEnd; i-- {
		c := p[i]
		if isSep(c) {
			if !matchedSlash {
				startPart = i + 1
				break
			}
			continue
		}
		if end == -1 {
			matchedSlash = false
			end = i + 1
		}
		if c == '.' {
			if startDot == -1 {
				startDot = i
			} else if preDotState != 1 {
				preDotState = 1
			}
		} else if startDot != -1 {
			preDotState = -1
		}
	}

	if startDot == -1 || end == -1 || preDotState == 0 ||
		(preDotState == 1 && startDot == end-1 && startDot == startPart+1) {
		if end != -1 {
			base = p[startPart:end]
			name = p[startPart:end]
		}
	} else {
		name = p[startPart:startDot]
		base = p[startPart:end]
		ext = p[startDot:end]
	}

	if startPart > 0 && startPart != rootEnd {
		dir = p[:startPart-1]
	} else {
		dir = root
	}

	return newPathParseResult(root, dir, base, ext, name)
}

// formatPath mirrors real Node's shared, sep-parameterized `_format`
// (used by path.format, path.posix.format, path.win32.format alike):
// dir||root, joined to base||(name+ext), with sep only inserted when
// dir isn't already exactly the root.
func formatPath(sep string, obj map[string]interface{}) string {
	strField := func(key string) string {
		if v, ok := obj[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return ""
	}

	root := strField("root")
	dir := strField("dir")
	if dir == "" {
		dir = root
	}
	base := strField("base")
	if base == "" {
		base = strField("name") + strField("ext")
	}
	if dir == "" {
		return base
	}
	if dir == root {
		return dir + base
	}
	return dir + sep + base
}

func win32Relative(from, to string) string {
	from = win32Resolve(from)
	to = win32Resolve(to)
	if strings.EqualFold(from, to) {
		return ""
	}
	fromParts := splitNonEmpty(strings.TrimPrefix(from, from[:2]), `\`)
	toParts := splitNonEmpty(strings.TrimPrefix(to, to[:2]), `\`)
	i := 0
	for i < len(fromParts) && i < len(toParts) && strings.EqualFold(fromParts[i], toParts[i]) {
		i++
	}
	var segments []string
	for range fromParts[i:] {
		segments = append(segments, "..")
	}
	segments = append(segments, toParts[i:]...)
	return strings.Join(segments, `\`)
}

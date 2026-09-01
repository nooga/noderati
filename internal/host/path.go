package host

import (
	"os"
	gopath "path"
	"path/filepath"
	"strings"

	"github.com/nooga/paserati/pkg/driver"
)

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
			if len(parts) == 0 {
				cwd, err := os.Getwd()
				if err != nil {
					return ""
				}
				return cwd
			}
			p := filepath.Join(parts...)
			if filepath.IsAbs(p) {
				return filepath.Clean(p)
			}
			abs, err := filepath.Abs(p)
			if err != nil {
				return filepath.Clean(p)
			}
			return abs
		})
		m.Function("relative", func(from, to string) string {
			rel, err := filepath.Rel(from, to)
			if err != nil {
				return to
			}
			return rel
		})
		m.Function("toNamespacedPath", func(p string) string { return p })
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

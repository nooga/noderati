package host

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// fs_glob.go implements fs.globSync's matcher (the callback and promise
// forms are built on it in the JS layer). It follows real Node's
// minimatch-based semantics where they're observable: `*`/`?`/`[...]`,
// `{a,b}` braces, `@(a|b)`-style extglobs, `**` crossing directories but
// never dotfiles or symlinked directories, a trailing `/` keeping only
// directories, results relative to cwd (absolute for absolute patterns)
// and normalized ("./x" and "a/../x" come back as "x").

type globOptions struct {
	cwd     string
	exclude func(rel string, abs string, isDir bool) bool
}

type globMatch struct {
	rel   string // what Node returns (relative to cwd, or absolute)
	abs   string
	isDir bool
}

// globExpandBraces expands `{a,b}` alternatives (nested too) into
// separate patterns.
func globExpandBraces(p string) []string {
	depth, start := 0, -1
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '\\':
			i++
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			if depth == 0 {
				continue
			}
			depth--
			if depth == 0 {
				body := p[start+1 : i]
				var alts []string
				d, last := 0, 0
				for j := 0; j < len(body); j++ {
					switch body[j] {
					case '\\':
						j++
					case '{':
						d++
					case '}':
						d--
					case ',':
						if d == 0 {
							alts = append(alts, body[last:j])
							last = j + 1
						}
					}
				}
				alts = append(alts, body[last:])
				if len(alts) < 2 {
					continue
				}
				var out []string
				for _, a := range alts {
					out = append(out, globExpandBraces(p[:start]+a+p[i+1:])...)
				}
				return out
			}
		}
	}
	return []string{p}
}

func globHasMagic(seg string) bool {
	return strings.ContainsAny(seg, "*?[") || strings.Contains(seg, "@(") || strings.Contains(seg, "+(")
}

// globSegmentRegexp compiles one path segment's pattern.
func globSegmentRegexp(seg string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	var stack []byte // pending extglob suffixes
	for i := 0; i < len(seg); i++ {
		c := seg[i]
		if i+1 < len(seg) && seg[i+1] == '(' && strings.IndexByte("@?+*", c) >= 0 {
			b.WriteString("(?:")
			stack = append(stack, c)
			i++
			continue
		}
		switch c {
		case '\\':
			if i+1 < len(seg) {
				i++
				b.WriteString(regexp.QuoteMeta(seg[i : i+1]))
			}
		case '*':
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		case '|':
			if len(stack) > 0 {
				b.WriteString("|")
			} else {
				b.WriteString(`\|`)
			}
		case ')':
			if len(stack) == 0 {
				b.WriteString(`\)`)
				continue
			}
			kind := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			b.WriteString(")")
			switch kind {
			case '?':
				b.WriteString("?")
			case '+':
				b.WriteString("+")
			case '*':
				b.WriteString("*")
			}
		case '[':
			end := strings.IndexByte(seg[i+1:], ']')
			if end < 0 {
				b.WriteString(`\[`)
				continue
			}
			class := seg[i+1 : i+1+end]
			if end == 0 { // "[]...]" - a literal ] first
				if e2 := strings.IndexByte(seg[i+2:], ']'); e2 >= 0 {
					class = seg[i+1 : i+2+e2]
					end = e2 + 1
				}
			}
			b.WriteString("[")
			if strings.HasPrefix(class, "!") || strings.HasPrefix(class, "^") {
				b.WriteString("^")
				class = class[1:]
			}
			b.WriteString(strings.ReplaceAll(strings.ReplaceAll(class, `\`, `\\`), "[", `\[`))
			b.WriteString("]")
			i += end + 1
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	for range stack {
		b.WriteString(")")
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return regexp.MustCompile("^" + regexp.QuoteMeta(seg) + "$")
	}
	return re
}

type globSegment struct {
	raw   string
	globs bool // `**`
	re    *regexp.Regexp
}

func globCompile(pattern string) (root string, segs []globSegment, dirOnly bool) {
	pattern = filepath.ToSlash(pattern)
	if strings.HasPrefix(pattern, "/") {
		root = "/"
		pattern = strings.TrimLeft(pattern, "/")
	}
	parts := strings.Split(pattern, "/")
	if len(parts) > 1 && parts[len(parts)-1] == "" {
		dirOnly = true
		parts = parts[:len(parts)-1]
	}
	for _, p := range parts {
		switch {
		case p == "" && len(segs) > 0:
			continue
		case p == "**":
			if len(segs) > 0 && segs[len(segs)-1].globs {
				continue
			}
			segs = append(segs, globSegment{raw: p, globs: true})
		case globHasMagic(p):
			segs = append(segs, globSegment{raw: p, re: globSegmentRegexp(p)})
		default:
			segs = append(segs, globSegment{raw: strings.ReplaceAll(p, `\`, "")})
		}
	}
	return root, segs, dirOnly
}

// globMatchParts matches a relative path's parts against compiled
// segments (used for exclude patterns).
func globMatchParts(segs []globSegment, parts []string) bool {
	if len(segs) == 0 {
		return len(parts) == 0
	}
	s := segs[0]
	if s.globs {
		for i := 0; i <= len(parts); i++ {
			if globMatchParts(segs[1:], parts[i:]) {
				return true
			}
		}
		return false
	}
	if len(parts) == 0 {
		return false
	}
	if s.re != nil {
		if !s.re.MatchString(parts[0]) {
			return false
		}
	} else if s.raw != parts[0] {
		return false
	}
	return globMatchParts(segs[1:], parts[1:])
}

// globExcludeMatcher compiles exclude glob patterns into a predicate over
// relative result paths.
func globExcludeMatcher(patterns []string) func(rel string) bool {
	var compiled [][]globSegment
	for _, p := range patterns {
		for _, e := range globExpandBraces(p) {
			_, segs, _ := globCompile(e)
			compiled = append(compiled, segs)
		}
	}
	return func(rel string) bool {
		parts := strings.Split(filepath.ToSlash(rel), "/")
		for _, segs := range compiled {
			if globMatchParts(segs, parts) {
				return true
			}
		}
		return false
	}
}

func fsGlob(patterns []string, opts globOptions) ([]globMatch, error) {
	var out []globMatch
	seen := map[string]bool{}
	for _, pat := range patterns {
		for _, p := range globExpandBraces(pat) {
			root, segs, dirOnly := globCompile(p)
			g := &globWalker{opts: opts, root: root, dirOnly: dirOnly, seen: seen}
			g.walk(root, segs)
			out = append(out, g.out...)
		}
	}
	return out, nil
}

type globWalker struct {
	opts    globOptions
	root    string
	dirOnly bool
	seen    map[string]bool
	out     []globMatch
	visited map[string]bool
}

func (g *globWalker) abs(rel string) string {
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(g.opts.cwd, rel)
}

func (g *globWalker) result(rel string) string {
	if rel == "" {
		return "."
	}
	return filepath.Clean(rel)
}

func (g *globWalker) excluded(rel string, isDir bool) bool {
	if g.opts.exclude == nil {
		return false
	}
	r := g.result(rel)
	return g.opts.exclude(r, g.abs(r), isDir)
}

func (g *globWalker) emit(rel string, isDir bool) {
	if g.dirOnly && !isDir {
		return
	}
	r := g.result(rel)
	if g.seen[r] {
		return
	}
	g.seen[r] = true
	g.out = append(g.out, globMatch{rel: r, abs: g.abs(r), isDir: isDir})
}

func joinRel(rel, name string) string {
	if rel == "" {
		return name
	}
	if strings.HasSuffix(rel, "/") {
		return rel + name
	}
	return rel + "/" + name
}

func (g *globWalker) walk(rel string, segs []globSegment) {
	if len(segs) == 0 {
		info, err := os.Stat(g.abs(rel))
		if err == nil || rel == "" {
			g.emit(rel, err == nil && info.IsDir())
		}
		return
	}
	seg := segs[0]
	switch {
	case seg.globs:
		// Guard against revisiting the same (rel, remaining) pair.
		key := rel + "\x00" + segKey(segs)
		if g.visited == nil {
			g.visited = map[string]bool{}
		}
		if g.visited[key] {
			return
		}
		g.visited[key] = true
		g.walk(rel, segs[1:])
		entries, err := os.ReadDir(g.abs(rel))
		if err != nil {
			return
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			child := joinRel(rel, e.Name())
			if e.IsDir() { // never through a symlink, as in Node
				if g.excluded(child, true) {
					continue
				}
				g.walk(child, segs)
			} else if len(segs) == 1 {
				if g.excluded(child, false) {
					continue
				}
				g.emit(child, false)
			}
		}
	case seg.re == nil:
		child := joinRel(rel, seg.raw)
		info, err := os.Stat(g.abs(child))
		if err != nil {
			if _, lerr := os.Lstat(g.abs(child)); lerr != nil || len(segs) > 1 {
				return
			}
		}
		isDir := err == nil && info.IsDir()
		if g.excluded(child, isDir) {
			return
		}
		if len(segs) == 1 {
			g.emit(child, isDir)
		} else if isDir {
			g.walk(child, segs[1:])
		}
	default:
		entries, err := os.ReadDir(g.abs(rel))
		if err != nil {
			return
		}
		dotOK := strings.HasPrefix(seg.raw, ".")
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") && !dotOK {
				continue
			}
			if !seg.re.MatchString(name) {
				continue
			}
			child := joinRel(rel, name)
			isDir := e.IsDir()
			if e.Type()&os.ModeSymlink != 0 {
				if info, err := os.Stat(g.abs(child)); err == nil {
					isDir = info.IsDir()
				}
			}
			if g.excluded(child, isDir) {
				continue
			}
			if len(segs) == 1 {
				g.emit(child, isDir)
			} else if isDir {
				g.walk(child, segs[1:])
			}
		}
	}
}

func segKey(segs []globSegment) string {
	parts := make([]string, len(segs))
	for i, s := range segs {
		parts[i] = s.raw
	}
	return strings.Join(parts, "/")
}

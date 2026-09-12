package host

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/nooga/paserati/pkg/modules"
)

// NodeModulesResolver resolves bare npm package specifiers from node_modules,
// by walking up from the importing file's own directory the way Node's real
// resolution algorithm does (see findPackageDir) - never from any extra,
// hardcoded root. It used to also accept a variadic list of "extra
// directories" (the entry script's own directory, plus - until round 63 -
// a couple of hardcoded homebrew global-install paths so pi-coding-agent's
// own dependencies could be found regardless of where noderati happened to
// be invoked from). Deleted 2026-09-06 (round 64, docs/real-node-plan.md's
// Phase 4 section): confirmed, by testing with both removed entirely, that
// findPackageDir's own walk-up already reaches every real dependency a real
// invocation needs - pi --version/--help/-p (real Fireworks backend), the
// full scoreboard, and pi-coding-agent's own real extension-loader call
// pattern (createJiti(import.meta.url,...) from loader.js's own real path)
// all still pass with zero extra directories. The one thing that stops
// working is resolving a bare specifier from a script that was never
// actually part of any package's own node_modules tree in the first place -
// which is exactly what real Node also fails at, not a noderati gap.
type NodeModulesResolver struct {
	priority int
}

// NewNodeModulesResolver returns a resolver that loads packages from
// node_modules by walking up from the importing file's own directory.
func NewNodeModulesResolver() *NodeModulesResolver {
	return &NodeModulesResolver{priority: 0}
}

func (r *NodeModulesResolver) Name() string {
	return "NodeModules"
}

func (r *NodeModulesResolver) Priority() int {
	return r.priority
}

func (r *NodeModulesResolver) CanResolve(specifier string) bool {
	if specifier == "" {
		return false
	}
	if strings.HasPrefix(specifier, ".") || strings.HasPrefix(specifier, "/") {
		return false
	}
	if strings.HasPrefix(specifier, "node:") {
		return false
	}
	if strings.Contains(specifier, "://") {
		return false
	}
	return true
}

func (r *NodeModulesResolver) Resolve(specifier string, fromPath string) (*modules.ResolvedModule, error) {
	startDir, err := resolverStartDir(fromPath)
	if err != nil {
		return nil, err
	}

	pkgName, subpath := splitPackageSpecifier(specifier)
	pkgDir, err := findPackageDir(startDir, pkgName)
	if err != nil {
		return nil, fmt.Errorf("package %q not found: %w", pkgName, err)
	}

	entryPath, err := resolvePackageEntry(pkgDir, subpath, exportsConditionImport)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve entry for %q: %w", specifier, err)
	}

	absPath, err := canonicalPath(entryPath)
	if err != nil {
		return nil, fmt.Errorf("failed to absolutize %q: %w", entryPath, err)
	}

	source, err := openMaybeCJS(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open %q: %w", absPath, err)
	}

	return &modules.ResolvedModule{
		Specifier:    specifier,
		ResolvedPath: absPath,
		Source:       source,
		Resolver:     r.Name(),
	}, nil
}

type packageJSON struct {
	Main    string          `json:"main"`
	Module  string          `json:"module"`
	Exports json.RawMessage `json:"exports"`
	Imports json.RawMessage `json:"imports"`
}

func resolverStartDir(fromPath string) (string, error) {
	if fromPath == "" || fromPath == "." {
		return os.Getwd()
	}
	info, err := os.Stat(fromPath)
	if err == nil && !info.IsDir() {
		return filepath.Abs(filepath.Dir(fromPath))
	}
	if err == nil {
		return filepath.Abs(fromPath)
	}
	if os.IsNotExist(err) {
		return filepath.Abs(filepath.Dir(fromPath))
	}
	return "", err
}

func splitPackageSpecifier(specifier string) (pkgName, subpath string) {
	if strings.HasPrefix(specifier, "@") {
		parts := strings.SplitN(specifier, "/", 3)
		if len(parts) < 2 {
			return specifier, ""
		}
		pkgName = parts[0] + "/" + parts[1]
		if len(parts) == 3 {
			subpath = parts[2]
		}
		return pkgName, subpath
	}

	parts := strings.SplitN(specifier, "/", 2)
	pkgName = parts[0]
	if len(parts) == 2 {
		subpath = parts[1]
	}
	return pkgName, subpath
}

// canonicalPath resolves filename to an absolute path with every symlink
// component followed - the same thing real Node's own module resolution
// does (via fs.realpathSync) before using a resolved path as a module
// cache key or as the "from" directory for a nested require/import.
// Skipping this is what a real, reproduced bug traced back to: a real
// npm/homebrew global install (@earendil-works/pi-coding-agent's own
// node_modules, found while chasing the Bedrock "@smithy/core/protocols"
// blocker) had a stray self-referential
// `node_modules/node_modules -> node_modules` symlink sitting inside it.
// findPackageDir's ancestor walk (see below) matches an ancestor as soon
// as `<ancestor>/node_modules/<pkg>` exists - and that self-symlink makes
// that check succeed one level too early, prepending an extra, spurious
// "/node_modules" segment onto the resolved path. Each hop across a
// circular require (@smithy/core/protocols <-> @smithy/core/serde, a
// real, unavoidable cycle in real, unmodified `@smithy/core`'s own
// dist-cjs output) re-triggered that same shortcut from a deeper starting
// point, so the resolved absolute path grew by one more "/node_modules"
// every time - a different string each time for what is really the same
// file. Since execFile's own module cache is keyed by that exact string,
// every hop looked like a brand-new, never-before-required module: the
// circular require never got the shared, in-progress `module.exports`
// real Node's cache would hand back, and something reading from the
// wrong, freshly-(re)executing instance's not-yet-populated exports (a
// `class X extends SerdeContext` base class, in the repro that surfaced
// this) got `undefined` instead of the real one - "Class extends value
// undefined is not a constructor or null", nothing to do with class
// syntax itself. EvalSymlinks collapses any such shortcut straight back
// to the one real, canonical file, restoring cache identity regardless
// of which path first reached it - falls back to a plain filepath.Abs
// only if the file can't be stat'd at all (e.g. it was already deleted).
func canonicalPath(filename string) (string, error) {
	if resolved, err := filepath.EvalSymlinks(filename); err == nil {
		return filepath.Abs(resolved)
	}
	return filepath.Abs(filename)
}

func findPackageDir(startDir, pkgName string) (string, error) {
	dir := startDir
	for {
		candidate := filepath.Join(dir, "node_modules", filepath.FromSlash(pkgName))
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return candidate, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("node_modules/%s not found from %s", pkgName, startDir)
}

// exportsCondition picks which of the "import"/"require" branches of a
// conditional exports map applies to the module system doing the
// resolving. Real Node never considers the branch that doesn't match the
// caller's module system (a require() call never picks an "import"-only
// target, and vice versa) — mixing them up silently hands a CJS require()
// call an ESM file (or the reverse), which our loader then either
// misparses or, worse, "successfully" loads with an empty exports object.
type exportsCondition int

const (
	exportsConditionRequire exportsCondition = iota
	exportsConditionImport
)

func (c exportsCondition) candidates() []string {
	if c == exportsConditionRequire {
		return []string{"node", "require", "default"}
	}
	return []string{"node", "import", "default"}
}

func resolvePackageEntry(pkgDir, subpath string, cond exportsCondition) (string, error) {
	if subpath != "" {
		return resolveSubpathEntry(pkgDir, subpath, cond)
	}
	return resolveMainEntry(pkgDir, cond)
}

func resolveMainEntry(pkgDir string, cond exportsCondition) (string, error) {
	pkg, err := readPackageJSON(pkgDir)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}

	if pkg != nil {
		if entry, ok, err := entryFromExports(pkg.Exports, ".", cond); err != nil {
			return "", err
		} else if ok {
			return resolveRelativeEntry(pkgDir, entry)
		}
		// "module" is a bundler-only convention (webpack/rollup/esbuild),
		// not part of real Node's own resolution algorithm at all - real
		// Node uses "main" for both require() and import when there's no
		// "exports" map, full stop, and completely ignores "module".
		// Verified directly (not assumed): a synthetic package with both
		// fields and no "exports" resolves to "main" under real Node's
		// `import`, every time. This dates back to this resolver's very
		// first commit and had gone unquestioned since - found the hard
		// way chasing a real crash while constructing
		// @aws-sdk/client-bedrock-runtime (docs/real-node-plan.md, round
		// 96): the package's own package.json has no "exports", so this
		// preference silently picked its `dist-es/index.js` (the
		// "module" field) over the `dist-cjs/index.js` real Node's
		// import would actually load - a different file with a
		// different import graph and, it turned out, a different crash
		// than the one real Node's own resolution would ever hit here.
		if pkg.Main != "" {
			return resolveRelativeEntry(pkgDir, pkg.Main)
		}
	}

	return resolveIndexFallback(pkgDir)
}

func resolveSubpathEntry(pkgDir, subpath string, cond exportsCondition) (string, error) {
	pkg, err := readPackageJSON(pkgDir)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}

	exportKey := "./" + subpath
	if pkg != nil {
		if entry, ok, err := entryFromExports(pkg.Exports, exportKey, cond); err != nil {
			return "", err
		} else if ok {
			return resolveRelativeEntry(pkgDir, entry)
		}
	}

	if path, ok := tryExistingFile(pkgDir, subpath); ok {
		return path, nil
	}

	return "", fmt.Errorf("subpath %q not found in %s", subpath, pkgDir)
}

func readPackageJSON(pkgDir string) (*packageJSON, error) {
	data, err := os.ReadFile(filepath.Join(pkgDir, "package.json"))
	if err != nil {
		return nil, err
	}
	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, fmt.Errorf("invalid package.json in %s: %w", pkgDir, err)
	}
	return &pkg, nil
}

func entryFromExports(exports json.RawMessage, key string, cond exportsCondition) (string, bool, error) {
	if len(exports) == 0 {
		return "", false, nil
	}

	// A top-level "exports" value that's a string or an array is real
	// Node's shorthand for "this whole field is the target for the '.'
	// subpath" (no other subpaths exist) — see resolveExportTarget's own
	// array handling below for why an array shows up here at all.
	var asString string
	if err := json.Unmarshal(exports, &asString); err == nil {
		if key == "." {
			return asString, true, nil
		}
		return "", false, nil
	}

	var asArray []json.RawMessage
	if err := json.Unmarshal(exports, &asArray); err == nil {
		if key == "." {
			return resolveExportTarget(exports, cond)
		}
		return "", false, nil
	}

	var asMap map[string]json.RawMessage
	if err := json.Unmarshal(exports, &asMap); err != nil {
		return "", false, fmt.Errorf("unsupported exports format")
	}

	value, ok := asMap[key]
	if !ok {
		return "", false, nil
	}

	return resolveExportTarget(value, cond)
}

func resolveExportTarget(raw json.RawMessage, cond exportsCondition) (string, bool, error) {
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString, true, nil
	}

	// Real Node also allows an export target to be an *array* of
	// alternatives, tried in order — a fallback for resolvers that don't
	// understand one of the shapes inside it (e.g. a conditions object),
	// rather than a set of conditions itself. Confirmed directly: real
	// `eslint-visitor-keys@4.x`'s package.json ships exactly this shape
	// (`"exports": {".": [{"import": "...", "require": "..."}, "./dist/
	// eslint-visitor-keys.cjs"]}`) and real Node resolves it — this
	// resolver didn't handle the array case at all, so real, unmodified
	// `eslint` failed to import with "Cannot find module
	// 'eslint-visitor-keys'" even though the package (and a valid target
	// for the caller's condition) both genuinely exist on disk.
	var asArray []json.RawMessage
	if err := json.Unmarshal(raw, &asArray); err == nil {
		for _, candidate := range asArray {
			resolved, found, err := resolveExportTarget(candidate, cond)
			if err != nil {
				continue
			}
			if found && resolved != "" {
				return resolved, true, nil
			}
		}
		return "", false, nil
	}

	var asMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &asMap); err != nil {
		return "", false, fmt.Errorf("unsupported export target format")
	}

	for _, candidate := range cond.candidates() {
		entry, ok := asMap[candidate]
		if !ok {
			continue
		}
		resolved, found, err := resolveExportTarget(entry, cond)
		if err != nil {
			return "", false, err
		}
		if found && resolved != "" {
			return resolved, true, nil
		}
	}

	return "", false, nil
}

func resolveRelativeEntry(pkgDir, entry string) (string, error) {
	if filepath.IsAbs(entry) {
		return entry, nil
	}
	path := filepath.Join(pkgDir, filepath.FromSlash(entry))
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return path, nil
	}
	if path, ok := tryExistingFile(pkgDir, strings.TrimPrefix(filepath.ToSlash(entry), "./")); ok {
		return path, nil
	}
	return "", fmt.Errorf("entry %q does not exist in %s", entry, pkgDir)
}

func resolveIndexFallback(pkgDir string) (string, error) {
	for _, name := range []string{"index.js", "index.mjs", "index.ts"} {
		path := filepath.Join(pkgDir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", fmt.Errorf("no entry point found in %s", pkgDir)
}

func tryExistingFile(pkgDir, subpath string) (string, bool) {
	candidates := []string{
		subpath,
		subpath + ".js",
		subpath + ".mjs",
		subpath + ".ts",
		filepath.Join(subpath, "index.js"),
		filepath.Join(subpath, "index.mjs"),
		filepath.Join(subpath, "index.ts"),
	}

	for _, candidate := range candidates {
		path := filepath.Join(pkgDir, filepath.FromSlash(candidate))
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() {
			return path, true
		}
	}
	return "", false
}

func openMaybeCJS(absPath string) (io.ReadCloser, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}
	src := StripShebang(string(data))
	if shouldWrapCJS(absPath, src) {
		src = patchCJSSource(src, absPath)
		return io.NopCloser(strings.NewReader(cjsESMWrapper(absPath, src))), nil
	}
	return io.NopCloser(strings.NewReader(src)), nil
}

func shouldWrapCJS(absPath, source string) bool {
	ext := strings.ToLower(filepath.Ext(absPath))
	switch ext {
	case ".mjs", ".ts", ".mts", ".json":
		return false
	case ".cjs":
		return true
	}
	if looksLikeESMSource(source) {
		return false
	}
	return looksLikeCJSSource(source) || ext == ".js"
}

// esmKeywordRe looks for `import`/`export` used as the reserved-word
// statement keyword, anywhere in the source — not just at the start of
// a line. The line-prefix version this replaced (`strings.HasPrefix(trim,
// "import ")`) missed two real shapes at once: a minified bundle is one
// giant line, so "starts a line" never matches anything past line 1; and
// minifiers routinely drop the space after the keyword entirely
// (`import{fileURLToPath as X}from...`, `export{...}`), so even a
// same-line check requiring a literal trailing space would still miss
// it. A real, unmodified ESM bundle (glob's minified dist/esm/index.min.js
// is what surfaced this) was silently misdetected as CommonJS this way —
// CJS-wrapped despite having no CommonJS in it at all, which hides its
// `export{...}` inside a function body the wrapper wraps around it,
// leaving every export silently empty with no error anywhere. `import`
// and `export` are JS reserved words — they can only appear as this
// keyword, as `import()`/`import.meta`, or inside a string/comment, so a
// same-word-boundary match anywhere in the source is safe.
var esmKeywordRe = regexp.MustCompile(`(?:^|[^\w$])(?:import|export)(?:[^\w$]|$)`)

// dynamicImportCallRe matches a dynamic `import(...)` call expression -
// legal in both CommonJS and ES module source (a CJS file can perfectly
// well use it to load an ESM-only dependency), so its presence alone
// must not count as "this file is ESM" the way a static `import ...
// from ...`/bare `export ...` declaration does. esmKeywordRe's blanket
// word-boundary match on "import" can't tell the two apart by itself.
// Found via a real CJS file - @smithy/core's own dist-cjs
// submodules/protocols/index.js (a real Bedrock/@smithy dependency) -
// doing exactly `const { X } = await import('@smithy/core/event-streams')`
// among otherwise unambiguous `require(...)`/`module.exports` CJS: that
// one dynamic-import call was enough to flip looksLikeESMSource to
// true, which skipped CJS-wrapping the file entirely and left its own
// top-level `require(...)` calls with no `require` in scope once it was
// loaded as if it really were ESM source - "ReferenceError: require is
// not defined", not a resolution bug at all.
var dynamicImportCallRe = regexp.MustCompile(`\bimport\s*\(`)

func looksLikeESMSource(source string) bool {
	source = dynamicImportCallRe.ReplaceAllString(source, "")
	return esmKeywordRe.MatchString(source)
}

func looksLikeCJSSource(source string) bool {
	return strings.Contains(source, "module.exports") ||
		strings.Contains(source, "exports.") ||
		strings.Contains(source, "require(")
}

func cjsESMWrapper(absPath, source string) string {
	var b strings.Builder
	b.WriteString("const __cjs = process.__noderatiCJSRequire(")
	b.WriteString(strconv.Quote(absPath))
	b.WriteString(");\nexport default __cjs;\n")
	for _, name := range extractCJSExportNames(source) {
		alias := "__cjs_named_" + name
		b.WriteString("const ")
		b.WriteString(alias)
		b.WriteString(" = __cjs[")
		b.WriteString(strconv.Quote(name))
		b.WriteString("];\nexport { ")
		b.WriteString(alias)
		b.WriteString(" as ")
		b.WriteString(cjsExportAlias(name))
		b.WriteString(" };\n")
	}
	return b.String()
}

func extractCJSExportNames(source string) []string {
	seen := make(map[string]bool)
	var names []string
	add := func(name string) {
		if name == "" || name == "default" || seen[name] {
			return
		}
		seen[name] = true
		names = append(names, name)
	}

	for _, m := range regexp.MustCompile(`exports\.(\w+)\s*=`).FindAllStringSubmatch(source, -1) {
		add(m[1])
	}

	if m := regexp.MustCompile(`module\.exports\s*=\s*\{([\s\S]*?)\n\}`).FindStringSubmatch(source); len(m) == 2 {
		body := m[1]
		for _, part := range regexp.MustCompile(`(?m)^\s*(\w+)\s*,?\s*$`).FindAllStringSubmatch(body, -1) {
			add(part[1])
		}
		for _, part := range regexp.MustCompile(`(?m)^\s*(\w+)\s*:`).FindAllStringSubmatch(body, -1) {
			add(part[1])
		}
	}
	return names
}

// cjsExportAlias quotes export names that collide with TS keywords.
func cjsExportAlias(name string) string {
	switch name {
	case "satisfies", "is", "as", "type", "declare", "module", "namespace", "interface", "enum":
		return strconv.Quote(name)
	default:
		return name
	}
}

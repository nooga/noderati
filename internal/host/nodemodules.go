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

// NodeModulesResolver resolves bare npm package specifiers from node_modules.
type NodeModulesResolver struct {
	priority  int
	extraDirs []string
}

// NewNodeModulesResolver returns a resolver that loads packages from node_modules.
// extraDirs are additional roots to search (e.g. the entry script's directory).
func NewNodeModulesResolver(extraDirs ...string) *NodeModulesResolver {
	return &NodeModulesResolver{priority: 0, extraDirs: extraDirs}
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
		for _, extra := range r.extraDirs {
			if extra == "" {
				continue
			}
			pkgDir, err = findPackageDir(extra, pkgName)
			if err == nil {
				break
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("package %q not found: %w", pkgName, err)
	}

	entryPath, err := resolvePackageEntry(pkgDir, subpath, exportsConditionImport)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve entry for %q: %w", specifier, err)
	}

	absPath, err := filepath.Abs(entryPath)
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
		if cond == exportsConditionImport && pkg.Module != "" {
			return resolveRelativeEntry(pkgDir, pkg.Module)
		}
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

	var asString string
	if err := json.Unmarshal(exports, &asString); err == nil {
		if key == "." {
			return asString, true, nil
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
	src = patchModuleSource(src, absPath)
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

func looksLikeESMSource(source string) bool {
	for _, line := range strings.Split(source, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "import ") || strings.HasPrefix(trim, "export ") {
			return true
		}
	}
	return false
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

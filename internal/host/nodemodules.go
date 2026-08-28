package host

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

	entryPath, err := resolvePackageEntry(pkgDir, subpath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve entry for %q: %w", specifier, err)
	}

	absPath, err := filepath.Abs(entryPath)
	if err != nil {
		return nil, fmt.Errorf("failed to absolutize %q: %w", entryPath, err)
	}

	source, err := os.Open(absPath)
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

func resolvePackageEntry(pkgDir, subpath string) (string, error) {
	if subpath != "" {
		return resolveSubpathEntry(pkgDir, subpath)
	}
	return resolveMainEntry(pkgDir)
}

func resolveMainEntry(pkgDir string) (string, error) {
	pkg, err := readPackageJSON(pkgDir)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}

	if pkg != nil {
		if entry, ok, err := entryFromExports(pkg.Exports, "."); err != nil {
			return "", err
		} else if ok {
			return resolveRelativeEntry(pkgDir, entry)
		}
		if pkg.Module != "" {
			return resolveRelativeEntry(pkgDir, pkg.Module)
		}
		if pkg.Main != "" {
			return resolveRelativeEntry(pkgDir, pkg.Main)
		}
	}

	return resolveIndexFallback(pkgDir)
}

func resolveSubpathEntry(pkgDir, subpath string) (string, error) {
	pkg, err := readPackageJSON(pkgDir)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}

	exportKey := "./" + subpath
	if pkg != nil {
		if entry, ok, err := entryFromExports(pkg.Exports, exportKey); err != nil {
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

func entryFromExports(exports json.RawMessage, key string) (string, bool, error) {
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

	return resolveExportTarget(value)
}

func resolveExportTarget(raw json.RawMessage) (string, bool, error) {
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString, true, nil
	}

	var asMap map[string]string
	if err := json.Unmarshal(raw, &asMap); err != nil {
		return "", false, fmt.Errorf("unsupported export target format")
	}

	for _, candidate := range []string{"import", "default", "require"} {
		if entry, ok := asMap[candidate]; ok && entry != "" {
			return entry, true, nil
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

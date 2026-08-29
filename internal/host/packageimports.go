package host

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nooga/paserati/pkg/modules"
)

// PackageImportsResolver resolves package.json "imports" specifiers (#foo).
type PackageImportsResolver struct {
	priority int
}

func NewPackageImportsResolver() *PackageImportsResolver {
	return &PackageImportsResolver{priority: -10}
}

func (r *PackageImportsResolver) Name() string  { return "PackageImports" }
func (r *PackageImportsResolver) Priority() int { return r.priority }

func (r *PackageImportsResolver) CanResolve(specifier string) bool {
	return strings.HasPrefix(specifier, "#")
}

func (r *PackageImportsResolver) Resolve(specifier string, fromPath string) (*modules.ResolvedModule, error) {
	pkgDir, err := findOwnerPackageDir(fromPath)
	if err != nil {
		return nil, err
	}

	pkg, err := readPackageJSON(pkgDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read package.json for %s: %w", specifier, err)
	}
	if len(pkg.Imports) == 0 {
		return nil, fmt.Errorf("package %s has no imports field", pkgDir)
	}

	entry, ok, err := entryFromImports(pkg.Imports, specifier)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("import %q not defined in %s", specifier, pkgDir)
	}

	resolved, err := resolveRelativeEntry(pkgDir, entry)
	if err != nil {
		return nil, err
	}

	source, err := openMaybeCJS(resolved)
	if err != nil {
		return nil, fmt.Errorf("failed to open %q: %w", resolved, err)
	}

	return &modules.ResolvedModule{
		Specifier:    specifier,
		ResolvedPath: resolved,
		Source:       source,
		Resolver:     r.Name(),
	}, nil
}

func findOwnerPackageDir(fromPath string) (string, error) {
	dir := fromPath
	if dir == "" || dir == "." {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = cwd
	} else if info, err := os.Stat(dir); err == nil && !info.IsDir() {
		dir = filepath.Dir(dir)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("no package.json found for %s", fromPath)
}

func entryFromImports(imports json.RawMessage, key string) (string, bool, error) {
	if len(imports) == 0 {
		return "", false, nil
	}

	var asMap map[string]json.RawMessage
	if err := json.Unmarshal(imports, &asMap); err != nil {
		return "", false, fmt.Errorf("unsupported imports format")
	}

	value, ok := asMap[key]
	if !ok {
		return "", false, nil
	}
	return resolveImportTarget(value)
}

func resolveImportTarget(raw json.RawMessage) (string, bool, error) {
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString, true, nil
	}

	var asMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &asMap); err != nil {
		return "", false, fmt.Errorf("unsupported import target format")
	}

	for _, candidate := range []string{"node", "import", "require", "default"} {
		entry, ok := asMap[candidate]
		if !ok {
			continue
		}
		resolved, found, err := resolveImportTarget(entry)
		if err != nil {
			return "", false, err
		}
		if found && resolved != "" {
			return resolved, true, nil
		}
	}

	return "", false, nil
}

package host

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nooga/paserati/pkg/modules"
)

// OSPathResolver resolves relative and absolute specifiers on the real OS
// filesystem. Paserati's DirFS resolver cannot open absolute paths because
// io/fs.ValidPath rejects them.
type OSPathResolver struct {
	priority int
}

func NewOSPathResolver() *OSPathResolver {
	return &OSPathResolver{priority: 50}
}

func (r *OSPathResolver) Name() string  { return "OSPath" }
func (r *OSPathResolver) Priority() int { return r.priority }
func (r *OSPathResolver) CanResolve(specifier string) bool {
	if specifier == "" {
		return false
	}
	return strings.HasPrefix(specifier, "./") ||
		strings.HasPrefix(specifier, "../") ||
		strings.HasPrefix(specifier, "/") ||
		filepath.IsAbs(specifier)
}

func (r *OSPathResolver) Resolve(specifier string, fromPath string) (*modules.ResolvedModule, error) {
	target, err := osResolveTarget(specifier, fromPath)
	if err != nil {
		return nil, err
	}
	resolved, err := osTryFile(target)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve %s: %w", specifier, err)
	}
	// Route through the same CJS-detection/wrapping openMaybeCJS applies for
	// NodeModulesResolver (nodemodules.go) — a relative `import` of a
	// `.cjs`/CommonJS-shaped `.js` file is completely ordinary Node interop
	// (e.g. jiti's own lib/jiti-static.mjs does `import _createJiti from
	// "../dist/jiti.cjs"`), and without this an OS-path-resolved CJS file
	// loads as plain ESM with no `require`/`module`/`exports` injected at
	// all, throwing "require is not defined" the moment it uses any of
	// them.
	source, err := openMaybeCJS(resolved)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", resolved, err)
	}
	return &modules.ResolvedModule{
		Specifier:    specifier,
		ResolvedPath: resolved,
		Source:       source,
		Resolver:     r.Name(),
	}, nil
}

func osResolveTarget(specifier, fromPath string) (string, error) {
	if strings.HasPrefix(specifier, "/") || filepath.IsAbs(specifier) {
		return filepath.Clean(specifier), nil
	}

	base := fromPath
	if base == "" || base == "." {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		base = cwd
	} else if info, err := os.Stat(base); err == nil && info.IsDir() {
		// already a directory
	} else {
		base = filepath.Dir(base)
	}
	return filepath.Clean(filepath.Join(base, specifier)), nil
}

func osTryFile(target string) (string, error) {
	candidates := []string{
		target,
		target + ".js",
		target + ".mjs",
		target + ".cjs",
		target + ".ts",
		filepath.Join(target, "index.js"),
		filepath.Join(target, "index.mjs"),
		filepath.Join(target, "index.cjs"),
		filepath.Join(target, "index.ts"),
	}
	if strings.HasSuffix(target, ".js") {
		stem := strings.TrimSuffix(target, ".js")
		candidates = append(candidates, stem+".ts", stem+".tsx")
	}
	for _, c := range candidates {
		info, err := os.Stat(c)
		if err == nil && !info.IsDir() {
			abs, err := filepath.Abs(c)
			if err != nil {
				return c, nil
			}
			return abs, nil
		}
	}
	return "", fmt.Errorf("module not found: %s", target)
}

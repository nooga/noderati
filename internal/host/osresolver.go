package host

import (
	"fmt"
	"io"
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
	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", resolved, err)
	}
	src := patchModuleSource(StripShebang(string(data)), resolved)
	return &modules.ResolvedModule{
		Specifier:    specifier,
		ResolvedPath: resolved,
		Source:       io.NopCloser(strings.NewReader(src)),
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

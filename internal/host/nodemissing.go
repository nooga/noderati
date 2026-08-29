package host

import (
	"fmt"
	"io"
	"strings"

	"github.com/nooga/paserati/pkg/modules"
)

// NodeMissingResolver returns a clear error for unknown node: built-ins.
type NodeMissingResolver struct {
	priority int
}

func NewNodeMissingResolver() *NodeMissingResolver {
	return &NodeMissingResolver{priority: 200}
}

func (r *NodeMissingResolver) Name() string  { return "NodeMissing" }
func (r *NodeMissingResolver) Priority() int { return r.priority }

func (r *NodeMissingResolver) CanResolve(specifier string) bool {
	return strings.HasPrefix(specifier, "node:")
}

func (r *NodeMissingResolver) Resolve(specifier string, _ string) (*modules.ResolvedModule, error) {
	src := fmt.Sprintf("throw new Error(%q);", "Cannot find module '"+specifier+"'")
	return &modules.ResolvedModule{
		Specifier:    specifier,
		ResolvedPath: "noderati-missing:" + specifier,
		Source:       io.NopCloser(strings.NewReader(src)),
		Resolver:     r.Name(),
	}, nil
}

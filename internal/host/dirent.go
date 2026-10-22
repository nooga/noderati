package host

import (
	"os"
	"path/filepath"

	"github.com/nooga/paserati/pkg/vm"
)

// Real Node's readdir(Sync) returns Dirent objects (fs_classes.go)
// instead of plain name strings when called with { withFileTypes: true }
// - real packages that walk a tree (path-scurry, glob's real filesystem
// dependency, is what surfaced this) use that option specifically so
// they don't need a second stat() call per entry just to tell files from
// directories.

// withFileTypesRequested reports whether a readdir(Sync) options argument
// (a JS object; noderati's own reflection converts it to a Go map, a
// string "encoding" shorthand converts to nil) asked for Dirent entries.
func withFileTypesRequested(opts map[string]interface{}) bool {
	if opts == nil {
		return false
	}
	v, ok := opts["withFileTypes"].(bool)
	return ok && v
}

func recursiveRequested(opts map[string]interface{}) bool {
	if opts == nil {
		return false
	}
	v, ok := opts["recursive"].(bool)
	return ok && v
}

// readdirEntries lists path's entries as vm.Values — plain name strings
// normally, real Dirent objects when withFileTypes was requested. With
// recursive it walks breadth-first like real Node, naming nested entries
// by their path relative to `path` (Dirents keep the basename and carry
// the containing directory as parentPath).
func readdirEntries(vmInst *vm.VM, path string, opts map[string]interface{}) ([]vm.Value, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	withTypes := withFileTypesRequested(opts)
	recursive := recursiveRequested(opts)
	var out []vm.Value
	type pending struct {
		rel     string
		entries []os.DirEntry
	}
	queue := []pending{{"", entries}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		parent := path
		if cur.rel != "" {
			parent = filepath.Join(path, cur.rel)
		}
		for _, e := range cur.entries {
			rel := e.Name()
			if cur.rel != "" {
				rel = filepath.Join(cur.rel, e.Name())
			}
			if withTypes {
				out = append(out, newDirent(vmInst, e.Name(), parent, e.Type()))
			} else {
				out = append(out, vm.NewString(rel))
			}
			if recursive && e.IsDir() {
				if sub, err := os.ReadDir(filepath.Join(path, rel)); err == nil {
					queue = append(queue, pending{rel, sub})
				}
			}
		}
	}
	if out == nil {
		out = []vm.Value{}
	}
	return out, nil
}

package host

import (
	"os"

	"github.com/nooga/paserati/pkg/vm"
)

// newDirent builds a real fs.Dirent-shaped object: .name plus the usual
// is*() type-check methods. Real Node's readdir(Sync) returns these
// instead of plain name strings when called with { withFileTypes: true }
// — real packages that walk a tree (path-scurry, glob's real filesystem
// dependency, is what surfaced this) use that option specifically so
// they don't need a second stat() call per entry just to tell files from
// directories. Without it, readdirSync silently returning plain strings
// wasn't a type error anywhere — the caller's own entToType()-style
// dispatch (`e.isFile() ? ... : e.isDirectory() ? ...`) just found none
// of those methods on a string and fell through every branch, walking
// zero children with no error at all.
func newDirent(vmInst *vm.VM, name string, mode os.FileMode) vm.Value {
	obj := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	obj.SetOwn("name", vm.NewString(name))
	boolFn := func(v bool) vm.Value {
		return vm.NewNativeFunction(0, false, "", func(_ []vm.Value) (vm.Value, error) {
			return vm.BooleanValue(v), nil
		})
	}
	obj.SetOwn("isFile", boolFn(mode.IsRegular()))
	obj.SetOwn("isDirectory", boolFn(mode.IsDir()))
	obj.SetOwn("isSymbolicLink", boolFn(mode&os.ModeSymlink != 0))
	obj.SetOwn("isBlockDevice", boolFn(mode&os.ModeDevice != 0 && mode&os.ModeCharDevice == 0))
	obj.SetOwn("isCharacterDevice", boolFn(mode&os.ModeCharDevice != 0))
	obj.SetOwn("isFIFO", boolFn(mode&os.ModeNamedPipe != 0))
	obj.SetOwn("isSocket", boolFn(mode&os.ModeSocket != 0))
	return vm.NewValueFromPlainObject(obj)
}

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

// readdirEntries lists path's entries as vm.Values — plain name strings
// normally, real Dirent objects when withFileTypes was requested.
func readdirEntries(vmInst *vm.VM, path string, opts map[string]interface{}) ([]vm.Value, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	withTypes := withFileTypesRequested(opts)
	out := make([]vm.Value, len(entries))
	for i, e := range entries {
		if withTypes {
			out[i] = newDirent(vmInst, e.Name(), e.Type())
		} else {
			out[i] = vm.NewString(e.Name())
		}
	}
	return out, nil
}

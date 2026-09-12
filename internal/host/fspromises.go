package host

import (
	"os"
	"path/filepath"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

func declareFSPromises(p *driver.Paserati) {
	vmInst := p.GetVM()
	p.DeclareModule("fs/promises", func(m *driver.ModuleBuilder) {
		m.AsyncFunction("readFile", func(path string, opts ...interface{}) (vm.Value, error) {
			fsTouch("read", path)
			b, err := os.ReadFile(path)
			if err != nil {
				return vm.Undefined, wrapFsErr(vmInst, "open", path, err)
			}
			// Same fix as fs.go's readFileSync (Round 88,
			// docs/real-node-plan.md): real Node's fs.promises.readFile
			// returns a Buffer by default, only decoding to a string
			// when an explicit encoding was given.
			encoding, hasEncoding := fsReadEncoding(opts)
			if !hasEncoding {
				return wrapBuffer(vmInst, b), nil
			}
			return vm.NewString(encodeBufferBytes(b, encoding)), nil
		})
		m.AsyncFunction("writeFile", func(path string, data vm.Value, _ ...interface{}) (interface{}, error) {
			// Same fix as fs.go's writeFileSync: accept a real
			// Buffer/TypedArray `data` argument (not just a string) and
			// write its raw bytes.
			return nil, wrapFsErr(vmInst, "open", path, os.WriteFile(path, valueToBytes(vmInst, data), 0644))
		})
		m.AsyncFunction("mkdir", func(path string, opts map[string]interface{}) (interface{}, error) {
			mkdirFn := os.Mkdir
			if mkdirRecursiveRequested(opts) {
				mkdirFn = os.MkdirAll
			}
			return nil, wrapFsErr(vmInst, "mkdir", path, mkdirFn(path, 0755))
		})
		m.AsyncFunction("readdir", func(path string, opts map[string]interface{}) ([]vm.Value, error) {
			fsTouch("readdir", path)
			entries, err := readdirEntries(vmInst, path, opts)
			if err != nil {
				return nil, wrapFsErr(vmInst, "scandir", path, err)
			}
			return entries, nil
		})
		m.AsyncFunction("stat", func(path string, _ ...interface{}) (*fsStats, error) {
			fsTouch("stat", path)
			info, err := os.Stat(path)
			if err != nil {
				return nil, wrapFsErr(vmInst, "stat", path, err)
			}
			return newFsStats(vmInst, info), nil
		})
		m.AsyncFunction("lstat", func(path string, _ ...interface{}) (*fsStats, error) {
			fsTouch("stat", path)
			info, err := os.Lstat(path)
			if err != nil {
				return nil, wrapFsErr(vmInst, "lstat", path, err)
			}
			return newFsStats(vmInst, info), nil
		})
		m.AsyncFunction("access", func(path string, _ ...interface{}) (interface{}, error) {
			fsTouch("stat", path)
			_, err := os.Stat(path)
			return nil, wrapFsErr(vmInst, "access", path, err)
		})
		m.AsyncFunction("unlink", func(path string) (interface{}, error) {
			return nil, wrapFsErr(vmInst, "unlink", path, os.Remove(path))
		})
		m.AsyncFunction("rm", func(path string, _ ...interface{}) (interface{}, error) {
			if err := os.RemoveAll(path); err != nil && !os.IsNotExist(err) {
				return nil, wrapFsErr(vmInst, "rm", path, err)
			}
			return nil, nil
		})
		m.AsyncFunction("realpath", func(path string, _ ...interface{}) (string, error) {
			resolved, err := filepath.EvalSymlinks(path)
			return resolved, wrapFsErr(vmInst, "realpath", path, err)
		})
		m.Default(nil)
	})
	_ = p.DeclareModuleAlias("node:fs/promises", "fs/promises")
}

// installFSPromisesAlias adds fs.promises, real Node's own long-standing
// alias: `require('fs').promises === require('fs/promises')` is `true`
// in real Node (the exact same kind of alias util.go's own
// TextEncoder/TextDecoder fix already covers for a different module -
// see that file's doc comment). Found the hard way chasing the real
// Bedrock investigation (docs/real-node-plan.md, round 98):
// @aws-sdk/token-providers' own dist-cjs/index.js does
// `const { writeFile } = node_fs.promises` (`node_fs` being
// `require('node:fs')`) at module top level - a real, unconditional
// destructure, not a hypothetical one - so a missing `.promises` threw
// "Cannot destructure 'undefined'" before any of the module's own
// token-provider logic ran.
//
// Reuses fs/promises's own, already-implemented module (declared just
// above, in the same file) via LoadModule + GetExportValues - the same
// mechanism cjs.go's own requireNative uses for every native
// require() - rather than a second, separate implementation: real
// Node's fs.promises genuinely is the same object fs/promises's own
// module.exports is, not a separate copy with its own behavior to keep
// in sync.
func installFSPromisesAlias(p *driver.Paserati) {
	vmInst := p.GetVM()
	if vmInst == nil {
		return
	}
	fsRec, err := p.LoadModule("fs", ".")
	if err != nil {
		return
	}
	promisesRec, err := p.LoadModule("fs/promises", ".")
	if err != nil {
		return
	}

	// fs/promises's own `m.Default(nil)` (declared just above in this
	// same file) already built its whole-module "default" object -
	// reusing it directly (rather than copying its named exports into a
	// second, new object) is what makes `fs.promises ===
	// require("fs/promises")` real, strict identity, matching real
	// Node's own guarantee here exactly, not just equivalent behavior.
	promisesExports := promisesRec.GetExportValues()
	promisesDefault, ok := promisesExports["default"]
	if !ok {
		return
	}

	fsExports := fsRec.GetExportValues()
	fsExports["promises"] = promisesDefault

	// "fs"'s own `m.Default(nil)` (fs.go) already built and cached a
	// snapshot "default" object at declare time, before this function
	// ever runs - the same one-time-snapshot behavior util.go's own
	// TextEncoder/TextDecoder fix has to work around (see its own
	// comment there). Mutating the named-exports map above alone
	// doesn't reach into that already-built object, so it has to be
	// rebuilt here too, or `import fs from "fs"`/CJS require("fs")'s
	// whole-module value (what real code actually destructures
	// `.promises` off of) would never see the addition.
	ns := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	for name, val := range fsExports {
		if name == "default" {
			continue
		}
		ns.SetOwn(name, val)
	}
	fsExports["default"] = vm.NewValueFromPlainObject(ns)
}

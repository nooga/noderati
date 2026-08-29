package host

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/errors"
	"github.com/nooga/paserati/pkg/vm"
)

var nativeRequireNames = map[string]bool{
	"fs": true, "path": true, "os": true, "util": true,
	"assert": true, "url": true, "querystring": true,
	"child_process": true,
}

type cjsLoader struct {
	p     *driver.Paserati
	cache map[string]vm.Value // resolved path → module object (or native namespace)
}

// StripShebang removes a leading #! line so CJS/JS files can be parsed.
func StripShebang(source string) string {
	if strings.HasPrefix(source, "#!") {
		if i := strings.IndexByte(source, '\n'); i >= 0 {
			return source[i+1:]
		}
		return ""
	}
	return source
}

// RunCJS executes CommonJS source with exports/require/module/__filename/__dirname.
func RunCJS(p *driver.Paserati, source, filename string) (vm.Value, []errors.PaseratiError) {
	return newCJSLoader(p).execFile(filename, StripShebang(source))
}

func newCJSLoader(p *driver.Paserati) *cjsLoader {
	return &cjsLoader{p: p, cache: make(map[string]vm.Value)}
}

func declareCJSInterop(p *driver.Paserati) {
	loader := newCJSLoader(p)
	vmInst := p.GetVM()
	proc, ok := vmInst.GetGlobal("process")
	if !ok {
		return
	}
	obj := proc.AsPlainObject()
	if obj == nil {
		return
	}
	obj.SetOwn("__noderatiCJSRequire", vm.NewNativeFunction(1, false, "__noderatiCJSRequire", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.Undefined, fmt.Errorf("cjsRequire: missing filename")
		}
		filename := args[0].ToString()
		data, err := os.ReadFile(filename)
		if err != nil {
			return vm.Undefined, err
		}
		val, errs := loader.execFile(filename, StripShebang(string(data)))
		if len(errs) > 0 {
			return vm.Undefined, fmt.Errorf("%s", errs[0].Error())
		}
		return val, nil
	}))
}

func (l *cjsLoader) execFile(filename, source string) (vm.Value, []errors.PaseratiError) {
	abs, err := filepath.Abs(filename)
	if err != nil {
		abs = filename
	}
	if cached, ok := l.cache[abs]; ok {
		return moduleExports(cached), nil
	}

	vmInst := l.p.GetVM()
	exportsVal := vm.NewObject(vmInst.ObjectPrototype)
	moduleVal := vm.NewObject(vmInst.ObjectPrototype)
	modObj := moduleVal.AsPlainObject()
	modObj.SetOwn("exports", exportsVal)
	moduleVal = vm.NewValueFromPlainObject(modObj)
	l.cache[abs] = moduleVal

	fromFile := abs
	requireFn := vm.NewNativeFunction(1, false, "require", func(args []vm.Value) (vm.Value, error) {
		spec := ""
		if len(args) > 0 {
			spec = args[0].ToString()
		}
		return l.require(spec, fromFile)
	})

	wrapped := "(function (exports, require, module, __filename, __dirname) {\n" + source + "\n})"
	fn, errs := l.p.RunScript(wrapped, abs)
	if len(errs) > 0 {
		return vm.Undefined, errs
	}

	_, err = vmInst.Call(fn, vm.Undefined, []vm.Value{
		exportsVal,
		requireFn,
		moduleVal,
		vm.String(abs),
		vm.String(filepath.Dir(abs)),
	})
	if err != nil {
		return vm.Undefined, []errors.PaseratiError{&errors.RuntimeError{Msg: formatCallError(err)}}
	}
	return moduleExports(moduleVal), nil
}

func formatCallError(err error) string {
	if ee, ok := err.(vm.ExceptionError); ok {
		return "VM exception: " + ee.GetExceptionValue().Inspect()
	}
	return err.Error()
}

func moduleExports(moduleVal vm.Value) vm.Value {
	obj := moduleVal.AsPlainObject()
	if obj == nil {
		return moduleVal
	}
	exp, ok := obj.GetOwn("exports")
	if !ok {
		return vm.Undefined
	}
	return exp
}

func (l *cjsLoader) require(specifier, fromFile string) (vm.Value, error) {
	spec := strings.TrimPrefix(specifier, "node:")
	if nativeRequireNames[spec] {
		return l.requireNative(spec)
	}

	resolved, err := l.resolveFile(specifier, fromFile)
	if err != nil {
		return vm.Undefined, fmt.Errorf("Cannot find module '%s'", specifier)
	}
	if cached, ok := l.cache[resolved]; ok {
		return moduleExports(cached), nil
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return vm.Undefined, fmt.Errorf("Cannot find module '%s'", specifier)
	}
	val, errs := l.execFile(resolved, StripShebang(string(data)))
	if len(errs) > 0 {
		return vm.Undefined, fmt.Errorf("%s", errs[0].Error())
	}
	return val, nil
}

func (l *cjsLoader) requireNative(spec string) (vm.Value, error) {
	cacheKey := "native://" + spec
	if cached, ok := l.cache[cacheKey]; ok {
		return cached, nil
	}

	rec, err := l.p.LoadModule(spec, ".")
	if err != nil {
		return vm.Undefined, fmt.Errorf("Cannot find module '%s'", spec)
	}
	if rec.GetError() != nil {
		return vm.Undefined, rec.GetError()
	}

	vals := rec.GetExportValues()
	var ns vm.Value
	if def, ok := vals["default"]; ok {
		ns = def
	} else {
		obj := vm.NewObject(l.p.GetVM().ObjectPrototype).AsPlainObject()
		for name, val := range vals {
			if name == "default" {
				continue
			}
			obj.SetOwn(name, val)
		}
		ns = vm.NewValueFromPlainObject(obj)
	}
	l.cache[cacheKey] = ns
	return ns, nil
}

func (l *cjsLoader) resolveFile(specifier, fromFile string) (string, error) {
	if strings.HasPrefix(specifier, "./") || strings.HasPrefix(specifier, "../") {
		return existingJSFile(filepath.Join(filepath.Dir(fromFile), specifier))
	}
	if strings.HasPrefix(specifier, "/") || filepath.IsAbs(specifier) {
		return existingJSFile(specifier)
	}

	pkgName, subpath := splitPackageSpecifier(specifier)
	pkgDir, err := findPackageDir(filepath.Dir(fromFile), pkgName)
	if err != nil {
		return "", err
	}
	return resolvePackageEntry(pkgDir, subpath)
}

func existingJSFile(target string) (string, error) {
	candidates := []string{
		target,
		target + ".js",
		target + ".cjs",
		filepath.Join(target, "index.js"),
		filepath.Join(target, "index.cjs"),
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
	return "", fmt.Errorf("not found: %s", target)
}

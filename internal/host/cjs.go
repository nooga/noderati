package host

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/errors"
	"github.com/nooga/paserati/pkg/vm"
)

var nativeRequireNames = map[string]bool{
	"fs": true, "path": true, "os": true, "util": true,
	"assert": true, "url": true, "querystring": true,
	"child_process": true, "readline": true, "tty": true, "process": true, "buffer": true,
	"events": true, "stream": true, "crypto": true, "undici": true,
	"fs/promises": true,
	"module": true, "worker_threads": true,
	"perf_hooks": true, "string_decoder": true,
	"stream/promises": true,
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
	obj.SetOwn("__noderatiCreateRequire", vm.NewNativeFunction(1, false, "__noderatiCreateRequire", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.Undefined, fmt.Errorf("createRequire: missing filename")
		}
		fromFile, err := resolveRequireFilename(args[0].ToString())
		if err != nil {
			return vm.Undefined, err
		}
		abs, err := filepath.Abs(fromFile)
		if err != nil {
			abs = fromFile
		}
		return loader.bindRequire(abs), nil
	}))
}

func resolveRequireFilename(filenameOrURL string) (string, error) {
	if strings.HasPrefix(filenameOrURL, "file://") {
		u, err := url.Parse(filenameOrURL)
		if err != nil {
			return "", err
		}
		if u.Scheme != "file" {
			return "", fmt.Errorf("createRequire: filename must be a file URL or path")
		}
		return filepath.FromSlash(u.Path), nil
	}
	return filenameOrURL, nil
}

func (l *cjsLoader) bindRequire(fromFile string) vm.Value {
	return vm.NewNativeFunction(1, false, "require", func(args []vm.Value) (vm.Value, error) {
		spec := ""
		if len(args) > 0 {
			spec = args[0].ToString()
		}
		return l.require(spec, fromFile)
	})
}

func (l *cjsLoader) execFile(filename, source string) (vm.Value, []errors.PaseratiError) {
	abs, err := filepath.Abs(filename)
	if err != nil {
		abs = filename
	}
	if cached, ok := l.cache[abs]; ok {
		return l.moduleExports(cached), nil
	}

	source = patchCJSSource(source, abs)

	vmInst := l.p.GetVM()
	exportsVal := vm.NewObject(vmInst.ObjectPrototype)
	moduleVal := vm.NewObject(vmInst.ObjectPrototype)
	modObj := moduleVal.AsPlainObject()
	modObj.SetOwn("exports", exportsVal)
	moduleVal = vm.NewValueFromPlainObject(modObj)
	l.cache[abs] = moduleVal

	fromFile := abs
	requireFn := l.bindRequire(fromFile)

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
	return l.moduleExports(moduleVal), nil
}

func formatCallError(err error) string {
	if ee, ok := err.(vm.ExceptionError); ok {
		return "VM exception: " + ee.GetExceptionValue().Inspect()
	}
	return err.Error()
}

func (l *cjsLoader) moduleExports(moduleVal vm.Value) vm.Value {
	obj := moduleVal.AsPlainObject()
	if obj == nil {
		return moduleVal
	}
	if getter, _, ok, _, _ := obj.GetOwnAccessor("exports"); ok && getter.IsCallable() {
		val, err := l.p.GetVM().Call(getter, moduleVal, nil)
		if err == nil {
			return val
		}
	}
	exp, ok := obj.Get("exports")
	if !ok {
		return vm.Undefined
	}
	return exp
}

func (l *cjsLoader) require(specifier, fromFile string) (vm.Value, error) {
	spec := strings.TrimPrefix(specifier, "node:")
	if spec == "process" {
		if proc, ok := l.p.GetVM().GetGlobal("process"); ok {
			return proc, nil
		}
	}
	if nativeRequireNames[spec] {
		return l.requireNative(spec)
	}

	resolved, err := l.resolveFile(specifier, fromFile)
	if err != nil {
		return vm.Undefined, fmt.Errorf("Cannot find module '%s'", specifier)
	}
	if cached, ok := l.cache[resolved]; ok {
		return l.moduleExports(cached), nil
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
		return vm.Undefined, fmt.Errorf("Cannot find module '%s'", spec)
	}

	vals := rec.GetExportValues()
	var ns vm.Value
	if def, ok := vals["default"]; ok && !def.IsUndefined() && def.Type() != vm.TypeNull {
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

// patchCJSSource applies small source transforms for known third-party CJS quirks.
func patchCJSSource(source, filename string) string {
	source = patchCJSSatisfiesKeyword(source)
	source = patchCJSClassSelfInstanceOf(source, filename)
	source = patchCJSClassStaticAccess(source, filename)
	return source
}

// patchCJSSatisfiesKeyword works around Paserati parsing `satisfies` as a TS keyword.
func patchCJSSatisfiesKeyword(source string) string {
	if !strings.Contains(source, "satisfies") {
		return source
	}
	reConst := regexp.MustCompile(`\bconst\s+satisfies\s*=`)
	source = reConst.ReplaceAllString(source, "const satisfiesFn =")

	reCall := regexp.MustCompile(`\bsatisfies\s*\(`)
	source = reCall.ReplaceAllString(source, "satisfiesFn(")

	source = strings.ReplaceAll(source, "module.exports = satisfies", "module.exports = satisfiesFn")
	reShorthand := regexp.MustCompile(`(?m)^(\s*)satisfies,\s*$`)
	source = reShorthand.ReplaceAllString(source, `${1}satisfies: satisfiesFn,`)
	return source
}

// patchCJSClassSelfInstanceOf rewrites `instanceof ClassName` self-checks to use
// module.exports — Paserati does not bind hoisted class names inside constructors.
func patchCJSClassSelfInstanceOf(source, filename string) string {
	switch filepath.Base(filename) {
	case "comparator.js":
		source = strings.ReplaceAll(source, "instanceof Comparator", "instanceof module.exports")
	case "range.js":
		source = strings.ReplaceAll(source, "instanceof Range", "instanceof module.exports")
	case "semver.js":
		source = strings.ReplaceAll(source, "instanceof SemVer", "instanceof module.exports")
	}
	return source
}

// patchCJSClassStaticAccess rewrites Class.static inside yaml's Directives
// constructor — Paserati does not bind the class name in that scope.
func patchCJSClassStaticAccess(source, filename string) string {
	if filepath.Base(filename) != "directives.js" {
		return source
	}
	source = strings.ReplaceAll(source, "Directives.defaultYaml =", "__ASSIGN_DIR_YAML__")
	source = strings.ReplaceAll(source, "Directives.defaultTags =", "__ASSIGN_DIR_TAGS__")
	source = strings.ReplaceAll(source, "new Directives(", "new module.exports.Directives(")
	source = strings.ReplaceAll(source, "Directives.defaultYaml", "module.exports.Directives.defaultYaml")
	source = strings.ReplaceAll(source, "Directives.defaultTags", "module.exports.Directives.defaultTags")
	source = strings.ReplaceAll(source, "__ASSIGN_DIR_YAML__", "Directives.defaultYaml =")
	source = strings.ReplaceAll(source, "__ASSIGN_DIR_TAGS__", "Directives.defaultTags =")
	return source
}

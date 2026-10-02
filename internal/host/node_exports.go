package host

//go:generate node gen_node_exports.mjs

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/lexer"
	"github.com/nooga/paserati/pkg/parser"
	"github.com/nooga/paserati/pkg/vm"
)

// Every named export real Node has for a builtin must be importable here
// too: ES module linking rejects `import { exec } from "node:child_process"`
// outright when the module doesn't export `exec`, so one gap anywhere in a
// package's import graph fails the whole package at load time, before any
// code runs - even when the missing function is never called. (Go-declared
// native modules are opaque to the linker and never had this problem; the
// JS shims do.)
//
// augmentShimExports appends, to a JS shim's source, a named export for
// each of Node's export names (node_exports_gen.go) the shim doesn't
// declare itself:
//   - the shim's default export's own property of that name, when it has
//     one (the named form was simply never written out);
//   - otherwise, for a function, a stub that throws
//     ERR_NODERATI_NOT_IMPLEMENTED when called or constructed, naming the
//     missing API;
//   - otherwise (a constant, a namespace), undefined.
//
// Only named exports are added. The default export object is left alone,
// so feature detection (`if (fs.glob)`) and CJS require() see exactly what
// noderati really implements.

var augmentedShims sync.Map // canonical name -> augmented source

func augmentShimExports(canonical, source string) string {
	nodeNames, ok := nodeBuiltinExports[canonical]
	if !ok || nodeNames == "" {
		return source
	}
	if cached, ok := augmentedShims.Load(canonical); ok {
		return cached.(string)
	}
	out := source
	if own, ok := shimExportedNames(source); ok {
		out = source + missingExportsSource(canonical, strings.Fields(nodeNames), own)
	}
	augmentedShims.Store(canonical, out)
	return out
}

func missingExportsSource(canonical string, nodeNames []string, own map[string]bool) string {
	var b strings.Builder
	n := 0
	for _, entry := range nodeNames {
		name := strings.TrimSuffix(entry, "()")
		if own[name] {
			continue
		}
		if n == 0 {
			fmt.Fprintf(&b, "\nimport * as __noderatiSelf from %q;\n", "node:"+canonical)
			b.WriteString(`function __noderatiMissing(name, isFunction) {
  const d = __noderatiSelf.default;
  if (d != null && name in Object(d)) return d[name];
  if (!isFunction) return undefined;
  const api = ` + fmt.Sprintf("%q", canonical+".") + ` + name;
  const stub = function () {
    const err = new Error(api + " is not implemented in noderati");
    err.code = "ERR_NODERATI_NOT_IMPLEMENTED";
    throw err;
  };
  return Object.defineProperty(stub, "name", { value: name });
}
`)
		}
		fmt.Fprintf(&b, "const __noderatiMissing%d = __noderatiMissing(%q, %t); export { __noderatiMissing%d as %s };\n",
			n, name, strings.HasSuffix(entry, "()"), n, name)
		n++
	}
	return b.String()
}

// shimExportedNames returns the export names a module source declares.
// ok is false when they can't be known (a parse error, or an
// `export * from` whose names would come from elsewhere), in which case
// the shim is served unchanged.
func shimExportedNames(source string) (map[string]bool, bool) {
	prog, errs := parser.NewParser(lexer.NewLexer(source)).ParseProgram()
	if len(errs) > 0 || prog == nil {
		return nil, false
	}
	names := map[string]bool{}
	for _, stmt := range prog.Statements {
		switch s := stmt.(type) {
		case *parser.ExportDefaultDeclaration:
			names["default"] = true
		case *parser.ExportAllDeclaration:
			if s.Exported == nil {
				return nil, false
			}
			names[exportNameValue(s.Exported)] = true
		case *parser.ExportNamedDeclaration:
			if s.Declaration != nil {
				for _, n := range declarationNames(s.Declaration) {
					names[n] = true
				}
			}
			for _, sp := range s.Specifiers {
				if es, ok := sp.(*parser.ExportNamedSpecifier); ok {
					names[exportNameValue(es.Exported)] = true
				}
			}
		}
	}
	return names, true
}

func declarationNames(decl parser.Statement) []string {
	switch d := decl.(type) {
	case *parser.ClassDeclaration:
		if d.Name != nil {
			return []string{d.Name.Value}
		}
	case *parser.ExpressionStatement:
		if fn, ok := d.Expression.(*parser.FunctionLiteral); ok && fn.Name != nil {
			return []string{fn.Name.Value}
		}
	}
	return parser.DeclaredNames(decl)
}

func exportNameValue(e parser.Expression) string {
	switch n := e.(type) {
	case *parser.Identifier:
		return n.Value
	case *parser.StringLiteral:
		return n.Value
	}
	return ""
}

// missingNodeExports reports, per builtin, Node's export names a module
// namespace object lacks - used by the parity test.
func missingNodeExports(module string, have map[string]bool) []string {
	var missing []string
	for _, entry := range strings.Fields(nodeBuiltinExports[module]) {
		if name := strings.TrimSuffix(entry, "()"); !have[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

// setNativeExport adds (or replaces) name on an already-loaded Go-declared
// module: both the named export and the default export object, so ESM
// named imports, default imports and CJS require() all see it. Used for
// values ModuleBuilder can't express (arbitrary vm.Values).
func setNativeExport(p *driver.Paserati, module, name string, value vm.Value) {
	rec, err := p.LoadModule(module, ".")
	if err != nil {
		return
	}
	exports := rec.GetExportValues()
	exports[name] = value
	if def, ok := exports["default"]; ok && def.Type() == vm.TypeObject {
		def.AsPlainObject().SetOwn(name, value)
	}
}

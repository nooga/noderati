package host

import (
	_ "embed"
	"strings"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// consoleImpl is Node's Console class and global-console behaviour
// (console.js), built on the inspect shim and loaded on first use.
//
//go:embed console.js
var consoleImpl string

// nodeConsoleShim is node:console: the global console itself (as in Node,
// require("console") === console), with its methods as named exports.
const nodeConsoleShim = `import { Console, installGlobalConsole } from "noderati-internal:console";
const console = installGlobalConsole(globalThis.console);
export default console;
export { Console };
export const log = console.log;
export const info = console.info;
export const debug = console.debug;
export const warn = console.warn;
export const error = console.error;
export const dir = console.dir;
export const dirxml = console.dirxml;
export const table = console.table;
export const trace = console.trace;
export const assert = console.assert;
export const clear = console.clear;
export const count = console.count;
export const countReset = console.countReset;
export const group = console.group;
export const groupCollapsed = console.groupCollapsed;
export const groupEnd = console.groupEnd;
export const time = console.time;
export const timeEnd = console.timeEnd;
export const timeLog = console.timeLog;
export const timeStamp = console.timeStamp;
export const profile = console.profile;
export const profileEnd = console.profileEnd;
export const context = console.context;
export const createTask = console.createTask;
`

func declareConsole() {
	registerJSShim("noderati-internal:console", consoleImpl)
	registerJSShim("console", nodeConsoleShim)
}

// globalConsoleMethods are the global console's methods, in Node's order.
var globalConsoleMethods = []string{
	"log", "info", "debug", "warn", "error", "dir", "time", "timeEnd", "timeLog", "trace", "assert", "clear",
	"count", "countReset", "group", "groupEnd", "table", "dirxml", "groupCollapsed",
	"profile", "profileEnd", "timeStamp", "context", "createTask",
}

// installGlobalConsole replaces the engine's built-in console methods with
// stubs that hand over to console.js on first use. Until then, plain
// string output (the common case) is written straight to process.stdout /
// process.stderr, so a script that only logs strings never loads the
// formatter.
func installGlobalConsole(p *driver.Paserati) {
	vmInst := p.GetVM()
	consoleVal, ok := vmInst.GetGlobal("console")
	if !ok || consoleVal.Type() != vm.TypeObject {
		return
	}
	obj := consoleVal.AsPlainObject()
	lazy := &lazyJSModule{p: p, spec: "noderati-internal:console"}
	installed := false
	install := func() error {
		if installed {
			return nil
		}
		fn, err := lazy.get("installGlobalConsole")
		if err != nil {
			return err
		}
		installed = true
		_, err = vmInst.Call(fn, vm.Undefined, []vm.Value{consoleVal})
		return err
	}
	for _, name := range globalConsoleMethods {
		name := name
		stub := vm.NewNativeFunction(0, true, name, func(args []vm.Value) (vm.Value, error) {
			if !installed {
				if stream, line, ok := plainConsoleLine(name, args); ok {
					return vm.Undefined, writeToProcessStream(vmInst, stream, line)
				}
				if err := install(); err != nil {
					return vm.Undefined, err
				}
			}
			fn, ok := obj.GetOwn(name)
			if !ok || !fn.IsCallable() {
				return vm.Undefined, nil
			}
			return vmInst.Call(fn, consoleVal, args)
		})
		obj.DeleteOwn(name)
		obj.SetOwn(name, stub)
	}
	getConsole := vm.NewNativeFunction(0, false, "get Console", func(_ []vm.Value) (vm.Value, error) {
		if err := install(); err != nil {
			return vm.Undefined, err
		}
		v, _ := obj.GetOwn("Console")
		return v, nil
	})
	setConsole := vm.NewNativeFunction(1, false, "set Console", func(args []vm.Value) (vm.Value, error) {
		obj.DeleteOwn("Console")
		obj.SetOwn("Console", argAt(args, 0))
		return vm.Undefined, nil
	})
	yes := true
	obj.DefineAccessorProperty("Console", getConsole, true, setConsole, true, &yes, &yes)
}

// plainConsoleLine formats a log/info/debug/warn/error call whose
// arguments are all strings with no format directives, exactly as
// util.format would, without loading it.
func plainConsoleLine(method string, args []vm.Value) (stream string, line string, ok bool) {
	switch method {
	case "log", "info", "debug", "dirxml":
		stream = "stdout"
	case "warn", "error":
		stream = "stderr"
	default:
		return "", "", false
	}
	parts := make([]string, len(args))
	for i, a := range args {
		if a.Type() != vm.TypeString {
			return "", "", false
		}
		parts[i] = a.ToString()
	}
	if len(parts) > 1 && strings.IndexByte(parts[0], '%') >= 0 {
		return "", "", false
	}
	return stream, strings.Join(parts, " ") + "\n", true
}

func writeToProcessStream(vmInst *vm.VM, stream, text string) error {
	proc, ok := vmInst.GetGlobal("process")
	if !ok {
		return nil
	}
	s, err := vmInst.GetProperty(proc, stream)
	if err != nil {
		return err
	}
	write, err := vmInst.GetProperty(s, "write")
	if err != nil || !write.IsCallable() {
		return err
	}
	_, err = vmInst.Call(write, s, []vm.Value{vm.NewString(text)})
	return err
}

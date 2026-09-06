package host

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/nooga/paserati/pkg/builtins"
	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/types"
	"github.com/nooga/paserati/pkg/vm"
	"golang.org/x/term"
)

// processStartTime anchors process.hrtime()'s monotonic clock. Real Node
// measures from an arbitrary fixed point too (not the Unix epoch) - only the
// deltas are meaningful. time.Since uses Go's monotonic clock reading, so
// this stays correct across NTP/wall-clock adjustments during the run.
var processStartTime = time.Now()

// ProcessInitializer is noderati’s process global. Do not grow Paserati’s stub.
type ProcessInitializer struct {
	argv []string
}

func NewProcessInitializer(argv []string) *ProcessInitializer {
	return &ProcessInitializer{argv: argv}
}

func (p *ProcessInitializer) Name() string { return "process" }

func (p *ProcessInitializer) Priority() int { return 300 }

func (p *ProcessInitializer) InitTypes(ctx *builtins.TypeContext) error {
	processType := types.NewObjectType().
		WithProperty("argv", &types.ArrayType{ElementType: types.String}).
		WithProperty("platform", types.String).
		WithProperty("arch", types.String).
		WithProperty("version", types.String).
		WithProperty("pid", types.Number).
		WithProperty("env", types.Any).
		WithProperty("execPath", types.String).
		WithProperty("execArgv", &types.ArrayType{ElementType: types.String}).
		WithProperty("cwd", types.NewSimpleFunction([]types.Type{}, types.String)).
		WithProperty("nextTick", types.NewSimpleFunction([]types.Type{types.Any}, types.Undefined)).
		WithProperty("exit", types.NewSimpleFunction([]types.Type{types.Number}, types.Undefined)).
		WithProperty("hrtime", types.NewOptionalFunction(
			[]types.Type{types.Any},
			&types.ArrayType{ElementType: types.Number},
			[]bool{true},
		))
	if err := ctx.DefineGlobal("process", processType); err != nil {
		return err
	}
	if err := ctx.DefineGlobal("structuredClone", types.NewSimpleFunction([]types.Type{types.Any}, types.Any)); err != nil {
		return err
	}
	if err := ctx.DefineGlobal("btoa", types.NewSimpleFunction([]types.Type{types.String}, types.String)); err != nil {
		return err
	}
	if err := ctx.DefineGlobal("atob", types.NewSimpleFunction([]types.Type{types.String}, types.String)); err != nil {
		return err
	}
	return ctx.DefineGlobal("global", types.Any)
}

func (p *ProcessInitializer) InitRuntime(ctx *builtins.RuntimeContext) error {
	vmInstance := ctx.VM

	argvArray := vm.NewArray()
	arr := argvArray.AsArray()
	for _, arg := range p.argv {
		arr.Append(vm.NewString(arg))
	}

	execPath := ""
	if len(p.argv) > 0 {
		execPath = p.argv[0]
	}

	envObj := vm.NewObject(vmInstance.ObjectPrototype).AsPlainObject()
	for _, env := range os.Environ() {
		for i := 0; i < len(env); i++ {
			if env[i] == '=' {
				envObj.SetOwn(env[:i], vm.NewString(env[i+1:]))
				break
			}
		}
	}

	stdoutObj := newStdioWritable(vmInstance, os.Stdout)
	cols, tty := stdoutColumnsAndTTY()
	stdoutObj.SetOwn("isTTY", tty)
	if cols != vm.Undefined {
		stdoutObj.SetOwn("columns", cols)
	}

	stderrObj := newStdioWritable(vmInstance, os.Stderr)
	stderrObj.SetOwn("isTTY", stdinOrFdIsTTY(os.Stderr))

	stdinObj := newStdinObject(vmInstance)

	processObj := newEventEmitterObject(vmInstance)
	processObj.SetOwn("argv", argvArray)
	processObj.SetOwn("execArgv", vm.NewArray())
	processObj.SetOwn("execPath", vm.NewString(execPath))
	processObj.SetOwn("platform", vm.NewString(runtime.GOOS))
	processObj.SetOwn("arch", vm.NewString(runtime.GOARCH))
	versionsObj := vm.NewObject(vmInstance.ObjectPrototype).AsPlainObject()
	versionsObj.SetOwn("node", vm.NewString("22.0.0"))
	processObj.SetOwn("version", vm.NewString("v22.0.0"))
	processObj.SetOwn("versions", vm.NewValueFromPlainObject(versionsObj))
	processObj.SetOwn("pid", vm.IntegerValue(int32(os.Getpid())))
	processObj.SetOwn("env", vm.NewValueFromPlainObject(envObj))
	processObj.SetOwn("stdout", vm.NewValueFromPlainObject(stdoutObj))
	processObj.SetOwn("stderr", vm.NewValueFromPlainObject(stderrObj))
	processObj.SetOwn("stdin", vm.NewValueFromPlainObject(stdinObj))
	processObj.SetOwn("title", vm.NewString("noderati"))
	processObj.SetOwn("exitCode", vm.IntegerValue(0))
	processObj.SetOwn("emitWarning", vm.NewNativeFunction(1, false, "emitWarning", func(args []vm.Value) (vm.Value, error) {
		return vm.Undefined, nil
	}))
	processObj.SetOwn("cwd", vm.NewNativeFunction(0, false, "cwd", func(args []vm.Value) (vm.Value, error) {
		cwd, err := os.Getwd()
		if err != nil {
			return vm.NewString(""), nil
		}
		return vm.NewString(cwd), nil
	}))
	rt := vmInstance.GetAsyncRuntime()
	processObj.SetOwn("nextTick", vm.NewNativeFunction(1, true, "nextTick", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 || !args[0].IsCallable() {
			return vm.Undefined, nil
		}
		fn := args[0]
		fnArgs := args[1:]
		rt.ScheduleNextTick(func() {
			_, _ = vmInstance.Call(fn, vm.Undefined, fnArgs)
		})
		return vm.Undefined, nil
	}))
	processObj.SetOwn("exit", vm.NewNativeFunction(1, false, "exit", func(args []vm.Value) (vm.Value, error) {
		code := 0
		if len(args) > 0 && args[0].IsNumber() {
			code = int(args[0].ToFloat())
		}
		os.Exit(code)
		return vm.Undefined, nil
	}))
	processObj.SetOwn("hrtime", vm.NewNativeFunction(1, false, "hrtime", func(args []vm.Value) (vm.Value, error) {
		return hrtimeValue(args), nil
	}))
	installProcessKill(vmInstance, processObj)
	startSignalBridge(vmInstance, processObj)

	if err := ctx.DefineGlobal("process", vm.NewValueFromPlainObject(processObj)); err != nil {
		return err
	}
	if err := ctx.DefineGlobal("structuredClone", structuredCloneFn(vmInstance)); err != nil {
		return err
	}
	if err := ctx.DefineGlobal("btoa", btoaFn()); err != nil {
		return err
	}
	if err := ctx.DefineGlobal("atob", atobFn()); err != nil {
		return err
	}
	return ctx.DefineGlobal("global", vm.NewValueFromPlainObject(vmInstance.GlobalObject))
}

// hrtimeValue implements process.hrtime()'s [seconds, nanoseconds] tuple.
// With no argument it returns the elapsed time since processStartTime; with
// a previous hrtime() result as args[0], it returns the delta since that
// reading, matching real Node's process.hrtime(time) API.
func hrtimeValue(args []vm.Value) vm.Value {
	elapsed := time.Since(processStartTime)
	if len(args) > 0 && args[0].IsArray() {
		if prev := args[0].AsArray(); prev != nil && prev.Length() >= 2 {
			prevSec := int64(prev.Get(0).ToFloat())
			prevNsec := int64(prev.Get(1).ToFloat())
			elapsed -= time.Duration(prevSec)*time.Second + time.Duration(prevNsec)*time.Nanosecond
			if elapsed < 0 {
				elapsed = 0
			}
		}
	}
	sec := elapsed / time.Second
	nsec := elapsed % time.Second

	result := vm.NewArray()
	arr := result.AsArray()
	arr.Append(vm.NumberValue(float64(sec)))
	arr.Append(vm.NumberValue(float64(nsec)))
	return result
}

func stdoutColumnsAndTTY() (columns vm.Value, isTTY vm.Value) {
	fd := int(os.Stdout.Fd())
	if !term.IsTerminal(fd) {
		return vm.Undefined, vm.False
	}
	w, _, err := term.GetSize(fd)
	if err != nil || w <= 0 {
		return vm.NumberValue(80), vm.True
	}
	return vm.NumberValue(float64(w)), vm.True
}

func stdinOrFdIsTTY(f *os.File) vm.Value {
	if term.IsTerminal(int(f.Fd())) {
		return vm.True
	}
	return vm.False
}

func newStdioWritable(vmInstance *vm.VM, out *os.File) *vm.PlainObject {
	obj := newEventEmitterObject(vmInstance)
	obj.SetOwn("writable", vm.True)
	obj.SetOwn("writableLength", vm.IntegerValue(0))
	obj.SetOwn("fd", vm.IntegerValue(int32(out.Fd())))
	rt := vmInstance.GetAsyncRuntime()
	obj.SetOwn("write", vm.NewNativeFunction(1, true, "write", func(args []vm.Value) (vm.Value, error) {
		chunk := ""
		if len(args) > 0 && !args[0].IsUndefined() && args[0].Type() != vm.TypeNull {
			chunk = args[0].ToString()
		}
		cb := writeCallback(args)
		if chunk != "" {
			_, _ = fmt.Fprint(out, chunk)
		}
		if cb.IsCallable() {
			fn := cb
			rt.ScheduleNextTick(func() {
				_, _ = vmInstance.Call(fn, vm.Undefined, nil)
			})
		}
		return vm.True, nil
	}))
	self := vm.NewValueFromPlainObject(obj)
	obj.SetOwn("cork", vm.NewNativeFunction(0, false, "cork", func(_ []vm.Value) (vm.Value, error) {
		return vm.Undefined, nil
	}))
	obj.SetOwn("uncork", vm.NewNativeFunction(0, false, "uncork", func(_ []vm.Value) (vm.Value, error) {
		return vm.Undefined, nil
	}))
	obj.SetOwn("end", vm.NewNativeFunction(0, true, "end", func(args []vm.Value) (vm.Value, error) {
		if len(args) > 0 && args[0].IsCallable() {
			_, _ = vmInstance.Call(args[0], vm.Undefined, nil)
		} else if len(args) > 0 {
			if writeFn, ok := obj.GetOwn("write"); ok && writeFn.IsCallable() {
				_, _ = vmInstance.Call(writeFn, self, args[:1])
			}
		}
		emitOnObject(vmInstance, obj, "finish")
		return self, nil
	}))
	return obj
}

func writeCallback(args []vm.Value) vm.Value {
	if len(args) >= 2 && args[1].IsCallable() {
		return args[1]
	}
	if len(args) >= 3 && args[2].IsCallable() {
		return args[2]
	}
	return vm.Undefined
}

func newStdinObject(vmInstance *vm.VM) *vm.PlainObject {
	obj := newEventEmitterObject(vmInstance)
	isTTY := stdinOrFdIsTTY(os.Stdin)
	obj.SetOwn("isTTY", isTTY)
	obj.SetOwn("fd", vm.IntegerValue(0))
	obj.SetOwn("isRaw", vm.False)
	obj.SetOwn("readable", vm.True)
	self := vm.NewValueFromPlainObject(obj)
	noopSelf := func(_ []vm.Value) (vm.Value, error) {
		return self, nil
	}
	obj.SetOwn("setEncoding", vm.NewNativeFunction(1, false, "setEncoding", noopSelf))
	obj.SetOwn("pause", vm.NewNativeFunction(0, false, "pause", noopSelf))
	// setRawMode used to only flip a JS-visible "isRaw" flag without ever
	// touching the real terminal - process.stdin.setRawMode(true) is what
	// every real TUI framework (Ink, pi-tui, etc.) calls before reading
	// keystrokes, and without term.MakeRaw actually being invoked, the OS
	// terminal driver stayed in cooked mode: line-buffered (so individual
	// keystrokes/arrow-key escape sequences never reach the process until
	// Enter is pressed) and echoing (so typed characters show up twice -
	// once from the raw OS echo, once from whatever the app itself tries
	// to render). Combined with resume() never starting a reader for TTY
	// stdin at all (see below), this meant a real interactive TUI's input
	// path was completely inert - confirmed directly (round 65,
	// docs/real-node-plan.md's Phase 5 section) via `expect`, which showed
	// typed text echoed raw by the pty itself and never reflected in the
	// TUI's own rendered editor box.
	//
	// Caveat (not a bug, but worth being explicit about): term.MakeRaw
	// mutates the *real* controlling terminal, and nothing here restores it
	// automatically if the process dies without calling setRawMode(false) -
	// a panic, an os.Exit from somewhere that skips cleanup, or a hard kill
	// all leave the user's shell stuck in raw mode (no echo, no line
	// editing) after this process exits. Real Node has the identical
	// footgun; well-behaved TUIs (pi-tui included, see its Terminal.stop())
	// restore raw mode themselves from their normal shutdown path and from
	// SIGTERM/SIGINT handlers, which now actually fire thanks to
	// startSignalBridge (signals.go) - so the common paths are covered by
	// the app itself, not by noderati. There's still no recovery for a
	// SIGKILL or an unhandled crash; that gap is inherent to raw mode on
	// any platform, not something this host can close.
	var rawState *term.State
	obj.SetOwn("setRawMode", vm.NewNativeFunction(1, false, "setRawMode", func(args []vm.Value) (vm.Value, error) {
		want := len(args) > 0 && args[0].IsTruthy()
		fd := int(os.Stdin.Fd())
		if want && rawState == nil {
			if st, err := term.MakeRaw(fd); err == nil {
				rawState = st
			}
		} else if !want && rawState != nil {
			_ = term.Restore(fd, rawState)
			rawState = nil
		}
		obj.SetOwn("isRaw", vm.BooleanValue(want))
		return self, nil
	}))
	obj.SetOwn("read", vm.NewNativeFunction(0, false, "read", func(_ []vm.Value) (vm.Value, error) {
		return vm.Null, nil
	}))

	var started atomic.Bool
	rt := vmInstance.GetAsyncRuntime()
	obj.SetOwn("resume", vm.NewNativeFunction(0, false, "resume", func(_ []vm.Value) (vm.Value, error) {
		// Previously skipped starting the reader entirely when stdin is a
		// TTY - meaning interactive keyboard input was never read at all,
		// TTY or not (see setRawMode's comment above for how this was
		// found and confirmed). os.Stdin.Read blocks correctly either way
		// (line-buffered without raw mode, byte-at-a-time with it), so
		// there's no TTY-specific reason to special-case this.
		if !started.CompareAndSwap(false, true) {
			return self, nil
		}
		rt.BeginExternalOp()
		go pumpProcessStdin(vmInstance, obj, rt)
		return self, nil
	}))
	return obj
}

func pumpProcessStdin(vmInstance *vm.VM, stream *vm.PlainObject, rt interface {
	EndExternalOp()
}) {
	defer rt.EndExternalOp()
	buf := make([]byte, 4096)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			scheduleEmit(vmInstance, stream, "data", vm.NewString(string(buf[:n])))
		}
		if err != nil {
			if err != io.EOF {
				scheduleEmit(vmInstance, stream, "error", vm.NewString(err.Error()))
			}
			scheduleEmit(vmInstance, stream, "end")
			return
		}
	}
}

// ProcessExitCode reads process.exitCode after the script event loop drains.
func ProcessExitCode(p *driver.Paserati) int {
	vmInst := p.GetVM()
	if vmInst == nil || vmInst.GlobalObject == nil {
		return 0
	}
	procVal, ok := vmInst.GlobalObject.GetOwn("process")
	if !ok {
		return 0
	}
	proc := procVal.AsPlainObject()
	if proc == nil {
		return 0
	}
	code, ok := proc.GetOwn("exitCode")
	if !ok || !code.IsNumber() {
		return 0
	}
	return int(code.ToFloat())
}

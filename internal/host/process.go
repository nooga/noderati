package host

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"sync/atomic"

	"github.com/nooga/paserati/pkg/builtins"
	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/types"
	"github.com/nooga/paserati/pkg/vm"
	"golang.org/x/term"
)

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
		WithProperty("exit", types.NewSimpleFunction([]types.Type{types.Number}, types.Undefined))
	if err := ctx.DefineGlobal("process", processType); err != nil {
		return err
	}
	if err := ctx.DefineGlobal("structuredClone", types.NewSimpleFunction([]types.Type{types.Any}, types.Any)); err != nil {
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

	if err := ctx.DefineGlobal("process", vm.NewValueFromPlainObject(processObj)); err != nil {
		return err
	}
	if err := ctx.DefineGlobal("structuredClone", structuredCloneFn(vmInstance)); err != nil {
		return err
	}
	return ctx.DefineGlobal("global", vm.NewValueFromPlainObject(vmInstance.GlobalObject))
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
	obj.SetOwn("setRawMode", vm.NewNativeFunction(1, false, "setRawMode", func(args []vm.Value) (vm.Value, error) {
		if len(args) > 0 {
			obj.SetOwn("isRaw", args[0])
		}
		return self, nil
	}))
	obj.SetOwn("read", vm.NewNativeFunction(0, false, "read", func(_ []vm.Value) (vm.Value, error) {
		return vm.Null, nil
	}))

	var started atomic.Bool
	rt := vmInstance.GetAsyncRuntime()
	obj.SetOwn("resume", vm.NewNativeFunction(0, false, "resume", func(_ []vm.Value) (vm.Value, error) {
		if isTTY.IsTruthy() || !started.CompareAndSwap(false, true) {
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

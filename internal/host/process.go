package host

import (
	"fmt"
	"os"
	"runtime"

	"github.com/nooga/paserati/pkg/builtins"
	"github.com/nooga/paserati/pkg/types"
	"github.com/nooga/paserati/pkg/vm"
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
		WithProperty("exit", types.NewSimpleFunction([]types.Type{types.Number}, types.Undefined))
	return ctx.DefineGlobal("process", processType)
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

	stdoutObj := vm.NewObject(vmInstance.ObjectPrototype).AsPlainObject()
	stdoutObj.SetOwn("write", vm.NewNativeFunction(1, false, "write", func(args []vm.Value) (vm.Value, error) {
		if len(args) > 0 {
			fmt.Print(args[0].ToString())
		}
		return vm.True, nil
	}))

	stderrObj := vm.NewObject(vmInstance.ObjectPrototype).AsPlainObject()
	stderrObj.SetOwn("write", vm.NewNativeFunction(1, false, "write", func(args []vm.Value) (vm.Value, error) {
		if len(args) > 0 {
			fmt.Fprint(os.Stderr, args[0].ToString())
		}
		return vm.True, nil
	}))

	processObj := vm.NewObject(vmInstance.ObjectPrototype).AsPlainObject()
	processObj.SetOwn("argv", argvArray)
	processObj.SetOwn("execArgv", vm.NewArray())
	processObj.SetOwn("execPath", vm.NewString(execPath))
	processObj.SetOwn("platform", vm.NewString(runtime.GOOS))
	processObj.SetOwn("arch", vm.NewString(runtime.GOARCH))
	processObj.SetOwn("version", vm.NewString("v22.0.0"))
	processObj.SetOwn("versions", vm.NewObject(vmInstance.ObjectPrototype))
	processObj.SetOwn("pid", vm.IntegerValue(int32(os.Getpid())))
	processObj.SetOwn("env", vm.NewValueFromPlainObject(envObj))
	processObj.SetOwn("stdout", vm.NewValueFromPlainObject(stdoutObj))
	processObj.SetOwn("stderr", vm.NewValueFromPlainObject(stderrObj))
	processObj.SetOwn("cwd", vm.NewNativeFunction(0, false, "cwd", func(args []vm.Value) (vm.Value, error) {
		cwd, err := os.Getwd()
		if err != nil {
			return vm.NewString(""), nil
		}
		return vm.NewString(cwd), nil
	}))
	processObj.SetOwn("exit", vm.NewNativeFunction(1, false, "exit", func(args []vm.Value) (vm.Value, error) {
		code := 0
		if len(args) > 0 && args[0].IsNumber() {
			code = int(args[0].ToFloat())
		}
		os.Exit(code)
		return vm.Undefined, nil
	}))

	return ctx.DefineGlobal("process", vm.NewValueFromPlainObject(processObj))
}

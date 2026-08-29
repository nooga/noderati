package host

import (
	"bytes"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

type spawnHandle struct {
	cmd *exec.Cmd
}

var (
	spawnHandles   sync.Map
	spawnHandleSeq atomic.Uint64
)

func installChildProcessNatives(p *driver.Paserati) {
	vmInst := p.GetVM()
	if vmInst == nil {
		return
	}
	gt, ok := vmInst.GetGlobal("globalThis")
	if !ok {
		return
	}
	obj := gt.AsPlainObject()
	if obj == nil {
		return
	}

	obj.SetOwn("__noderatiSpawnSync", vm.NewNativeFunction(2, false, "__noderatiSpawnSync", func(args []vm.Value) (vm.Value, error) {
		if len(args) < 1 {
			return vm.Undefined, nil
		}
		command, cmdArgs := parseSpawnCommandArgs(args[0], args[1])
		return runSpawnSync(command, cmdArgs), nil
	}))

	obj.SetOwn("__noderatiSpawn", vm.NewNativeFunction(3, false, "__noderatiSpawn", func(args []vm.Value) (vm.Value, error) {
		if len(args) < 1 {
			return vm.Undefined, nil
		}
		command := args[0].ToString()
		cmdArgs := stringArrayFromValue(args[1])
		return spawnProcess(vmInst, command, cmdArgs), nil
	}))
}

func parseSpawnCommandArgs(commandVal, argsVal vm.Value) (string, []string) {
	command := commandVal.ToString()
	return command, stringArrayFromValue(argsVal)
}

func stringArrayFromValue(v vm.Value) []string {
	if v == vm.Undefined || v == vm.Null {
		return nil
	}
	if arr := v.AsArray(); arr != nil {
		out := make([]string, arr.Length())
		for i := 0; i < arr.Length(); i++ {
			out[i] = arr.Get(i).ToString()
		}
		return out
	}
	return nil
}

func runSpawnSync(command string, args []string) vm.Value {
	cmd := exec.Command(command, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	status := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			status = ee.ExitCode()
		} else {
			status = 1
		}
	}
	obj := vm.NewObject(vm.Undefined).AsPlainObject()
	obj.SetOwn("status", vm.NumberValue(float64(status)))
	obj.SetOwn("stdout", vm.NewString(stdout.String()))
	obj.SetOwn("stderr", vm.NewString(stderr.String()))
	return vm.NewValueFromPlainObject(obj)
}

func spawnProcess(vmInst *vm.VM, command string, args []string) vm.Value {
	cmd := exec.Command(command, args...)
	stdinPipe, _ := cmd.StdinPipe()
	stdoutPipe, _ := cmd.StdoutPipe()
	stderrPipe, _ := cmd.StderrPipe()

	child := newEventEmitterObject(vmInst)
	stdoutStream := newReadableStream(vmInst)
	stderrStream := newReadableStream(vmInst)
	var stdinStream *vm.PlainObject
	stdinStream = newWritableStream(vmInst,
		func(writeArgs []vm.Value) (vm.Value, error) {
			if len(writeArgs) > 0 && stdinPipe != nil {
				_, _ = stdinPipe.Write([]byte(writeArgs[0].ToString()))
			}
			return vm.True, nil
		},
		func(endArgs []vm.Value) (vm.Value, error) {
			if len(endArgs) > 0 && stdinPipe != nil {
				_, _ = stdinPipe.Write([]byte(endArgs[0].ToString()))
			}
			if stdinPipe != nil {
				_ = stdinPipe.Close()
			}
			emitOnObject(vmInst, stdinStream, "finish")
			return vm.Undefined, nil
		},
	)

	handleID := spawnHandleSeq.Add(1)
	spawnHandles.Store(handleID, &spawnHandle{cmd: cmd})
	child.SetOwn("__noderatiSpawnHandle", vm.NumberValue(float64(handleID)))

	child.SetOwn("stdout", vm.NewValueFromPlainObject(stdoutStream))
	child.SetOwn("stderr", vm.NewValueFromPlainObject(stderrStream))
	child.SetOwn("stdin", vm.NewValueFromPlainObject(stdinStream))
	child.SetOwn("pid", vm.NumberValue(0))

	child.SetOwn("kill", vm.NewNativeFunction(0, true, "kill", func(_ []vm.Value) (vm.Value, error) {
		if h := loadSpawnHandle(child); h != nil && h.cmd.Process != nil {
			_ = h.cmd.Process.Kill()
		}
		return vm.Undefined, nil
	}))

	rt := vmInst.GetAsyncRuntime()
	rt.BeginExternalOp()

	if err := cmd.Start(); err != nil {
		rt.EndExternalOp()
		scheduleEmit(vmInst, child, "error", vm.NewString(err.Error()))
		scheduleEmit(vmInst, child, "close", vm.NumberValue(1))
		return vm.NewValueFromPlainObject(child)
	}
	child.SetOwn("pid", vm.IntegerValue(int32(cmd.Process.Pid)))

	go pumpSpawnStream(vmInst, stdoutPipe, stdoutStream, rt)
	go pumpSpawnStream(vmInst, stderrPipe, stderrStream, rt)
	go waitSpawnProcess(vmInst, child, cmd, rt)

	return vm.NewValueFromPlainObject(child)
}

func loadSpawnHandle(child *vm.PlainObject) *spawnHandle {
	idVal, ok := child.GetOwn("__noderatiSpawnHandle")
	if !ok || !idVal.IsNumber() {
		return nil
	}
	id := uint64(idVal.ToFloat())
	if h, ok := spawnHandles.Load(id); ok {
		return h.(*spawnHandle)
	}
	return nil
}

func pumpSpawnStream(vmInst *vm.VM, r io.ReadCloser, stream *vm.PlainObject, rt interface {
	EndExternalOp()
}) {
	defer func() { _ = r.Close() }()
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			chunk := vm.NewString(string(buf[:n]))
			scheduleEmit(vmInst, stream, "data", chunk)
		}
		if err != nil {
			if err != io.EOF {
				scheduleEmit(vmInst, stream, "error", vm.NewString(err.Error()))
			}
			scheduleEmit(vmInst, stream, "end")
			break
		}
	}
}

func waitSpawnProcess(vmInst *vm.VM, child *vm.PlainObject, cmd *exec.Cmd, rt interface {
	EndExternalOp()
}) {
	err := cmd.Wait()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = 1
			scheduleEmit(vmInst, child, "error", vm.NewString(err.Error()))
		}
	}
	scheduleEmit(vmInst, child, "exit", vm.NumberValue(float64(code)))
	scheduleEmit(vmInst, child, "close", vm.NumberValue(float64(code)))
	rt.EndExternalOp()
	if idVal, ok := child.GetOwn("__noderatiSpawnHandle"); ok && idVal.IsNumber() {
		spawnHandles.Delete(uint64(idVal.ToFloat()))
	}
}

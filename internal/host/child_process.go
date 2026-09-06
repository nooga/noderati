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
		var opts vm.Value = vm.Undefined
		if len(args) > 2 {
			opts = args[2]
		}
		return spawnProcess(vmInst, command, cmdArgs, opts), nil
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

// spawnOptions is what child_process.spawn's real Node signature accepts as
// its third argument, narrowed to the fields pi-agent-core's own shell-exec
// harness actually passes (cwd, env, detached) - see nodejs.js's
// AgentEnvironment.exec(), which spawns every real tool-call subprocess this
// way. Previously __noderatiSpawn only ever read args[0]/args[1] (command,
// argv) - args[2] (this whole options object) crossed the JS-to-Go boundary
// intact and was then silently dropped, so every real tool call ran in
// noderati's own cwd with noderati's own environment, and "detached" (which
// pi-agent-core relies on so its own killProcessTree can target the whole
// process group via a negative pid, both for its per-call timeout and for
// Escape-key cancellation) did nothing at all.
type spawnOptions struct {
	cwd      string
	env      []string // nil means "inherit noderati's own environment" (Go's default); non-nil replaces it entirely, matching Node's own spawn(): passing an env object replaces, never merges.
	detached bool
}

func parseSpawnOptions(v vm.Value) spawnOptions {
	var opts spawnOptions
	obj := v.AsPlainObject()
	if obj == nil {
		return opts
	}
	if cwdVal, ok := obj.GetOwn("cwd"); ok && !cwdVal.IsUndefined() && cwdVal.Type() != vm.TypeNull {
		opts.cwd = cwdVal.ToString()
	}
	if envVal, ok := obj.GetOwn("env"); ok {
		if envObj := envVal.AsPlainObject(); envObj != nil {
			keys := envObj.OwnKeys()
			env := make([]string, 0, len(keys))
			for _, k := range keys {
				if val, ok := envObj.GetOwn(k); ok {
					env = append(env, k+"="+val.ToString())
				}
			}
			opts.env = env
		}
	}
	if detachedVal, ok := obj.GetOwn("detached"); ok {
		opts.detached = detachedVal.IsTruthy()
	}
	return opts
}

func spawnProcess(vmInst *vm.VM, command string, args []string, optsVal vm.Value) vm.Value {
	opts := parseSpawnOptions(optsVal)
	cmd := exec.Command(command, args...)
	if opts.cwd != "" {
		cmd.Dir = opts.cwd
	}
	if opts.env != nil {
		cmd.Env = opts.env
	}
	if opts.detached {
		setDetached(cmd)
	}
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

	// cmd.Wait() must not run concurrently with the pipe reads below - Go's
	// own StdoutPipe/StderrPipe docs say so explicitly ("it is incorrect to
	// call Wait before all reads from the pipe have completed"), since Wait
	// closes the pipes itself once the process exits. Racing them (the
	// previous shape here: three independent goroutines, waitSpawnProcess's
	// cmd.Wait() free to return before either pump goroutine's Read() had
	// even run) is exactly what made a fast-exiting command like `echo
	// hello` flaky: "close" could get scheduled onto the VM's event loop
	// before "data" was, so an already-finished child's own output raced
	// its own completion notification - a real chunk of the same
	// reliability gap a tool call's output capture depends on, not just a
	// test flake. pumpDone makes waitSpawnProcess's cmd.Wait() wait for
	// both pumps to actually finish (and therefore for every "data"/"end"
	// scheduleEmit call to have already happened-before it) before it calls
	// cmd.Wait() and schedules "exit"/"close" - so those necessarily land
	// on the VM's single-threaded event-loop queue after the output events
	// they logically follow, not just usually after.
	var pumpDone sync.WaitGroup
	pumpDone.Add(2)
	go pumpSpawnStream(vmInst, stdoutPipe, stdoutStream, &pumpDone)
	go pumpSpawnStream(vmInst, stderrPipe, stderrStream, &pumpDone)
	go waitSpawnProcess(vmInst, child, cmd, rt, &pumpDone)

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

func pumpSpawnStream(vmInst *vm.VM, r io.ReadCloser, stream *vm.PlainObject, wg *sync.WaitGroup) {
	defer wg.Done()
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
}, pumpDone *sync.WaitGroup) {
	// Must not call cmd.Wait() until both pipe-reading goroutines have
	// actually finished draining stdout/stderr - see spawnProcess's own
	// comment on pumpDone for why racing this (the previous shape) was a
	// real, encountered bug and not just theoretical.
	pumpDone.Wait()
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

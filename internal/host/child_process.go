package host

import (
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

type spawnHandle struct {
	cmd *exec.Cmd
	mu  sync.Mutex
	// refed tracks real Node's per-child ref count (collapsed to a bool,
	// since child.ref()/.unref() are idempotent - a second call in the
	// same direction is a no-op, matching real Node): true from spawn
	// (a real Node child_process keeps the event loop alive by default)
	// until unref() flips it, ref() flips it back. Guarded by mu since
	// ref()/unref() run on the VM's own thread while waitSpawnProcess's
	// own exit-triggered EndExternalOp() (child_process.go) runs on a
	// background goroutine.
	refed bool
	// exited is set once waitSpawnProcess has already resolved this
	// child's own BeginExternalOp/EndExternalOp balance - a ref()/unref()
	// call that loses the race and arrives after that point would
	// otherwise create or cancel an external op nothing will ever
	// balance again (a real, lasting leak or double-decrement), so both
	// become no-ops once this is true, matching real Node's own harmless
	// no-op semantics for ref()/unref() on an already-exited child.
	exited bool
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

	obj.SetOwn("__noderatiSpawnSync", vm.NewNativeFunction(3, false, "__noderatiSpawnSync", func(args []vm.Value) (vm.Value, error) {
		if len(args) < 1 {
			return vm.Undefined, nil
		}
		return spawnSyncNative(vmInst, args[0].ToString(), stringArrayFromValue(argAt(args, 1)), argAt(args, 2)), nil
	}))

	obj.SetOwn("__noderatiFork", vm.NewNativeFunction(3, false, "__noderatiFork", func(args []vm.Value) (vm.Value, error) {
		if len(args) < 2 {
			return vm.Undefined, nil
		}
		return forkChild(vmInst, args[0].ToString(), stringArrayFromValue(args[1]), argAt(args, 2))
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
	stdio    [3]stdioSpec
}

// stdioSpec is one of options.stdio's first three slots: a pipe (the
// default), the parent's own fd (inherit, a number, or a stream with an
// fd), or /dev/null (ignore).
type stdioSpec struct {
	mode string // "pipe", "inherit", "ignore"
	file *os.File
}

func stdioFileForFd(fd int) *os.File {
	switch fd {
	case 0:
		return os.Stdin
	case 1:
		return os.Stdout
	case 2:
		return os.Stderr
	}
	if f, ok := fsFile(int64(fd)); ok {
		return f
	}
	return nil
}

func parseStdioEntry(v vm.Value, index int) stdioSpec {
	switch {
	case isNullish(v):
		return stdioSpec{mode: "pipe"}
	case v.IsString():
		switch v.ToString() {
		case "inherit":
			return stdioSpec{mode: "inherit", file: stdioFileForFd(index)}
		case "ignore":
			return stdioSpec{mode: "ignore"}
		default: // "pipe", "overlapped"
			return stdioSpec{mode: "pipe"}
		}
	case v.IsNumber():
		if f := stdioFileForFd(int(v.ToFloat())); f != nil {
			return stdioSpec{mode: "inherit", file: f}
		}
	case v.Type() == vm.TypeObject:
		// A stream: process.stdout & co. carry an fd.
		if fdVal, ok := objOption(v, "fd"); ok && fdVal.IsNumber() {
			if f := stdioFileForFd(int(fdVal.ToFloat())); f != nil {
				return stdioSpec{mode: "inherit", file: f}
			}
		}
		return stdioSpec{mode: "inherit", file: stdioFileForFd(index)}
	}
	return stdioSpec{mode: "pipe"}
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
	if envVal, ok := obj.GetOwn("env"); ok && envVal.Type() == vm.TypeObject {
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
	if stdioVal, ok := obj.GetOwn("stdio"); ok {
		if stdioVal.Type() == vm.TypeArray {
			arr := stdioVal.AsArray()
			for i := 0; i < 3; i++ {
				opts.stdio[i] = parseStdioEntry(argAt(arrayValues(arr), i), i)
			}
		} else if !isNullish(stdioVal) {
			for i := 0; i < 3; i++ {
				opts.stdio[i] = parseStdioEntry(stdioVal, i)
			}
		}
	}
	return opts
}

func arrayValues(arr *vm.ArrayObject) []vm.Value {
	out := make([]vm.Value, arr.Length())
	for i := range out {
		out[i] = arr.Get(i)
	}
	return out
}

// spawnExtras carries what fork() adds on top of a plain spawn: the IPC
// socket handed to the child as fd 3, and the env vars announcing it.
type spawnExtras struct {
	extraFiles []*os.File
	extraEnv   []string
}

func spawnProcess(vmInst *vm.VM, command string, args []string, optsVal vm.Value) vm.Value {
	child, _ := spawnProcessWith(vmInst, command, args, optsVal, spawnExtras{})
	return vm.NewValueFromPlainObject(child)
}

func spawnProcessWith(vmInst *vm.VM, command string, args []string, optsVal vm.Value, extras spawnExtras) (*vm.PlainObject, bool) {
	rt := vmInst.GetAsyncRuntime()
	opts := parseSpawnOptions(optsVal)
	cmd := exec.Command(command, args...)
	if opts.cwd != "" {
		cmd.Dir = opts.cwd
	}
	// An explicit environment (as libuv always passes) also stops os/exec
	// from adding PWD=<cwd> to it, which Node never does.
	cmd.Env = opts.env
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	if len(extras.extraEnv) > 0 {
		if cmd.Env == nil {
			cmd.Env = os.Environ()
		}
		cmd.Env = append(cmd.Env, extras.extraEnv...)
	}
	cmd.ExtraFiles = extras.extraFiles
	if opts.detached {
		setDetached(cmd)
	}

	child := newEventEmitterObject(vmInst)
	var stdinPipe io.WriteCloser
	var pumps []struct {
		r      io.ReadCloser
		stream *vm.PlainObject
	}
	streamFor := func(i int) vm.Value {
		spec := opts.stdio[i]
		switch spec.mode {
		case "inherit":
			switch i {
			case 0:
				cmd.Stdin = spec.file
			case 1:
				cmd.Stdout = spec.file
			case 2:
				cmd.Stderr = spec.file
			}
			return vm.Null
		case "ignore":
			return vm.Null
		}
		if i == 0 {
			stdinPipe, _ = cmd.StdinPipe()
			var stdinStream *vm.PlainObject
			stdinStream = newWritableStream(vmInst,
				// Found via real esbuild's own service protocol (a binary,
				// length-prefixed message format) under noderati: writing a
				// Buffer here used to go through .ToString(), which - like
				// Object.prototype.toString on a typed array - doesn't decode
				// bytes at all, so any byte outside plain ASCII silently
				// corrupted the write instead of being sent as-is (confirmed
				// directly: a 7-byte binary Buffer arrived on the other end as
				// 19 bytes). valueToBytes (net.go) already does this correctly
				// for a string, Buffer, or TypedArray alike.
				func(writeArgs []vm.Value) (vm.Value, error) {
					if len(writeArgs) > 0 && stdinPipe != nil {
						_, _ = stdinPipe.Write(valueToBytes(vmInst, writeArgs[0]))
					}
					return vm.True, nil
				},
				func(endArgs []vm.Value) (vm.Value, error) {
					if len(endArgs) > 0 && stdinPipe != nil {
						_, _ = stdinPipe.Write(valueToBytes(vmInst, endArgs[0]))
					}
					if stdinPipe != nil {
						_ = stdinPipe.Close()
					}
					emitOnObject(vmInst, stdinStream, "finish")
					return vm.Undefined, nil
				},
			)
			return vm.NewValueFromPlainObject(stdinStream)
		}
		var r io.ReadCloser
		if i == 1 {
			r, _ = cmd.StdoutPipe()
		} else {
			r, _ = cmd.StderrPipe()
		}
		stream := newReadableStream(vmInst)
		pumps = append(pumps, struct {
			r      io.ReadCloser
			stream *vm.PlainObject
		}{r, stream})
		return vm.NewValueFromPlainObject(stream)
	}
	stdinVal := streamFor(0)
	stdoutVal := streamFor(1)
	stderrVal := streamFor(2)

	handleID := spawnHandleSeq.Add(1)
	// refed: true - real Node's own default: a freshly spawned child
	// keeps the event loop alive until something calls .unref() on it.
	spawnHandles.Store(handleID, &spawnHandle{cmd: cmd, refed: true})
	child.SetOwn("__noderatiSpawnHandle", vm.NumberValue(float64(handleID)))

	child.SetOwn("stdin", stdinVal)
	child.SetOwn("stdout", stdoutVal)
	child.SetOwn("stderr", stderrVal)
	stdioArr := vm.NewArray()
	for _, v := range []vm.Value{stdinVal, stdoutVal, stderrVal} {
		stdioArr.AsArray().Append(v)
	}
	child.SetOwn("stdio", stdioArr)
	child.SetOwn("pid", vm.Undefined)
	child.SetOwn("killed", vm.False)
	child.SetOwn("exitCode", vm.Null)
	child.SetOwn("signalCode", vm.Null)
	child.SetOwn("spawnfile", vm.NewString(command))

	// kill([signal]) sends a real signal (SIGTERM by default, like Node)
	// rather than always SIGKILL, and reports whether it was delivered.
	child.SetOwn("kill", vm.NewNativeFunction(1, false, "kill", func(killArgs []vm.Value) (vm.Value, error) {
		sig := syscall.SIGTERM
		if len(killArgs) > 0 && !isNullish(killArgs[0]) {
			if killArgs[0].IsNumber() {
				sig = syscall.Signal(int(killArgs[0].ToFloat()))
			} else if s, ok := nodeSignals[killArgs[0].ToString()]; ok {
				sig = s
			} else {
				return vm.Undefined, newNodeTypeError(vmInst, "ERR_UNKNOWN_SIGNAL", "Unknown signal: "+killArgs[0].ToString())
			}
		}
		h := loadSpawnHandle(child)
		if h == nil || h.cmd.Process == nil {
			return vm.False, nil
		}
		if err := h.cmd.Process.Signal(sig); err != nil {
			return vm.False, nil
		}
		child.SetOwn("killed", vm.True)
		return vm.True, nil
	}))
	// unref/ref were missing entirely - found via real esbuild's own
	// ensureServiceIsRunning (lib/main.js), which calls child.unref()
	// unconditionally right after spawn so a long-running build service
	// doesn't itself keep the host process alive. A first pass shipped
	// these as permanent no-ops, reasoning that noderati's async runtime
	// keeps running as long as any pump/wait goroutine has an
	// outstanding BeginExternalOp regardless of this call - true, but
	// beside the point: that's exactly the counter real ref()/unref()
	// are supposed to opt this child in and out of, so a no-op silently
	// keeps a "backgrounded" build service alive forever instead of
	// letting the process exit once nothing else is left running, the
	// one thing esbuild's own unref() call exists to request. Real
	// semantics instead: unref() ends this child's own BeginExternalOp
	// (so it stops counting toward "keep the process alive") exactly
	// once, ref() begins a fresh one to undo that - both idempotent
	// (spawnHandle.refed collapses repeated calls in the same direction
	// to a no-op) and both no-ops once the child has already exited
	// (spawnHandle.exited - see waitSpawnProcess, which needs the same
	// mutex-guarded state to know whether it still owes an
	// EndExternalOp() call of its own or unref() already made one).
	childVal := vm.NewValueFromPlainObject(child)
	child.SetOwn("unref", vm.NewNativeFunction(0, false, "unref", func(_ []vm.Value) (vm.Value, error) {
		if h := loadSpawnHandle(child); h != nil {
			h.mu.Lock()
			if h.refed && !h.exited {
				h.refed = false
				rt.EndExternalOp()
			}
			h.mu.Unlock()
		}
		return childVal, nil
	}))
	child.SetOwn("ref", vm.NewNativeFunction(0, false, "ref", func(_ []vm.Value) (vm.Value, error) {
		if h := loadSpawnHandle(child); h != nil {
			h.mu.Lock()
			if !h.refed && !h.exited {
				h.refed = true
				rt.BeginExternalOp()
			}
			h.mu.Unlock()
		}
		return childVal, nil
	}))

	rt.BeginExternalOp()

	if err := cmd.Start(); err != nil {
		rt.EndExternalOp()
		spawnErr := wrapFsErr(vmInst, "spawn "+command, command, err)
		errVal := fsErrToVM(spawnErr)
		if errVal.IsUndefined() {
			errVal = newJSError(vmInst, err.Error())
		}
		scheduleEmit(vmInst, child, "error", errVal)
		scheduleEmit(vmInst, child, "close", vm.NumberValue(-2), vm.Null)
		return child, false
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
	pumpDone.Add(len(pumps))
	for _, pmp := range pumps {
		go pumpSpawnStream(vmInst, pmp.r, pmp.stream, &pumpDone)
	}
	go waitSpawnProcess(vmInst, child, cmd, rt, &pumpDone)

	return child, true
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
			// Real Node's child.stdout/stderr emit real Buffers by
			// default (only a string once setEncoding() has been
			// called) - this used to always emit a plain JS string,
			// the exact same bug pumpHTTPResponseBody (http.go) was
			// fixed for in round 101 (found there via a real AWS SDK
			// stream collector's Buffer.concat silently producing zero
			// bytes on string input). Found here independently, chasing
			// real esbuild's own service protocol (a binary,
			// length-prefixed stdout stream) under noderati - the exact
			// same silent-empty-output shape, not a thrown error.
			// wrapBuffer copies buf[:n] into a fresh ArrayBuffer, so
			// reusing buf across loop iterations is safe.
			scheduleEmit(vmInst, stream, "data", wrapBuffer(vmInst, buf[:n]))
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
	codeVal, sigVal := vm.NumberValue(0), vm.Null
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			if name := exitSignalName(ee); name != "" {
				codeVal, sigVal = vm.Null, vm.NewString(name)
			} else {
				codeVal = vm.NumberValue(float64(ee.ExitCode()))
			}
		} else {
			codeVal = vm.NumberValue(1)
			scheduleEmit(vmInst, child, "error", vm.NewString(err.Error()))
		}
	}
	vmInst.GetAsyncRuntime().ScheduleNextTick(func() {
		child.SetOwn("exitCode", codeVal)
		child.SetOwn("signalCode", sigVal)
		emitOnObject(vmInst, child, "exit", codeVal, sigVal)
		emitOnObject(vmInst, child, "close", codeVal, sigVal)
	})
	// Only balance the original spawn-time BeginExternalOp() if this
	// child is still ref'd - an unref() that already ran (see
	// spawnProcess's own unref/ref natives) already called EndExternalOp()
	// itself, and doing it again here would double-decrement the async
	// runtime's own external-op counter. Marking exited (under the same
	// mutex) before releasing it closes the other side of that race: a
	// ref()/unref() arriving after this point sees exited and does
	// nothing, rather than beginning or ending an op this function has
	// already accounted for one way or the other.
	if h := loadSpawnHandle(child); h != nil {
		h.mu.Lock()
		wasRefed := h.refed
		h.exited = true
		h.mu.Unlock()
		if wasRefed {
			rt.EndExternalOp()
		}
	} else {
		rt.EndExternalOp()
	}
	if idVal, ok := child.GetOwn("__noderatiSpawnHandle"); ok && idVal.IsNumber() {
		spawnHandles.Delete(uint64(idVal.ToFloat()))
	}
}

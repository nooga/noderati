package host

import (
	"bytes"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/nooga/paserati/pkg/vm"
)

// spawnSyncNative is child_process.spawnSync's binding, like Node's own
// spawn_sync.spawn: it runs file with args to completion honoring cwd,
// env, stdio, input, timeout/killSignal and maxBuffer, and returns the raw
// result ({ status, signal, output, pid } plus errorCode/errno on
// failure). child_process_shim.go turns that into Node's result object
// and errors.
func spawnSyncNative(vmInst *vm.VM, file string, args []string, optsVal vm.Value) vm.Value {
	opts := parseSpawnOptions(optsVal)
	cmd := exec.Command(file, args...)
	if opts.cwd != "" {
		cmd.Dir = opts.cwd
	}
	// An explicit environment (as libuv always passes) also stops os/exec
	// from adding PWD=<cwd> to it, which Node never does.
	cmd.Env = opts.env
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	if opts.detached {
		setDetached(cmd)
	}

	killSignal := syscall.SIGTERM
	if v, ok := objOption(optsVal, "killSignal"); ok && !isNullish(v) {
		if v.IsNumber() {
			killSignal = syscall.Signal(int(v.ToFloat()))
		} else if s, ok := nodeSignals[v.ToString()]; ok {
			killSignal = s
		}
	}
	maxBuffer := math.Inf(1)
	if v, ok := objOption(optsVal, "maxBuffer"); ok && v.IsNumber() {
		maxBuffer = v.ToFloat()
	}
	var timeout time.Duration
	if v, ok := objOption(optsVal, "timeout"); ok && v.IsNumber() && v.ToFloat() > 0 {
		timeout = time.Duration(v.ToFloat() * float64(time.Millisecond))
	}

	var mu sync.Mutex
	started, overflowed, timedOut := false, false, false
	kill := func() {
		if started && cmd.Process != nil {
			_ = cmd.Process.Signal(killSignal)
		}
	}

	switch spec := opts.stdio[0]; spec.mode {
	case "inherit":
		cmd.Stdin = spec.file
	case "ignore":
	default:
		var input []byte
		if v, ok := objOption(optsVal, "input"); ok && !isNullish(v) {
			input = valueToBytes(vmInst, v)
		}
		cmd.Stdin = bytes.NewReader(input)
	}
	var captured [3]*cappedBuffer
	for i := 1; i <= 2; i++ {
		var w io.Writer
		switch spec := opts.stdio[i]; spec.mode {
		case "inherit":
			w = spec.file
		case "ignore":
		default:
			captured[i] = &cappedBuffer{limit: maxBuffer, onOverflow: func() {
				mu.Lock()
				defer mu.Unlock()
				if !overflowed {
					overflowed = true
					kill()
				}
			}}
			w = captured[i]
		}
		if i == 1 {
			cmd.Stdout = w
		} else {
			cmd.Stderr = w
		}
	}

	res := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	mu.Lock()
	err := cmd.Start()
	if err == nil {
		started = true
	}
	mu.Unlock()
	if err != nil {
		code, errno := spawnErrCode(err)
		res.SetOwn("errorCode", vm.NewString(code))
		res.SetOwn("errno", vm.NumberValue(float64(errno)))
		res.SetOwn("status", vm.Null)
		res.SetOwn("signal", vm.Null)
		res.SetOwn("output", vm.Null)
		res.SetOwn("pid", vm.NumberValue(0))
		return vm.NewValueFromPlainObject(res)
	}
	if timeout > 0 {
		t := time.AfterFunc(timeout, func() {
			mu.Lock()
			defer mu.Unlock()
			timedOut = true
			kill()
		})
		defer t.Stop()
	}
	waitErr := cmd.Wait()

	status, signal := vm.NumberValue(0), vm.Null
	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			if name := exitSignalName(ee); name != "" {
				status, signal = vm.Null, vm.NewString(name)
			} else {
				status = vm.NumberValue(float64(ee.ExitCode()))
			}
		}
	}
	mu.Lock()
	switch {
	case timedOut:
		res.SetOwn("errorCode", vm.NewString("ETIMEDOUT"))
		res.SetOwn("errno", vm.NumberValue(-float64(syscall.ETIMEDOUT)))
	case overflowed:
		res.SetOwn("errorCode", vm.NewString("ENOBUFS"))
		res.SetOwn("errno", vm.NumberValue(-float64(syscall.ENOBUFS)))
	}
	mu.Unlock()
	output := vm.NewArray()
	output.AsArray().Append(vm.Null)
	for i := 1; i <= 2; i++ {
		if captured[i] == nil {
			output.AsArray().Append(vm.Null)
		} else {
			output.AsArray().Append(wrapBuffer(vmInst, captured[i].bytes()))
		}
	}
	res.SetOwn("status", status)
	res.SetOwn("signal", signal)
	res.SetOwn("output", output)
	res.SetOwn("pid", vm.NumberValue(float64(cmd.Process.Pid)))
	return vm.NewValueFromPlainObject(res)
}

// cappedBuffer collects a child's output and reports (once) when it
// grows past limit; like Node, what was read stays in the result.
type cappedBuffer struct {
	mu         sync.Mutex
	buf        bytes.Buffer
	limit      float64
	onOverflow func()
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	c.buf.Write(p)
	over := float64(c.buf.Len()) > c.limit
	c.mu.Unlock()
	if over {
		c.onOverflow()
	}
	return len(p), nil
}

func (c *cappedBuffer) bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Bytes()
}

// spawnErrCode maps a failed exec.Cmd.Start to libuv's code and negative
// errno: a command not found on PATH is ENOENT, as in Node.
func spawnErrCode(err error) (string, int) {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		if code, ok := errnoToCode[errno]; ok {
			return code, -int(errno)
		}
		return "UNKNOWN", -int(errno)
	}
	if errors.Is(err, exec.ErrNotFound) {
		return "ENOENT", -int(syscall.ENOENT)
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return "ENOENT", -int(syscall.ENOENT)
	}
	return "UNKNOWN", -1
}

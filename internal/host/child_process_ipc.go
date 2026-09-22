package host

import (
	"encoding/binary"
	"os"
	"strconv"
	"sync"
	"syscall"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// child_process_ipc.go implements the IPC channel behind
// child_process.fork(): a Unix socketpair whose child end is fd 3 in the
// child, announced through NODE_CHANNEL_FD/NODE_CHANNEL_SERIALIZATION_MODE
// exactly as real Node does. The wire formats are Node's own
// (lib/internal/child_process/serialization.js): "json" is one
// JSON.stringify'd message per line, "advanced" is a 4-byte big-endian
// length followed by a v8.serialize payload. The same code drives both
// ends: the ChildProcess object in the parent, process in the child.

// ipcDrivers lets the "advanced" serialization reach the JS v8 module.
var ipcDrivers sync.Map // *vm.VM -> *driver.Paserati

func registerIPCDriver(p *driver.Paserati) {
	if vmInst := p.GetVM(); vmInst != nil {
		ipcDrivers.Store(vmInst, p)
	}
}

type ipcWrite struct {
	data []byte
	done func(error)
}

type ipcChannel struct {
	vmInst    *vm.VM
	target    *vm.PlainObject
	file      *os.File
	fd        int
	mode      string
	hold      *keepAlive
	writes    chan ipcWrite
	connected bool
	started   bool
	explicit  bool // ref()/unref() was called: listener counting no longer applies
	isChild   bool
	pending   []byte
}

func (c *ipcChannel) jsonCall(method string, arg vm.Value) (vm.Value, error) {
	jsonVal, ok := c.vmInst.GetGlobal("JSON")
	if !ok {
		return vm.Undefined, nil
	}
	fn, err := c.vmInst.GetProperty(jsonVal, method)
	if err != nil {
		return vm.Undefined, err
	}
	return c.vmInst.Call(fn, jsonVal, []vm.Value{arg})
}

func (c *ipcChannel) v8Call(method string, arg vm.Value) (vm.Value, error) {
	pv, ok := ipcDrivers.Load(c.vmInst)
	if !ok {
		return vm.Undefined, newNodeTypeError(c.vmInst, "ERR_IPC_UNSUPPORTED", "advanced serialization is unavailable")
	}
	rec, err := pv.(*driver.Paserati).LoadModule("v8", ".")
	if err != nil {
		return vm.Undefined, err
	}
	fn, ok := rec.GetExportValues()[method]
	if !ok {
		if _, _, runErrs := pv.(*driver.Paserati).RunModuleWithValue("v8"); len(runErrs) == 0 {
			fn, ok = rec.GetExportValues()[method]
		}
	}
	if !ok {
		return vm.Undefined, newNodeTypeError(c.vmInst, "ERR_IPC_UNSUPPORTED", "v8."+method+" is unavailable")
	}
	return c.vmInst.Call(fn, vm.Undefined, []vm.Value{arg})
}

func (c *ipcChannel) encode(message vm.Value) ([]byte, error) {
	if c.mode == "advanced" {
		buf, err := c.v8Call("serialize", message)
		if err != nil {
			return nil, err
		}
		payload := valueToBytes(c.vmInst, buf)
		out := make([]byte, 4+len(payload))
		binary.BigEndian.PutUint32(out, uint32(len(payload)))
		copy(out[4:], payload)
		return out, nil
	}
	s, err := c.jsonCall("stringify", message)
	if err != nil {
		return nil, err
	}
	return []byte(s.ToString() + "\n"), nil
}

func (c *ipcChannel) startWriter() {
	go func() {
		rt := c.vmInst.GetAsyncRuntime()
		for w := range c.writes {
			_, err := c.file.Write(w.data)
			if w.done != nil {
				done, werr := w.done, err
				rt.ScheduleNextTick(func() { done(werr) })
			}
			// Balances send()'s BeginExternalOp: like a libuv write
			// request, a message in flight keeps the loop alive.
			rt.EndExternalOp()
		}
		_ = c.file.Close()
	}()
}

// startReader begins pulling bytes off the socket. The child starts it
// lazily, once something listens: until then messages wait in the
// kernel buffer rather than being emitted with nobody to receive them.
func (c *ipcChannel) startReader() {
	if c.started {
		return
	}
	c.started = true
	rt := c.vmInst.GetAsyncRuntime()
	go func() {
		buf := make([]byte, 64*1024)
		for {
			n, err := c.file.Read(buf)
			if n > 0 {
				chunk := append([]byte(nil), buf[:n]...)
				rt.ScheduleNextTick(func() { c.onData(chunk) })
			}
			if err != nil {
				rt.ScheduleNextTick(c.onEOF)
				return
			}
		}
	}()
}

// deliver emits in the same tick onData runs in (itself a tick after the
// read): a message that arrived before the peer's EOF must be emitted
// before onEOF - queued behind it in the same order - marks the channel
// disconnected, or a send()-then-disconnect() from the peer would be lost.
func (c *ipcChannel) deliver(message vm.Value) {
	emitOnObject(c.vmInst, c.target, "message", message)
}

func (c *ipcChannel) onData(chunk []byte) {
	if !c.connected {
		return
	}
	c.pending = append(c.pending, chunk...)
	for {
		if c.mode == "advanced" {
			if len(c.pending) < 4 {
				return
			}
			size := int(binary.BigEndian.Uint32(c.pending))
			if len(c.pending) < 4+size {
				return
			}
			payload := append([]byte(nil), c.pending[4:4+size]...)
			c.pending = c.pending[4+size:]
			msg, err := c.v8Call("deserialize", wrapBuffer(c.vmInst, payload))
			if err != nil {
				reportUncaughtCallbackException(c.vmInst, err)
				continue
			}
			c.deliver(msg)
			continue
		}
		i := indexByte(c.pending, '\n')
		if i < 0 {
			return
		}
		line := string(c.pending[:i])
		c.pending = c.pending[i+1:]
		msg, err := c.jsonCall("parse", vm.NewString(line))
		if err != nil {
			reportUncaughtCallbackException(c.vmInst, err)
			continue
		}
		c.deliver(msg)
	}
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

func (c *ipcChannel) onEOF() {
	c.shutdown()
}

// shutdown marks the channel disconnected and emits 'disconnect' on the
// next tick, as Node's own finish() does.
func (c *ipcChannel) shutdown() {
	if !c.connected {
		return
	}
	c.connected = false
	c.target.SetOwn("connected", vm.False)
	close(c.writes)
	c.hold.release()
	c.vmInst.GetAsyncRuntime().ScheduleNextTick(func() {
		emitOnObject(c.vmInst, c.target, "disconnect")
	})
}

func (c *ipcChannel) channelClosedError(code, message string) vm.Value {
	errVal := newJSError(c.vmInst, message)
	if obj := errVal.AsPlainObject(); obj != nil {
		obj.SetOwn("code", vm.NewString(code))
	}
	return errVal
}

// updateRef applies the child's listener-counted ref, the same rule as
// Node's _forkChild: the channel keeps the process alive only while
// 'message' or 'disconnect' listeners exist, unless ref()/unref() was
// called explicitly.
func (c *ipcChannel) updateRef() {
	if c.explicit || !c.connected {
		return
	}
	if listenerCount(c.target, "message")+listenerCount(c.target, "disconnect") > 0 {
		c.hold.ref()
		c.startReader()
	} else {
		c.hold.unref()
	}
}

func setupIPCChannel(vmInst *vm.VM, target *vm.PlainObject, file *os.File, fd int, mode string, isChild bool) *ipcChannel {
	c := &ipcChannel{
		vmInst: vmInst, target: target, file: file, fd: fd, mode: mode,
		hold:      newKeepAlive(vmInst, !isChild),
		writes:    make(chan ipcWrite, 1024),
		connected: true,
		isChild:   isChild,
	}
	c.startWriter()
	target.SetOwn("connected", vm.True)

	target.SetOwn("send", vm.NewNativeFunction(4, true, "send", func(args []vm.Value) (vm.Value, error) {
		cb := vm.Undefined
		if len(args) > 1 && args[len(args)-1].IsCallable() {
			cb = args[len(args)-1]
		}
		if len(args) == 0 || args[0].IsUndefined() {
			return vm.Undefined, newNodeTypeError(vmInst, "ERR_MISSING_ARGS", `The "message" argument must be specified`)
		}
		if !c.connected {
			errVal := c.channelClosedError("ERR_IPC_CHANNEL_CLOSED", "Channel closed")
			rt := vmInst.GetAsyncRuntime()
			if cb.IsCallable() {
				rt.ScheduleNextTick(func() { _, _ = vmInst.Call(cb, vm.Undefined, []vm.Value{errVal}) })
			} else {
				rt.ScheduleNextTick(func() { emitOnObject(vmInst, target, "error", errVal) })
			}
			return vm.False, nil
		}
		data, err := c.encode(args[0])
		if err != nil {
			return vm.Undefined, err
		}
		w := ipcWrite{data: data}
		if cb.IsCallable() {
			w.done = func(werr error) {
				arg := vm.Null
				if werr != nil {
					arg = newJSError(vmInst, werr.Error())
				}
				if _, err := vmInst.Call(cb, vm.Undefined, []vm.Value{arg}); err != nil {
					reportUncaughtCallbackException(vmInst, err)
				}
			}
		}
		vmInst.GetAsyncRuntime().BeginExternalOp()
		c.writes <- w
		return vm.True, nil
	}))
	target.SetOwn("disconnect", vm.NewNativeFunction(0, false, "disconnect", func(_ []vm.Value) (vm.Value, error) {
		if !c.connected {
			errVal := c.channelClosedError("ERR_IPC_DISCONNECTED", "IPC channel is already disconnected")
			vmInst.GetAsyncRuntime().ScheduleNextTick(func() { emitOnObject(vmInst, target, "error", errVal) })
			return vm.Undefined, nil
		}
		c.shutdown()
		return vm.Undefined, nil
	}))

	control := newEventEmitterObject(vmInst)
	control.SetOwn("fd", vm.NumberValue(float64(fd)))
	control.SetOwn("ref", vm.NewNativeFunction(0, false, "ref", func(_ []vm.Value) (vm.Value, error) {
		c.explicit = true
		c.hold.ref()
		c.startReader()
		return vm.Undefined, nil
	}))
	control.SetOwn("unref", vm.NewNativeFunction(0, false, "unref", func(_ []vm.Value) (vm.Value, error) {
		c.explicit = true
		c.hold.unref()
		return vm.Undefined, nil
	}))
	target.SetOwn("channel", vm.NewValueFromPlainObject(control))

	if isChild {
		// Re-evaluate the listener-counted ref whenever process gains or
		// loses listeners (Node does this via 'newListener'/'removeListener').
		for _, name := range []string{"on", "addListener", "once", "prependListener", "prependOnceListener", "off", "removeListener", "removeAllListeners"} {
			orig, ok := target.GetOwn(name)
			if !ok || !orig.IsCallable() {
				continue
			}
			fnName := name
			target.SetOwn(fnName, vm.NewNativeFunction(2, true, fnName, func(args []vm.Value) (vm.Value, error) {
				res, err := vmInst.Call(orig, vm.NewValueFromPlainObject(target), args)
				c.updateRef()
				return res, err
			}))
		}
	} else {
		c.startReader()
	}
	return c
}

// ipcFile wraps an IPC socket fd in non-blocking mode so Go's poller owns
// it: only then does Close() on disconnect interrupt this side's pending
// Read and actually close the socket (a blocking fd's close is deferred
// until that Read returns, so the peer would never see EOF).
func ipcFile(fd int, name string) *os.File {
	_ = syscall.SetNonblock(fd, true)
	return os.NewFile(uintptr(fd), name)
}

// setupChildIPCFromEnv wires process.send & co. when this process was
// started by fork(), then removes the NODE_CHANNEL_* variables from
// process.env as real Node does.
func setupChildIPCFromEnv(vmInst *vm.VM, processObj, envObj *vm.PlainObject) {
	fdStr := os.Getenv("NODE_CHANNEL_FD")
	if fdStr == "" {
		return
	}
	fd, err := strconv.Atoi(fdStr)
	if err != nil || fd < 0 {
		return
	}
	mode := os.Getenv("NODE_CHANNEL_SERIALIZATION_MODE")
	if mode != "advanced" {
		mode = "json"
	}
	envObj.DeleteOwn("NODE_CHANNEL_FD")
	envObj.DeleteOwn("NODE_CHANNEL_SERIALIZATION_MODE")
	_ = os.Unsetenv("NODE_CHANNEL_FD")
	_ = os.Unsetenv("NODE_CHANNEL_SERIALIZATION_MODE")
	syscall.CloseOnExec(fd)
	setupIPCChannel(vmInst, processObj, ipcFile(fd, "ipc"), fd, mode, true)
}

// forkChild is the Go side of child_process.fork(): spawn with a fresh
// socketpair whose child end becomes fd 3.
func forkChild(vmInst *vm.VM, execPath string, argv []string, optsVal vm.Value) (vm.Value, error) {
	mode := "json"
	if optsVal.Type() == vm.TypeObject {
		if v, ok := objOption(optsVal, "serialization"); ok && v.IsString() && v.ToString() == "advanced" {
			mode = "advanced"
		}
	}
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return vm.Undefined, err
	}
	syscall.CloseOnExec(fds[0])
	syscall.CloseOnExec(fds[1])
	parentEnd := ipcFile(fds[0], "ipc-parent")
	childEnd := os.NewFile(uintptr(fds[1]), "ipc-child")
	child, started := spawnProcessWith(vmInst, execPath, argv, optsVal, spawnExtras{
		extraFiles: []*os.File{childEnd},
		extraEnv:   []string{"NODE_CHANNEL_FD=3", "NODE_CHANNEL_SERIALIZATION_MODE=" + mode},
	})
	_ = childEnd.Close()
	c := setupIPCChannel(vmInst, child, parentEnd, fds[0], mode, false)
	if !started {
		c.shutdown()
	}
	return vm.NewValueFromPlainObject(child), nil
}

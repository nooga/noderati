package host

import (
	"github.com/nooga/paserati/pkg/vm"
)

func newEventEmitterObject(vmInst *vm.VM) *vm.PlainObject {
	obj := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	eventsTable := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	obj.SetOwn("_events", vm.NewValueFromPlainObject(eventsTable))

	obj.SetOwn("on", vm.NewNativeFunction(2, false, "on", func(args []vm.Value) (vm.Value, error) {
		return addListener(vmInst, obj, args[0].ToString(), args[1], false, false), nil
	}))
	// addListener is a plain alias for on - real Node's EventEmitter (which
	// process, streams, etc. all really are) exposes both under the same
	// behavior; only the name differs. Missing this specifically broke
	// pi-coding-agent's TUI startup (round 65, docs/real-node-plan.md's
	// Phase 5 section): registerSignalHandlers() calls
	// process.prependListener, which - like this one, before this fix -
	// didn't exist, throwing a TypeError inside an async init() that never
	// surfaced as a visible error, hanging the whole process with zero
	// output instead.
	obj.SetOwn("addListener", vm.NewNativeFunction(2, false, "addListener", func(args []vm.Value) (vm.Value, error) {
		return addListener(vmInst, obj, args[0].ToString(), args[1], false, false), nil
	}))
	obj.SetOwn("once", vm.NewNativeFunction(2, false, "once", func(args []vm.Value) (vm.Value, error) {
		return addListener(vmInst, obj, args[0].ToString(), args[1], true, false), nil
	}))
	obj.SetOwn("prependListener", vm.NewNativeFunction(2, false, "prependListener", func(args []vm.Value) (vm.Value, error) {
		return addListener(vmInst, obj, args[0].ToString(), args[1], false, true), nil
	}))
	obj.SetOwn("prependOnceListener", vm.NewNativeFunction(2, false, "prependOnceListener", func(args []vm.Value) (vm.Value, error) {
		return addListener(vmInst, obj, args[0].ToString(), args[1], true, true), nil
	}))
	obj.SetOwn("off", vm.NewNativeFunction(2, false, "off", func(args []vm.Value) (vm.Value, error) {
		return removeListener(obj, args[0].ToString(), args[1]), nil
	}))
	obj.SetOwn("removeListener", vm.NewNativeFunction(2, false, "removeListener", func(args []vm.Value) (vm.Value, error) {
		return removeListener(obj, args[0].ToString(), args[1]), nil
	}))
	obj.SetOwn("removeAllListeners", vm.NewNativeFunction(1, false, "removeAllListeners", func(args []vm.Value) (vm.Value, error) {
		return removeAllListeners(obj, args), nil
	}))
	obj.SetOwn("listenerCount", vm.NewNativeFunction(1, false, "listenerCount", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.NumberValue(0), nil
		}
		return vm.NumberValue(float64(listenerCount(obj, args[0].ToString()))), nil
	}))
	obj.SetOwn("listeners", vm.NewNativeFunction(1, false, "listeners", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.NewArray(), nil
		}
		return listenersOf(obj, args[0].ToString()), nil
	}))
	obj.SetOwn("emit", vm.NewNativeFunction(1, true, "emit", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.False, nil
		}
		event := args[0].ToString()
		payload := args[1:]
		return vm.BooleanValue(emitOnObject(vmInst, obj, event, payload...)), nil
	}))

	return obj
}

func newReadableStream(vmInst *vm.VM) *vm.PlainObject {
	obj := newEventEmitterObject(vmInst)
	obj.SetOwn("readable", vm.True)
	obj.SetInternalSlots(&pendingStreamData{})
	self := vm.NewValueFromPlainObject(obj)
	// setEncoding was missing entirely - real Node's Readable always has it,
	// and pi-agent-core's own real tool-call harness (nodejs.js's
	// AgentEnvironment.exec(), the code path behind every bash-tool
	// invocation) calls child.stdout.setEncoding("utf8")/
	// child.stderr?.setEncoding("utf8") unconditionally, immediately after
	// spawn, with no guarding `?.` before the call itself - so this being
	// undefined threw a synchronous TypeError inside every single real tool
	// call's Promise executor, rejecting exec()'s whole promise before a
	// single byte of output was ever read. There's no actual encoding
	// switch to perform: this stream's own "data" events already always
	// carry JS strings (pumpSpawnStream in child_process.go decodes with
	// vm.NewString, never emits a Buffer), so this is a real no-op that
	// exists to not be missing, not a stub standing in for unbuilt
	// behavior - matches Node's own fluent `return this`.
	obj.SetOwn("setEncoding", vm.NewNativeFunction(1, false, "setEncoding", func(_ []vm.Value) (vm.Value, error) {
		return self, nil
	}))
	// destroy was also missing - called on child.stdout/stderr elsewhere in
	// this same real install (cleanup paths, not the crash above) whenever
	// a tool call is aborted or its output stream needs to be torn down
	// early. A real Node destroy() emits "close" (and "error" first, if
	// given one) rather than doing nothing, so listeners relying on that
	// event to know a stream is done still fire.
	obj.SetOwn("destroy", vm.NewNativeFunction(0, true, "destroy", func(args []vm.Value) (vm.Value, error) {
		if len(args) > 0 && !args[0].IsUndefined() {
			scheduleEmit(vmInst, obj, "error", args[0])
		}
		scheduleEmit(vmInst, obj, "close")
		return self, nil
	}))
	obj.SetOwn("pipe", vm.NewNativeFunction(1, false, "pipe", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.Undefined, nil
		}
		dest := args[0]
		// Get, not GetOwn: found the hard way chasing the real Bedrock
		// investigation (docs/real-node-plan.md, round 101) - a real
		// `class Collector extends Writable` destination (real
		// @smithy/node-http-handler's own streamCollector, doing
		// `stream.pipe(collector)` to read a real HTTP response body)
		// has `write`/`end` on its *prototype* (stream.go's `Writable`
		// class defines them as ordinary class methods, which JS puts on
		// `Writable.prototype`, not as own properties of each instance) -
		// GetOwn only checks the instance itself and silently found
		// neither, so piping into any class-based Writable destination
		// wrote nothing and never called end() at all, hanging whatever
		// awaited the destination's own "finish"/"close" event forever.
		// Real Node's `dest.write(...)`/`dest.end()` are ordinary
		// property accesses, which naturally walk the prototype chain -
		// Get (object.go) is the direct equivalent here.
		addListener(vmInst, obj, "data", vm.NewNativeFunction(1, false, "pipeData", func(dataArgs []vm.Value) (vm.Value, error) {
			if len(dataArgs) == 0 {
				return vm.Undefined, nil
			}
			if destObj := dest.AsPlainObject(); destObj != nil {
				if writeFn, ok := destObj.Get("write"); ok && writeFn.IsCallable() {
					_, _ = vmInst.Call(writeFn, dest, []vm.Value{dataArgs[0]})
				}
			}
			return vm.Undefined, nil
		}), false, false)
		addListener(vmInst, obj, "end", vm.NewNativeFunction(0, false, "pipeEnd", func(_ []vm.Value) (vm.Value, error) {
			if destObj := dest.AsPlainObject(); destObj != nil {
				if endFn, ok := destObj.Get("end"); ok && endFn.IsCallable() {
					_, _ = vmInst.Call(endFn, dest, nil)
				}
			}
			return vm.Undefined, nil
		}), false, false)
		return dest, nil
	}))
	return obj
}

func newWritableStream(vmInst *vm.VM, writeFn func([]vm.Value) (vm.Value, error), endFn func([]vm.Value) (vm.Value, error)) *vm.PlainObject {
	obj := newEventEmitterObject(vmInst)
	obj.SetOwn("writable", vm.True)
	obj.SetOwn("write", vm.NewNativeFunction(1, false, "write", writeFn))
	if endFn != nil {
		obj.SetOwn("end", vm.NewNativeFunction(0, true, "end", endFn))
	}
	return obj
}

func getListenerArray(eventsTable *vm.PlainObject, event string) *vm.ArrayObject {
	if existing, ok := eventsTable.GetOwn(event); ok {
		if arr := existing.AsArray(); arr != nil {
			return arr
		}
	}
	arr := vm.NewArray()
	eventsTable.SetOwn(event, arr)
	return arr.AsArray()
}

// addListener registers listener for event, appending it (Node's on/once/
// addListener) or, when prepend is true, inserting it at index 0 instead
// (Node's prependListener/prependOnceListener - used by e.g. real Node's
// process.prependListener, which registerSignalHandlers-shaped code in
// real-world npm packages calls for signal/uncaughtException handlers so
// they run before any listener the packages themselves add later).
func addListener(vmInst *vm.VM, obj *vm.PlainObject, event string, listener vm.Value, once bool, prepend bool) vm.Value {
	if !listener.IsCallable() {
		return vm.NewValueFromPlainObject(obj)
	}
	eventsVal, ok := obj.GetOwn("_events")
	if !ok {
		return vm.NewValueFromPlainObject(obj)
	}
	eventsTable := eventsVal.AsPlainObject()
	if eventsTable == nil {
		return vm.NewValueFromPlainObject(obj)
	}
	fn := listener
	if once {
		fn = vm.NewNativeFunction(0, true, "onceWrapper", func(args []vm.Value) (vm.Value, error) {
			removeListener(obj, event, fn)
			_, err := vmInst.Call(listener, vm.NewValueFromPlainObject(obj), args)
			return vm.Undefined, err
		})
	}
	arr := getListenerArray(eventsTable, event)
	if prepend {
		n := arr.Length()
		arr.SetLength(n + 1)
		for i := n; i > 0; i-- {
			arr.Set(i, arr.Get(i-1))
		}
		arr.Set(0, fn)
	} else {
		arr.Append(fn)
	}
	// A "data" listener attaching *late* (after chunks already arrived
	// and got buffered by scheduleEmit/pendingStreamData, above) still
	// needs those chunks delivered - nothing else re-checks the buffer
	// once a chunk has already been scheduled and found no listener.
	// Scheduled for next tick, not flushed synchronously here, so this
	// doesn't reenter mid-registration and stays consistent with every
	// other "data"/"end" emit's own timing.
	if event == "data" {
		if pending, ok := obj.InternalSlots().(*pendingStreamData); ok {
			vmInst.GetAsyncRuntime().ScheduleNextTick(func() {
				flushPendingStreamData(vmInst, obj, pending)
			})
		}
	}
	return vm.NewValueFromPlainObject(obj)
}

// removeAllListeners removes every listener for the given event, or every
// listener for every event when called with no arguments (matching real
// Node's EventEmitter.removeAllListeners()).
func removeAllListeners(obj *vm.PlainObject, args []vm.Value) vm.Value {
	eventsVal, ok := obj.GetOwn("_events")
	if !ok {
		return vm.NewValueFromPlainObject(obj)
	}
	eventsTable := eventsVal.AsPlainObject()
	if eventsTable == nil {
		return vm.NewValueFromPlainObject(obj)
	}
	if len(args) == 0 {
		obj.SetOwn("_events", vm.NewValueFromPlainObject(vm.NewObject(vm.Undefined).AsPlainObject()))
		return vm.NewValueFromPlainObject(obj)
	}
	event := args[0].ToString()
	if existing, ok := eventsTable.GetOwn(event); ok {
		if arr := existing.AsArray(); arr != nil {
			arr.SetLength(0)
		}
	}
	return vm.NewValueFromPlainObject(obj)
}

func listenerCount(obj *vm.PlainObject, event string) int {
	eventsVal, ok := obj.GetOwn("_events")
	if !ok {
		return 0
	}
	eventsTable := eventsVal.AsPlainObject()
	if eventsTable == nil {
		return 0
	}
	existing, ok := eventsTable.GetOwn(event)
	if !ok {
		return 0
	}
	arr := existing.AsArray()
	if arr == nil {
		return 0
	}
	return arr.Length()
}

func listenersOf(obj *vm.PlainObject, event string) vm.Value {
	out := vm.NewArray()
	eventsVal, ok := obj.GetOwn("_events")
	if !ok {
		return out
	}
	eventsTable := eventsVal.AsPlainObject()
	if eventsTable == nil {
		return out
	}
	existing, ok := eventsTable.GetOwn(event)
	if !ok {
		return out
	}
	arr := existing.AsArray()
	if arr == nil {
		return out
	}
	outArr := out.AsArray()
	for i := 0; i < arr.Length(); i++ {
		outArr.Append(arr.Get(i))
	}
	return out
}

func removeListener(obj *vm.PlainObject, event string, listener vm.Value) vm.Value {
	eventsVal, ok := obj.GetOwn("_events")
	if !ok {
		return vm.NewValueFromPlainObject(obj)
	}
	eventsTable := eventsVal.AsPlainObject()
	if eventsTable == nil {
		return vm.NewValueFromPlainObject(obj)
	}
	existing, ok := eventsTable.GetOwn(event)
	if !ok {
		return vm.NewValueFromPlainObject(obj)
	}
	arr := existing.AsArray()
	if arr == nil {
		return vm.NewValueFromPlainObject(obj)
	}
	for i := 0; i < arr.Length(); i++ {
		if arr.Get(i) == listener {
			for j := i; j < arr.Length()-1; j++ {
				arr.Set(j, arr.Get(j+1))
			}
			arr.SetLength(arr.Length() - 1)
			break
		}
	}
	return vm.NewValueFromPlainObject(obj)
}

func emitOnObject(vmInst *vm.VM, obj *vm.PlainObject, event string, args ...vm.Value) bool {
	eventsVal, ok := obj.GetOwn("_events")
	if !ok {
		return false
	}
	eventsTable := eventsVal.AsPlainObject()
	if eventsTable == nil {
		return false
	}
	existing, ok := eventsTable.GetOwn(event)
	if !ok {
		return false
	}
	arr := existing.AsArray()
	if arr == nil || arr.Length() == 0 {
		return false
	}
	listeners := make([]vm.Value, arr.Length())
	for i := 0; i < arr.Length(); i++ {
		listeners[i] = arr.Get(i)
	}
	// Real Node's EventEmitter invokes every listener with the emitter
	// itself as `this` - real undici's own connector relies on exactly
	// this (lib/core/connect.js's `.once('connect', function () { ...
	// cb(null, this) })`, read directly before writing net.go/tls.go).
	// Passing vm.Undefined here (the previous behavior) silently handed
	// undici back `undefined` as its own socket, breaking every
	// downstream dispatch - a real, encountered bug, not a hypothetical
	// one (see docs/real-node-plan.md's round 69 entry).
	self := vm.NewValueFromPlainObject(obj)
	for _, fn := range listeners {
		if fn.IsCallable() {
			_, _ = vmInst.Call(fn, self, args)
		}
	}
	return true
}

// pendingStreamData buffers "data"/"end" for a newReadableStream object
// between the moment a chunk actually arrives and the moment something
// is actually listening for it - the same "paused until a consumer
// attaches" contract every real Node Readable has, and a real race
// without it: found the hard way chasing the real Bedrock investigation
// (docs/real-node-plan.md, round 101). Real Node's own
// `NodeHttpHandler` reads a response body only after normal async/await
// plumbing (several middleware layers, each its own microtask hop)
// finally reaches `stream.pipe(collector)` - by then, a background Go
// goroutine reading the real socket (pumpHTTPResponseBody, http.go) may
// already have scheduled *every* "data"/"end" emit for a stream nobody
// had attached a listener to yet. `emit()` with zero listeners is
// correctly a no-op per the EventEmitter contract - but that made the
// data gone, not merely deferred, the instant more than a couple of
// microtask hops separated "response received" from "somebody reads
// it". Confirmed directly: a minimal repro with only two
// `await Promise.resolve()` calls between receiving a response and
// calling `.pipe()` on it was enough to reproduce a stuck promise that
// real Node completes correctly every time.
type pendingStreamData struct {
	chunks []vm.Value
	ended  bool
}

func scheduleEmit(vmInst *vm.VM, obj *vm.PlainObject, event string, args ...vm.Value) {
	rt := vmInst.GetAsyncRuntime()
	if event == "data" && len(args) == 1 {
		chunk := args[0]
		rt.ScheduleNextTick(func() {
			if pending, ok := obj.InternalSlots().(*pendingStreamData); ok {
				pending.chunks = append(pending.chunks, chunk)
				flushPendingStreamData(vmInst, obj, pending)
				return
			}
			emitOnObject(vmInst, obj, event, chunk)
		})
		return
	}
	if event == "end" {
		rt.ScheduleNextTick(func() {
			if pending, ok := obj.InternalSlots().(*pendingStreamData); ok {
				pending.ended = true
				flushPendingStreamData(vmInst, obj, pending)
				return
			}
			emitOnObject(vmInst, obj, event)
		})
		return
	}
	rt.ScheduleNextTick(func() {
		emitOnObject(vmInst, obj, event, args...)
	})
}

// flushPendingStreamData delivers every buffered chunk, in arrival
// order, as real "data" emits - but only once a real consumer exists
// (a "data" listener, which pipe() registers one of internally too);
// otherwise it leaves everything buffered for the next call, whether
// that's triggered by the next chunk arriving or by addListener (below)
// noticing a "data" listener just got attached. Emits "end" once, after
// every buffered chunk has actually been delivered, if the source has
// already finished.
func flushPendingStreamData(vmInst *vm.VM, obj *vm.PlainObject, pending *pendingStreamData) {
	if listenerCount(obj, "data") == 0 {
		return
	}
	for len(pending.chunks) > 0 {
		chunk := pending.chunks[0]
		pending.chunks = pending.chunks[1:]
		emitOnObject(vmInst, obj, "data", chunk)
	}
	if pending.ended {
		pending.ended = false
		emitOnObject(vmInst, obj, "end")
	}
}

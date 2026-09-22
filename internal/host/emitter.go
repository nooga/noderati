package host

import (
	"fmt"

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
	// setEncoding was missing entirely - real Node's Readable always has
	// it, and pi-agent-core's own real tool-call harness (nodejs.js's
	// AgentEnvironment.exec(), the code path behind every bash-tool
	// invocation) calls child.stdout.setEncoding("utf8")/
	// child.stderr?.setEncoding("utf8") unconditionally, immediately after
	// spawn, with no guarding `?.` before the call itself. This used to be
	// a permanent no-op, justified at the time by every "data" emitter
	// (pumpSpawnStream in particular) always producing a JS string chunk
	// already, never a real Buffer - but that premise stopped being true
	// once pumpSpawnStream was fixed to emit real Buffers by default
	// (matching pumpHTTPResponseBody's own real-Buffer-by-default fix,
	// docs/real-node-plan.md round 101), found chasing real esbuild's own
	// service protocol under noderati: its stdout is a binary,
	// length-prefixed protocol, and a permanently-string "data" stream
	// broke it silently rather than throwing (Buffer.concat on a string
	// argument produces zero bytes, not an error - the same failure shape
	// round 101 already documented for HTTP). Now a real switch: sets a
	// per-stream StringDecoder (string_decoder.go's real incremental
	// decoder, so a multi-byte character split across two chunks decodes
	// correctly) that flushPendingStreamData consults before each delivery.
	obj.SetOwn("setEncoding", vm.NewNativeFunction(1, false, "setEncoding", func(args []vm.Value) (vm.Value, error) {
		if pending, ok := obj.InternalSlots().(*pendingStreamData); ok {
			if len(args) > 0 && !args[0].IsUndefined() && !args[0].IsNull() {
				pending.decoder = newStringDecoder(vmInst)(args[0])
			} else {
				pending.decoder = nil
			}
		}
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

// reportIfUnhandledError implements real Node's own special-cased
// EventEmitter contract: emitting "error" with no listener registered
// crashes the process (throwing the error itself when it's a real
// Error, or a generic "Unhandled error." otherwise) instead of
// silently doing nothing, unlike every other event name. Found
// chasing real tinypool's own worker-pool setup under noderati:
// worker/fork creation failures (both worker_threads.Worker and
// child_process.fork are still unimplemented) get reported via
// `this.emit('error', err)` internally with no listener yet attached,
// and this used to just return false - not a visible crash, a
// promise nothing would ever settle, hanging forever instead of
// failing loudly. Every call site of emitOnObject already treats a
// listener's own thrown exception as unconditionally
// process-crashing (reportUncaughtCallbackException, just above this
// function) regardless of whether the call originated synchronously
// from JS or from a background goroutine's own error path - this
// matches that same existing precedent for the "nobody's listening
// for error at all" case too, rather than threading a distinguishable
// Go error back through every one of this function's ~30 call sites.
// A synchronous `try { emitter.emit('error', e) } catch {}` in real
// Node would catch the throw locally instead of crashing - this
// still crashes the whole process for that case, the same known,
// pre-existing imprecision the listener-throw path above already
// has, not a new one introduced here.
func reportIfUnhandledError(vmInst *vm.VM, event string, args []vm.Value) bool {
	if event != "error" {
		return false
	}
	var er vm.Value = vm.Undefined
	if len(args) > 0 {
		er = args[0]
	}
	reportUncaughtCallbackException(vmInst, unhandledErrorEventError(er))
	return false
}

func unhandledErrorEventError(er vm.Value) error {
	return fmt.Errorf("Unhandled error event: %s", er.Inspect())
}

func emitOnObject(vmInst *vm.VM, obj *vm.PlainObject, event string, args ...vm.Value) bool {
	eventsVal, ok := obj.GetOwn("_events")
	if !ok {
		return reportIfUnhandledError(vmInst, event, args)
	}
	eventsTable := eventsVal.AsPlainObject()
	if eventsTable == nil {
		return reportIfUnhandledError(vmInst, event, args)
	}
	existing, ok := eventsTable.GetOwn(event)
	if !ok {
		return reportIfUnhandledError(vmInst, event, args)
	}
	arr := existing.AsArray()
	if arr == nil || arr.Length() == 0 {
		return reportIfUnhandledError(vmInst, event, args)
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
			// An exception thrown here used to vanish silently - this is a
			// host callback dispatch site with no catching JS context above
			// it (same shape as timeout_object.go/immediate_object.go's own
			// setTimeout/setImmediate call sites, see uncaught.go), but
			// nothing here ever reported the error Call() returns. Found the
			// hard way chasing real esbuild's own service protocol under
			// noderati: a real exception thrown inside a stream "data"
			// listener (itself caused by a real, separate engine bug,
			// paserati#498) silently corrupted downstream state instead of
			// crashing loudly the way real Node's own EventEmitter does for
			// an uncaught exception escaping a listener - turning an
			// otherwise easy, minutes-long repro into a much longer
			// investigation. Every EventEmitter-based construct in this
			// codebase (process, streams, sockets, HTTP, ...) shares this
			// one function, so this one fix covers all of them at once.
			if _, err := vmInst.Call(fn, self, args); err != nil {
				reportUncaughtCallbackException(vmInst, err)
			}
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
	// decoder is nil in the real-Node default (raw Buffer chunks); once
	// setEncoding() sets it, flushPendingStreamData routes every buffered
	// chunk through it before delivery, so a multi-byte character split
	// across two underlying reads still decodes correctly.
	decoder *stringDecoder
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
		if pending.decoder != nil {
			if text := pending.decoder.Write(chunk); text != "" {
				emitOnObject(vmInst, obj, "data", vm.NewString(text))
			}
			continue
		}
		emitOnObject(vmInst, obj, "data", chunk)
	}
	if pending.ended {
		pending.ended = false
		if pending.decoder != nil {
			if tail := pending.decoder.End(vm.Undefined); tail != "" {
				emitOnObject(vmInst, obj, "data", vm.NewString(tail))
			}
		}
		emitOnObject(vmInst, obj, "end")
	}
}

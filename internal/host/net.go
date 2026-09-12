package host

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/runtime"
	"github.com/nooga/paserati/pkg/vm"
)

// net.go implements real node:net (net.connect/net.createConnection/
// net.Socket) on top of Go's own net.Dial - a genuine TCP socket, not a
// synthetic stand-in. This exists specifically to back real undici's
// connector (lib/core/connect.js in the real npm package, read directly
// before writing this - see docs/real-node-plan.md's round 69 entry),
// which builds its own HTTP/1.1 client straight on raw net.Socket/
// tls.Socket rather than on node:http. tls.go is the TLS half of the same
// pair and reuses everything here (socketState, buildSocketObject,
// writerLoop/readerLoop) - a *tls.Conn satisfies net.Conn identically to
// a *net.TCPConn, so the byte-pump plumbing doesn't need to know which
// one it's holding.
//
// Backpressure is real, not faked: write() enqueues onto an unbounded
// Go-side queue (matching Node's own internal buffering - Node's write()
// doesn't block the event loop either, and returns a `false` hint while
// still accepting the data) and a dedicated goroutine drains it onto the
// actual conn; write() returns false once the queued byte count exceeds
// highWaterMark, and 'drain' fires for real once the queue empties back
// out - not on a timer or a fixed delay.

const defaultNetHighWaterMark = 64 * 1024

var (
	socketRegistry sync.Map // id(uint64) -> *socketState
	socketRegSeq   atomic.Uint64
)

// TEMP diagnostic instrumentation (docs/real-node-plan.md, Round 87) for
// chasing the two still-open, un-root-caused hangs first observed in
// Round 82/84: a non-deterministic "script never finishes" hang (main
// goroutine parked in WaitForExternalOp, live idle sockets) and a
// separate "process won't exit after success" case. Off by default
// (zero cost beyond one bool check per call site) - set
// NODERATI_NET_TRACE=1 to get a stderr trace of every socket's
// lifecycle events (connect/read/readable/read()/ref/unref/destroy),
// each tagged with a per-socket sequence id, to reconstruct exactly
// what a stalled connection last did. Not wired to any test; remove
// once the hang is root-caused, or keep if it earns its place
// alongside NODERATI_PPROF - undecided until then.
var netTraceEnabled = os.Getenv("NODERATI_NET_TRACE") != ""
var netTraceSeq atomic.Uint64

func netTrace(id uint64, format string, args ...any) {
	if !netTraceEnabled {
		return
	}
	fmt.Fprintf(os.Stderr, "[net#%d %s] "+format+"\n", append([]any{id, time.Now().Format("15:04:05.000000")}, args...)...)
}

const socketHandleMarker = "__noderatiSocketHandle"

type socketWriteItem struct {
	data  []byte
	isEnd bool
	cb    vm.Value
}

// socketState is the Go-side half of a Socket - the JS-visible object
// built by buildSocketObject only ever reaches into this through the
// native closures it installs, never the other way around, so nothing
// here touches VM values off the VM's own goroutine.
type socketState struct {
	mu   sync.Mutex
	cond *sync.Cond

	conn     net.Conn
	detached bool // true once ownership of conn was handed to a TLS upgrade; loops exit without closing it

	writeQueue []socketWriteItem
	closed     bool
	destroyed  bool
	closeOnce  sync.Once

	bytesQueued   int
	needDrain     bool
	paused        bool
	highWaterMark int

	bytesRead    int64
	bytesWritten int64

	encoding string // set via setEncoding(); "" means emit Buffers, matching real Node's default

	// Paused-mode Readable support (see pushReadData/read()/startFlowing).
	// Real undici's own HTTP/1.1 client (client-h1.js) drives its parser
	// exclusively through Node's paused-mode Readable protocol -
	// socket.on('readable', onHttpSocketReadable), which calls
	// socket.read() in a loop - and never listens for 'data' at all.
	// Before this existed, readerLoop unconditionally emitted 'data' with
	// no paused-mode alternative, so those bytes arrived, were drained
	// off the real OS socket, and were emitted into an event nobody was
	// listening for - undici's parser was never fed a single byte, and
	// the whole request hung forever. Confirmed directly via a minimal,
	// undici-free repro before writing this (docs/real-node-plan.md,
	// Round 77).
	//
	// flowing mirrors real Node's "flowing" vs "paused" stream mode:
	// registering the first 'data' listener (see buildSocketObject's
	// on/addListener/once overrides) or calling resume() switches to
	// flowing, where bytes auto-emit as 'data' exactly like this
	// implementation always did before this change; pause() switches
	// back. While not flowing, incoming bytes accumulate in readBuf and
	// a coalesced 'readable' fires so a caller can drain them via
	// read() - the only two primitives real undici's parser actually
	// needs.
	flowing           bool
	readBuf           []byte
	readableScheduled bool // coalesces bursts of arrivals into one 'readable' emission per drain cycle, reset just before that emission actually runs so a later arrival can schedule the next one
	sourceEnded       bool // true once readerLoop has observed real EOF - 'end' is deferred (see read()) until readBuf is fully drained, matching Node's own contract that 'end' never fires while there's still unread buffered data
	endEmitted        bool // guards against emitting 'end' (and destroying) more than once between the EOF branch and read()'s own end-of-buffer check racing to notice it first

	pendingNoDelay    *bool
	pendingKeepAlive  *bool
	keepAliveInitDlay time.Duration

	timeoutMu    sync.Mutex
	timeoutTimer *time.Timer

	// extRT/extOpActive back Socket.ref()/.unref(): doNetConnect/
	// doTLSConnect each call rt.BeginExternalOp() exactly once up front
	// (so the process waits for this connection by default, matching
	// real Node) and arrange for exactly one matching rt.EndExternalOp()
	// over the socket's whole lifetime, via endTrackedExternalOp() once
	// the connection's own goroutines actually finish. unref()/ref() (see
	// setExternalOpActive) can retire or restore that one registration
	// early - real undici's client-h1.js calls socket.unref() the moment
	// a keep-alive connection has no in-flight request (resumeH1) so a
	// pooled idle connection doesn't itself keep the process alive,
	// exactly like real Node's socket.unref() does. Before this existed,
	// unref()/ref() were no-op stubs, so a still-open idle keep-alive
	// socket (whose reader/writer loops block on a real, live conn.Read/
	// write forever) kept DrainUntilIdle's WaitForExternalOp() waiting
	// forever too - confirmed directly via a live pprof goroutine dump
	// during a real-undici fetch() E2E probe (docs/real-node-plan.md,
	// Round 76/77) before writing this fix.
	extRT       runtime.AsyncRuntime
	extOpActive bool
	// extOpFinal is set exactly once, inside endTrackedExternalOp().
	// Found by code inspection (round 82, docs/real-node-plan.md) while
	// investigating a real-undici E2E hang - NOT itself confirmed to be
	// the cause of that hang; a live pprof goroutine dump taken during
	// that specific hang showed every socket's reader/writer loops
	// still genuinely alive and idle, not orphaned, so this gap wasn't
	// what was actually observed there. It's a real, separate hole,
	// defensible on its own: setExternalOpActive's active/inactive
	// flip-flop is only balanced for unref()/ref() sequences that land
	// *while the connection is still alive*. The one goroutine that
	// ever calls endTrackedExternalOp() (doNetConnect/doTLSConnect's
	// `go func() { wg.Wait(); s.endTrackedExternalOp() }()`) runs
	// exactly once, when the reader+writer loops actually finish. A
	// ref() call landing *after* that goroutine has already released
	// its EndExternalOp (e.g. a pool tries to reuse a connection just
	// as it's dying) would otherwise issue a fresh BeginExternalOp()
	// that nothing remains alive to ever match with an EndExternalOp() -
	// a real, if not yet directly observed, leak of the pending-op
	// count. extOpFinal closes that window: once the one-shot teardown
	// has run, every later ref() is a permanent no-op, matching what
	// real Node does anyway (ref()/unref() on an already-destroyed
	// socket has no observable effect).
	extOpFinal bool

	// traceID is this socket's tag in the NODERATI_NET_TRACE diagnostic
	// log (see the netTrace doc comment above) - assigned once, in
	// newSocketState, independent of socketRegistry's own JS-handle id.
	traceID uint64
}

// beginTrackedExternalOp records that the caller (doNetConnect/
// doTLSConnect) already called rt.BeginExternalOp() once for this
// socket, and remembers rt so a later unref()/ref()/endTrackedExternalOp
// can issue the one matching EndExternalOp() this socket owes - never
// more, never less, regardless of how many times JS toggles ref()/
// unref() in between. Must be called before the socket's JS object is
// ever handed back to JS (so unref()/ref() can never race ahead of it).
func (s *socketState) beginTrackedExternalOp(rt runtime.AsyncRuntime) {
	s.mu.Lock()
	s.extRT = rt
	s.extOpActive = true
	s.mu.Unlock()
}

// setExternalOpActive is the shared logic behind Socket.unref()
// (active=false) and Socket.ref() (active=true): only calls Begin/
// EndExternalOp when the state actually changes, so calling either
// repeatedly - or calling unref() before the connection even finished
// dialing - is a harmless no-op past the first real transition, matching
// real Node's own ref()/unref() idempotency.
func (s *socketState) setExternalOpActive(active bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.extRT == nil || s.extOpActive == active {
		netTrace(s.traceID, "setExternalOpActive(%v) no-op (extRT-nil=%v already=%v)", active, s.extRT == nil, s.extOpActive == active)
		return
	}
	// A ref() (active=true) arriving after the one-shot teardown
	// goroutine already ran must not issue a fresh BeginExternalOp() -
	// see the extOpFinal field comment above for why: nothing is left
	// alive to ever call the matching EndExternalOp(), which would leak
	// the pending-op count and hang DrainUntilIdle forever. A late
	// unref() (active=false) is always safe to let through as a no-op
	// here - extOpActive is already false past teardown (endTracked-
	// ExternalOp sets it), so the s.extOpActive == active check above
	// already short-circuits that case.
	if active && s.extOpFinal {
		netTrace(s.traceID, "setExternalOpActive(true) blocked by extOpFinal")
		return
	}
	s.extOpActive = active
	if active {
		netTrace(s.traceID, "BeginExternalOp()")
		s.extRT.BeginExternalOp()
	} else {
		netTrace(s.traceID, "EndExternalOp()")
		s.extRT.EndExternalOp()
	}
}

// endTrackedExternalOp is called exactly once, when the connection's own
// writer+reader goroutines actually finish, to release whichever
// external-op registration is still outstanding - a no-op if unref()
// already released it earlier. Also latches extOpFinal so any ref()
// call arriving after this point can never re-open a registration that
// nothing remains alive to close.
func (s *socketState) endTrackedExternalOp() {
	netTrace(s.traceID, "endTrackedExternalOp (reader+writer loops both finished)")
	s.setExternalOpActive(false)
	s.mu.Lock()
	s.extOpFinal = true
	s.mu.Unlock()
}

func newSocketState(hwm int) *socketState {
	s := &socketState{highWaterMark: hwm, traceID: netTraceSeq.Add(1)}
	s.cond = sync.NewCond(&s.mu)
	netTrace(s.traceID, "created hwm=%d", hwm)
	return s
}

func registerSocketState(s *socketState) uint64 {
	id := socketRegSeq.Add(1)
	socketRegistry.Store(id, s)
	return id
}

func socketStateFromValue(v vm.Value) *socketState {
	obj := v.AsPlainObject()
	if obj == nil {
		return nil
	}
	idVal, ok := obj.GetOwn(socketHandleMarker)
	if !ok || !idVal.IsNumber() {
		return nil
	}
	if s, ok := socketRegistry.Load(uint64(idVal.ToFloat())); ok {
		return s.(*socketState)
	}
	return nil
}

// valueToBytes accepts either a JS string or a real Buffer/TypedArray
// (buffer.go's Buffer is a real Uint8Array subclass as of the Buffer
// realness pass done alongside paserati#375/WebAssembly - see buffer.go)
// and returns the underlying bytes. A TypedArray's own real backing
// bytes are read directly via typedArrayBytes rather than through any
// string round trip, so this is safe for arbitrary binary data too.
func valueToBytes(vmInst *vm.VM, v vm.Value) []byte {
	if ta := v.AsTypedArray(); ta != nil {
		if b := typedArrayBytes(ta); b != nil {
			return b
		}
		return nil
	}
	return []byte(v.ToString())
}

// valueToBytesWithEncoding is valueToBytes plus real support for an
// explicit string encoding on the string branch - real Node's own
// `Writable.write(chunk, [encoding], [callback])` accepts one, and
// callers that actually pass raw binary data through a string (the
// classic "latin1"/"binary" convention for byte values 0-255, one
// per character) need it decoded that way, not as the default utf8 a
// bare string argument gets everywhere else in this codebase. A
// TypedArray/Buffer argument already carries its own real bytes, so
// encoding is irrelevant there, same as valueToBytes.
func valueToBytesWithEncoding(vmInst *vm.VM, v vm.Value, encoding string) []byte {
	if ta := v.AsTypedArray(); ta != nil {
		if b := typedArrayBytes(ta); b != nil {
			return b
		}
		return nil
	}
	if encoding == "" || normalizeBufferEncoding(encoding) == "utf8" {
		return []byte(v.ToString())
	}
	if b, err := decodeBufferString(v.ToString(), encoding); err == nil {
		return b
	}
	return []byte(v.ToString())
}

// parseWriteEncodingAndCallback pulls the optional encoding/callback pair
// out of a real Node-style `write(chunk, [encoding], [callback])` (or
// `end(chunk, [encoding], [callback])`) argument list, starting at
// index `from` (the position right after `chunk`). Real Node accepts
// either an encoding string, a callback function, both, or neither in
// that slot - this mirrors that overload resolution rather than
// assuming one shape.
func parseWriteEncodingAndCallback(args []vm.Value, from int) (encoding string, cb vm.Value) {
	cb = vm.Undefined
	if len(args) > from {
		if args[from].IsCallable() {
			cb = args[from]
		} else if args[from].IsString() {
			encoding = args[from].ToString()
		}
	}
	if len(args) > from+1 && args[from+1].IsCallable() {
		cb = args[from+1]
	}
	return encoding, cb
}

// encodeChunkValue mirrors real Node's default: a chunk is a Buffer
// unless setEncoding() was called, in which case it's a decoded string
// instead. Shared between emitDataChunk (flowing-mode 'data' events) and
// read() (paused-mode's pull side) so both encode identically.
func encodeChunkValue(vmInst *vm.VM, chunk []byte, encoding string) vm.Value {
	switch encoding {
	case "":
		return wrapBuffer(vmInst, chunk)
	case "hex":
		return vm.NewString(hex.EncodeToString(chunk))
	case "base64":
		return vm.NewString(base64.StdEncoding.EncodeToString(chunk))
	default:
		return vm.NewString(string(chunk))
	}
}

// emitDataChunk schedules a 'data' event carrying chunk, encoded per
// encoding. The encoding is only ever read here, on the VM thread (via
// scheduleEmit's ScheduleNextTick), never from the background read
// goroutine that captured the raw bytes.
func emitDataChunk(vmInst *vm.VM, obj *vm.PlainObject, chunk []byte, encoding string) {
	scheduleEmit(vmInst, obj, "data", encodeChunkValue(vmInst, chunk, encoding))
}

// pushReadData is what readerLoop calls with each newly-received chunk.
// While flowing, it emits 'data' immediately - this implementation's
// original, still-default behavior, and what every existing 'data'-based
// caller in this codebase already relies on. Otherwise (paused mode) it
// buffers the bytes into readBuf and schedules a single coalesced
// 'readable' emission, which is the other half of the protocol real
// undici's h1 client actually drives its parser through.
func (s *socketState) pushReadData(vmInst *vm.VM, obj *vm.PlainObject, chunk []byte) {
	s.mu.Lock()
	if s.flowing {
		encoding := s.encoding
		s.mu.Unlock()
		netTrace(s.traceID, "pushReadData n=%d flowing=true -> emit data", len(chunk))
		emitDataChunk(vmInst, obj, chunk, encoding)
		return
	}
	s.readBuf = append(s.readBuf, chunk...)
	readBufLen := len(s.readBuf)
	s.mu.Unlock()
	netTrace(s.traceID, "pushReadData n=%d flowing=false readBufLen=%d", len(chunk), readBufLen)
	s.maybeScheduleReadable(vmInst, obj)
}

// maybeScheduleReadable schedules one 'readable' emission unless one is
// already pending, resetting readableScheduled just before the emission
// actually fires (not when it's merely queued) so a chunk that arrives
// in between still gets its own follow-up emission rather than being
// silently coalesced away. Both pushReadData and readerLoop's own EOF
// branch (which buffers no new bytes but still needs a 'readable' to
// tell a paused-mode consumer there's a final chunk left to drain) go
// through this same coalescing check rather than scheduling directly.
func (s *socketState) maybeScheduleReadable(vmInst *vm.VM, obj *vm.PlainObject) {
	s.mu.Lock()
	alreadyScheduled := s.readableScheduled
	s.readableScheduled = true
	s.mu.Unlock()
	if alreadyScheduled {
		netTrace(s.traceID, "maybeScheduleReadable: already scheduled, coalescing")
		return
	}
	netTrace(s.traceID, "maybeScheduleReadable: scheduling next-tick emission")
	vmInst.GetAsyncRuntime().ScheduleNextTick(func() {
		s.mu.Lock()
		s.readableScheduled = false
		readBufLen := len(s.readBuf)
		s.mu.Unlock()
		netTrace(s.traceID, "emitting readable, readBufLen=%d", readBufLen)
		emitOnObject(vmInst, obj, "readable")
	})
}

// startFlowing switches the socket into flowing mode (see the flowing
// field's doc comment) - triggered by registering the first 'data'
// listener or calling resume(). Anything that accumulated in readBuf
// while paused is drained as one 'data' event immediately, and if the
// source had already hit EOF while paused, this drain is exactly what
// finally empties the buffer, so 'end' (and the deferred destroy) fires
// here too - the same logic read() applies when it empties the buffer
// itself.
func (s *socketState) startFlowing(vmInst *vm.VM, obj *vm.PlainObject) {
	s.mu.Lock()
	if s.flowing {
		s.mu.Unlock()
		return
	}
	s.flowing = true
	buffered := s.readBuf
	s.readBuf = nil
	encoding := s.encoding
	shouldEnd := s.sourceEnded && !s.endEmitted
	if shouldEnd {
		s.endEmitted = true
	}
	s.mu.Unlock()
	if len(buffered) > 0 {
		emitDataChunk(vmInst, obj, buffered, encoding)
	}
	if shouldEnd {
		scheduleEmit(vmInst, obj, "end")
		s.destroyInternal(vmInst, obj, false)
	}
}

// stopFlowing switches back to paused mode (pause()). Bytes that arrive
// afterwards accumulate in readBuf instead of auto-emitting as 'data'.
func (s *socketState) stopFlowing() {
	s.mu.Lock()
	s.flowing = false
	s.mu.Unlock()
}

// writerLoop is the only goroutine that ever calls conn.Write - write()/
// end() just enqueue, exactly like http.go's bodyCh pattern, so a
// synchronous native call never blocks the VM's own execution thread on
// unbuffered (or slow) network I/O. It parks in cond.Wait() while there's
// nothing to send and no conn to send it on yet (pre-connect writes are
// legal in real Node and get flushed once the socket connects), and exits
// for good once the socket is closed and its queue has fully drained.
func (s *socketState) writerLoop(vmInst *vm.VM, rt runtime.AsyncRuntime, obj *vm.PlainObject, self vm.Value) {
	for {
		s.mu.Lock()
		for {
			if s.closed && (len(s.writeQueue) == 0 || s.conn == nil) {
				break
			}
			if len(s.writeQueue) > 0 && s.conn != nil {
				break
			}
			s.cond.Wait()
		}
		// A pending write/end() queued before destroy() can lose its data
		// here - closed became true (destroyInternal already closed conn)
		// while the item was still sitting in the queue, so it's drained
		// with an error instead of ever reaching conn.Write. This matches
		// real Node's own documented destroy() semantics, not a gap:
		// destroy() is specified to abort the socket immediately, with
		// "any data currently in flight" dropped, not flushed first (that
		// flush-before-close behavior is what end() alone is for). A
		// caller wanting queued data to actually go out has to await
		// end()/'finish' before calling destroy(), same as in real Node.
		if s.closed && (len(s.writeQueue) == 0 || s.conn == nil) {
			pending := s.writeQueue
			s.writeQueue = nil
			s.mu.Unlock()
			for _, item := range pending {
				item := item
				if item.cb.IsCallable() {
					rt.ScheduleNextTick(func() {
						_, _ = vmInst.Call(item.cb, self, []vm.Value{errorValueFromGo(vmInst, io.ErrClosedPipe)})
					})
				}
			}
			return
		}

		item := s.writeQueue[0]
		s.writeQueue = s.writeQueue[1:]
		conn := s.conn
		s.mu.Unlock()

		var writeErr error
		if len(item.data) > 0 {
			_, writeErr = conn.Write(item.data)
		}
		netTrace(s.traceID, "writerLoop wrote n=%d isEnd=%v err=%v", len(item.data), item.isEnd, writeErr)
		if writeErr == nil && item.isEnd {
			if cw, ok := conn.(interface{ CloseWrite() error }); ok {
				_ = cw.CloseWrite()
			} else {
				writeErr = conn.Close()
			}
		}

		s.mu.Lock()
		if writeErr == nil {
			s.bytesWritten += int64(len(item.data))
		}
		s.bytesQueued -= len(item.data)
		if s.bytesQueued < 0 {
			s.bytesQueued = 0
		}
		drain := false
		if s.bytesQueued == 0 && s.needDrain {
			s.needDrain = false
			drain = true
		}
		wasIntentional := s.closed
		s.mu.Unlock()

		if item.cb.IsCallable() {
			cb := item.cb
			goErr := writeErr
			rt.ScheduleNextTick(func() {
				errArg := vm.Undefined
				if goErr != nil {
					errArg = errorValueFromGo(vmInst, goErr)
				}
				_, _ = vmInst.Call(cb, self, []vm.Value{errArg})
			})
		}

		if writeErr != nil {
			if !wasIntentional {
				scheduleErrorEmit(vmInst, rt, obj, writeErr)
			}
			s.destroyInternal(vmInst, obj, writeErr != nil && !wasIntentional)
			return
		}
		if drain {
			scheduleEmit(vmInst, obj, "drain")
		}
	}
}

// readerLoop pulls bytes off conn as they arrive and hands each Read()
// call's chunk to pushReadData - which either emits 'data' immediately
// (flowing mode, this implementation's original behavior) or buffers it
// for read() to pull out later (paused mode - see pushReadData/read()/
// startFlowing) - never buffering the whole response before making it
// available one way or the other, so a slow/large response streams
// incrementally the same way http.go's pumpHTTPResponseBody already had
// to get right. pause()/resume() gate this loop directly (not just the
// JS-visible event delivery) so a paused socket genuinely stops pulling
// bytes off the OS socket, which is what makes TCP-level backpressure
// apply upstream instead of us just buffering in Go instead of
// buffering in JS.
func (s *socketState) readerLoop(vmInst *vm.VM, rt runtime.AsyncRuntime, obj *vm.PlainObject) {
	buf := make([]byte, 64*1024)
	for {
		s.mu.Lock()
		for s.paused && !s.closed {
			s.cond.Wait()
		}
		closed := s.closed
		conn := s.conn
		s.mu.Unlock()
		if closed || conn == nil {
			return
		}

		n, err := conn.Read(buf)
		netTrace(s.traceID, "readerLoop conn.Read n=%d err=%v", n, err)
		if n > 0 {
			s.mu.Lock()
			s.bytesRead += int64(n)
			s.mu.Unlock()
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			s.pushReadData(vmInst, obj, chunk)
		}
		if err != nil {
			s.mu.Lock()
			wasIntentional := s.closed
			s.mu.Unlock()
			if wasIntentional {
				return
			}
			if err == io.EOF {
				// 'end' must not fire while there's still unread
				// buffered data sitting in readBuf (real Node's own
				// contract - a paused-mode consumer needs read() to see
				// every remaining byte before 'end'). Only emit it
				// (and destroy) immediately when the buffer already
				// happens to be empty right now; otherwise defer to
				// whichever of read()/startFlowing next drains it to
				// empty.
				s.mu.Lock()
				s.sourceEnded = true
				empty := len(s.readBuf) == 0
				shouldEnd := empty && !s.endEmitted
				if shouldEnd {
					s.endEmitted = true
				}
				s.mu.Unlock()
				if shouldEnd {
					scheduleEmit(vmInst, obj, "end")
					s.destroyInternal(vmInst, obj, false)
				} else if !empty {
					s.maybeScheduleReadable(vmInst, obj)
				}
			} else {
				scheduleErrorEmit(vmInst, rt, obj, err)
				s.destroyInternal(vmInst, obj, true)
			}
			return
		}
	}
}

// destroyInternal is the single real teardown path - called whether the
// peer closed first (readerLoop's EOF), a write failed, or JS called
// .destroy() directly - guarded by closeOnce so 'close' fires exactly
// once regardless of which of those raced to get there first.
func (s *socketState) destroyInternal(vmInst *vm.VM, obj *vm.PlainObject, hadError bool) {
	s.closeOnce.Do(func() {
		netTrace(s.traceID, "destroyInternal hadError=%v", hadError)
		s.mu.Lock()
		s.closed = true
		s.destroyed = true
		conn := s.conn
		detached := s.detached
		s.mu.Unlock()
		s.timeoutMu.Lock()
		if s.timeoutTimer != nil {
			s.timeoutTimer.Stop()
		}
		s.timeoutMu.Unlock()
		if conn != nil && !detached {
			_ = conn.Close()
		}
		s.cond.Broadcast()
		obj.SetOwn("destroyed", vm.True)
		scheduleEmit(vmInst, obj, "close", vm.BooleanValue(hadError))
	})
}

func (s *socketState) queueWrite(vmInst *vm.VM, obj *vm.PlainObject, data []byte, isEnd bool, cb vm.Value) bool {
	s.mu.Lock()
	if s.destroyed {
		s.mu.Unlock()
		if cb.IsCallable() {
			scheduleEmit0(vmInst, func() {
				_, _ = vmInst.Call(cb, vm.NewValueFromPlainObject(obj), []vm.Value{errorValueFromGo(vmInst, io.ErrClosedPipe)})
			})
		}
		return false
	}
	s.writeQueue = append(s.writeQueue, socketWriteItem{data: data, isEnd: isEnd, cb: cb})
	s.bytesQueued += len(data)
	underHWM := s.bytesQueued <= s.highWaterMark
	if !underHWM {
		s.needDrain = true
	}
	s.mu.Unlock()
	s.cond.Broadcast()
	return underHWM
}

// scheduleEmit0 schedules a plain closure on the VM's next tick - used
// where there's no PlainObject/event name pair handy (a callback
// invocation rather than an emit).
func scheduleEmit0(vmInst *vm.VM, fn func()) {
	vmInst.GetAsyncRuntime().ScheduleNextTick(fn)
}

// scheduleErrorEmit schedules an 'error' emit whose Error *value* is
// built from goErr only once the tick actually runs on the VM's own
// thread - never before. A real, race-detector-caught bug (not a
// hypothetical one - see docs/real-node-plan.md's round 73 entry) is
// exactly the mistake this guards against: errorValueFromGo() ultimately
// calls vmInst.Construct() on the Error constructor, which mutates
// shared VM fields (currentThis/currentNewTarget/inConstructorCall) -
// safe when it runs on the VM's own goroutine via this tick, a genuine
// data race with the VM's main execution loop when called eagerly on a
// background goroutine before scheduling (which is what
// `scheduleEmit(vmInst, obj, "error", errorValueFromGo(vmInst, err))`
// actually does: Go evaluates errorValueFromGo's call *before* handing
// its result to scheduleEmit, on whichever goroutine made the call).
// Passing the plain Go `error` across the goroutine boundary and
// constructing the vm.Value only inside this closure is what makes it
// safe - callers on a background goroutine must use this, never
// scheduleEmit(..., errorValueFromGo(...)) directly.
func scheduleErrorEmit(vmInst *vm.VM, rt runtime.AsyncRuntime, obj *vm.PlainObject, goErr error) {
	rt.ScheduleNextTick(func() {
		emitOnObject(vmInst, obj, "error", errorValueFromGo(vmInst, goErr))
	})
}

// buildSocketObject builds the JS-visible Socket - readable+writable,
// wired directly onto socketState rather than via newReadableStream's
// destroy()/pipe() defaults, since a real Socket's destroy() must
// actually close a live conn (not just emit events) and its data flow is
// byte-level, not the string-only shape newReadableStream assumes.
func buildSocketObject(vmInst *vm.VM, s *socketState) (*vm.PlainObject, vm.Value) {
	obj := newEventEmitterObject(vmInst)
	self := vm.NewValueFromPlainObject(obj)
	id := registerSocketState(s)
	obj.SetOwn(socketHandleMarker, vm.NumberValue(float64(id)))
	obj.SetOwn("readable", vm.True)
	obj.SetOwn("writable", vm.True)
	obj.SetOwn("connecting", vm.False)
	obj.SetOwn("pending", vm.True)
	obj.SetOwn("destroyed", vm.False)
	obj.SetOwn("bufferSize", vm.NumberValue(0))

	rt := vmInst.GetAsyncRuntime()

	// on/addListener/once/prependListener/prependOnceListener override
	// newEventEmitterObject's generic versions with one Socket-specific
	// addition: registering a 'data' listener auto-switches the socket
	// into flowing mode, exactly like real Node's Readable. This is what
	// lets every existing 'data'-based caller in this codebase (and any
	// future one) keep working unchanged, while a caller that never adds
	// a 'data' listener - real undici's h1 client, which only ever uses
	// 'readable' + read() - stays in paused mode and must pull bytes out
	// itself. See the flowing field's doc comment on socketState.
	registerListener := func(name string, once, prepend bool) {
		obj.SetOwn(name, vm.NewNativeFunction(2, false, name, func(args []vm.Value) (vm.Value, error) {
			if len(args) < 2 {
				return self, nil
			}
			event := args[0].ToString()
			result := addListener(vmInst, obj, event, args[1], once, prepend)
			if event == "data" {
				s.startFlowing(vmInst, obj)
			}
			return result, nil
		}))
	}
	registerListener("on", false, false)
	registerListener("addListener", false, false)
	registerListener("once", true, false)
	registerListener("prependListener", false, true)
	registerListener("prependOnceListener", true, true)

	// read([size]) is paused-mode Readable's pull side: real undici's h1
	// client calls it with no arguments in a loop (readMore(), driven by
	// 'readable') to pull everything currently buffered out at once. A
	// size argument, when given, takes only that many bytes and leaves
	// the rest queued - real Node supports this too, and it costs
	// nothing extra to honor here since readBuf is already a flat byte
	// slice. Returns null when nothing is available yet, matching real
	// Node exactly (the caller waits for the next 'readable').
	obj.SetOwn("read", vm.NewNativeFunction(1, false, "read", func(args []vm.Value) (vm.Value, error) {
		size := -1
		if len(args) > 0 && args[0].IsNumber() {
			size = int(args[0].ToFloat())
		}
		s.mu.Lock()
		if len(s.readBuf) == 0 {
			shouldEnd := s.sourceEnded && !s.endEmitted
			if shouldEnd {
				s.endEmitted = true
			}
			s.mu.Unlock()
			netTrace(s.traceID, "read(size=%d) -> null (buffer empty, shouldEnd=%v)", size, shouldEnd)
			if shouldEnd {
				scheduleEmit(vmInst, obj, "end")
				s.destroyInternal(vmInst, obj, false)
			}
			return vm.Null, nil
		}
		var chunk []byte
		if size < 0 || size >= len(s.readBuf) {
			chunk = s.readBuf
			s.readBuf = nil
		} else {
			chunk = append([]byte(nil), s.readBuf[:size]...)
			s.readBuf = s.readBuf[size:]
		}
		encoding := s.encoding
		remaining := len(s.readBuf)
		s.mu.Unlock()
		netTrace(s.traceID, "read(size=%d) -> n=%d remaining=%d", size, len(chunk), remaining)
		return encodeChunkValue(vmInst, chunk, encoding), nil
	}))

	obj.SetOwn("write", vm.NewNativeFunction(3, true, "write", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.True, nil
		}
		data := valueToBytes(vmInst, args[0])
		var cb vm.Value
		for _, a := range args[1:] {
			if a.IsCallable() {
				cb = a
				break
			}
		}
		ok := s.queueWrite(vmInst, obj, data, false, cb)
		return vm.BooleanValue(ok), nil
	}))

	obj.SetOwn("end", vm.NewNativeFunction(2, true, "end", func(args []vm.Value) (vm.Value, error) {
		var data []byte
		var cb vm.Value
		for _, a := range args {
			if a.IsCallable() {
				cb = a
			} else if !a.IsUndefined() && a.Type() != vm.TypeNull {
				data = valueToBytes(vmInst, a)
			}
		}
		s.queueWrite(vmInst, obj, data, true, cb)
		return self, nil
	}))

	obj.SetOwn("destroy", vm.NewNativeFunction(1, true, "destroy", func(args []vm.Value) (vm.Value, error) {
		hadError := false
		if len(args) > 0 && !args[0].IsUndefined() && args[0].Type() != vm.TypeNull {
			hadError = true
			scheduleEmit(vmInst, obj, "error", args[0])
		}
		s.destroyInternal(vmInst, obj, hadError)
		return self, nil
	}))

	obj.SetOwn("pause", vm.NewNativeFunction(0, false, "pause", func(_ []vm.Value) (vm.Value, error) {
		s.mu.Lock()
		s.paused = true
		s.mu.Unlock()
		s.stopFlowing()
		return self, nil
	}))
	obj.SetOwn("resume", vm.NewNativeFunction(0, false, "resume", func(_ []vm.Value) (vm.Value, error) {
		s.mu.Lock()
		s.paused = false
		s.mu.Unlock()
		s.cond.Broadcast()
		s.startFlowing(vmInst, obj)
		return self, nil
	}))

	obj.SetOwn("setEncoding", vm.NewNativeFunction(1, false, "setEncoding", func(args []vm.Value) (vm.Value, error) {
		enc := ""
		if len(args) > 0 && !args[0].IsUndefined() {
			enc = args[0].ToString()
			if enc == "utf8" || enc == "utf-8" {
				enc = ""
			}
		}
		s.mu.Lock()
		s.encoding = enc
		s.mu.Unlock()
		return self, nil
	}))

	obj.SetOwn("setNoDelay", vm.NewNativeFunction(1, false, "setNoDelay", func(args []vm.Value) (vm.Value, error) {
		v := true
		if len(args) > 0 {
			v = args[0].IsTruthy()
		}
		s.mu.Lock()
		conn := s.conn
		s.pendingNoDelay = &v
		s.mu.Unlock()
		applyNoDelay(conn, v)
		return self, nil
	}))

	obj.SetOwn("setKeepAlive", vm.NewNativeFunction(2, false, "setKeepAlive", func(args []vm.Value) (vm.Value, error) {
		v := false
		if len(args) > 0 {
			v = args[0].IsTruthy()
		}
		delay := 0 * time.Millisecond
		if len(args) > 1 && args[1].IsNumber() {
			delay = time.Duration(args[1].ToFloat()) * time.Millisecond
		}
		s.mu.Lock()
		conn := s.conn
		s.pendingKeepAlive = &v
		s.keepAliveInitDlay = delay
		s.mu.Unlock()
		applyKeepAlive(conn, v, delay)
		return self, nil
	}))

	obj.SetOwn("setTimeout", vm.NewNativeFunction(2, false, "setTimeout", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 || !args[0].IsNumber() {
			return self, nil
		}
		ms := args[0].ToFloat()
		var cb vm.Value
		if len(args) > 1 && args[1].IsCallable() {
			cb = args[1]
		}
		s.timeoutMu.Lock()
		if s.timeoutTimer != nil {
			s.timeoutTimer.Stop()
		}
		if ms > 0 {
			s.timeoutTimer = time.AfterFunc(time.Duration(ms)*time.Millisecond, func() {
				rt.ScheduleNextTick(func() {
					if cb.IsCallable() {
						_, _ = vmInst.Call(cb, self, nil)
					}
					emitOnObject(vmInst, obj, "timeout")
				})
			})
		}
		s.timeoutMu.Unlock()
		return self, nil
	}))

	// ref()/unref(): real Node's mechanism for telling the process "don't
	// wait on this handle alone" - real undici's client-h1.js calls
	// socket.unref() the instant a keep-alive connection has no in-flight
	// request (resumeH1) specifically so a pooled idle connection can't
	// by itself keep the process running, and .ref() to undo that the
	// moment a new request reuses it. setExternalOpActive is what makes
	// that real rather than a no-op: it retires/restores the one
	// BeginExternalOp() this socket registered at connect time, which is
	// what DrainUntilIdle's WaitForExternalOp() actually waits on. Used
	// to be a pair of pure no-ops, which is exactly why a real, live
	// keep-alive socket left idle (reader/writer loops blocked on a real
	// conn.Read/write forever) hung the whole process even after undici
	// unref'd it - confirmed via a live pprof goroutine dump during a
	// real-undici fetch() E2E probe (docs/real-node-plan.md, Round 76/77).
	obj.SetOwn("ref", vm.NewNativeFunction(0, false, "ref", func(_ []vm.Value) (vm.Value, error) {
		netTrace(s.traceID, "JS called .ref()")
		s.setExternalOpActive(true)
		return self, nil
	}))
	obj.SetOwn("unref", vm.NewNativeFunction(0, false, "unref", func(_ []vm.Value) (vm.Value, error) {
		netTrace(s.traceID, "JS called .unref()")
		s.setExternalOpActive(false)
		return self, nil
	}))
	// cork/uncork: real Node batches writes between these into a single
	// underlying write; our queue already coalesces at the OS-write
	// granularity of one item per write()/end() call, and undici's h1
	// path (allowH2:false, the only path this implementation targets)
	// never calls either - honest no-ops rather than unbuilt batching.
	obj.SetOwn("cork", vm.NewNativeFunction(0, false, "cork", func(_ []vm.Value) (vm.Value, error) { return vm.Undefined, nil }))
	obj.SetOwn("uncork", vm.NewNativeFunction(0, false, "uncork", func(_ []vm.Value) (vm.Value, error) { return vm.Undefined, nil }))

	obj.SetOwn("address", vm.NewNativeFunction(0, false, "address", func(_ []vm.Value) (vm.Value, error) {
		s.mu.Lock()
		conn := s.conn
		s.mu.Unlock()
		if conn == nil {
			return vm.NewValueFromPlainObject(vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()), nil
		}
		return addrObject(vmInst, conn.LocalAddr()), nil
	}))

	return obj, self
}

func addrObject(vmInst *vm.VM, addr net.Addr) vm.Value {
	out := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	if addr == nil {
		return vm.NewValueFromPlainObject(out)
	}
	host, portStr, err := net.SplitHostPort(addr.String())
	family := "IPv4"
	if err == nil && strings.Contains(host, ":") {
		family = "IPv6"
	}
	out.SetOwn("address", vm.NewString(host))
	out.SetOwn("family", vm.NewString(family))
	if port, perr := strconv.Atoi(portStr); perr == nil {
		out.SetOwn("port", vm.NumberValue(float64(port)))
	}
	return vm.NewValueFromPlainObject(out)
}

func setConnAddrFields(vmInst *vm.VM, obj *vm.PlainObject, conn net.Conn) {
	if local, ok := addrParts(conn.LocalAddr()); ok {
		obj.SetOwn("localAddress", vm.NewString(local.host))
		obj.SetOwn("localPort", vm.NumberValue(float64(local.port)))
		obj.SetOwn("localFamily", vm.NewString(local.family))
	}
	if remote, ok := addrParts(conn.RemoteAddr()); ok {
		obj.SetOwn("remoteAddress", vm.NewString(remote.host))
		obj.SetOwn("remotePort", vm.NumberValue(float64(remote.port)))
		obj.SetOwn("remoteFamily", vm.NewString(remote.family))
	}
}

type addrParsed struct {
	host   string
	port   int
	family string
}

func addrParts(addr net.Addr) (addrParsed, bool) {
	if addr == nil {
		return addrParsed{}, false
	}
	host, portStr, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addrParsed{}, false
	}
	family := "IPv4"
	if strings.Contains(host, ":") {
		family = "IPv6"
	}
	port, _ := strconv.Atoi(portStr)
	return addrParsed{host: host, port: port, family: family}, true
}

func applyNoDelay(conn net.Conn, v bool) {
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(v)
	} else if nd, ok := conn.(interface{ SetNoDelay(bool) error }); ok {
		_ = nd.SetNoDelay(v)
	}
}

func applyKeepAlive(conn net.Conn, v bool, delay time.Duration) {
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetKeepAlive(v)
		if v && delay > 0 {
			_ = tc.SetKeepAlivePeriod(delay)
		}
	}
}

// tcpConnOf unwraps to the underlying *net.TCPConn for option-setting
// even when conn is a *tls.Conn (tls.Conn.NetConn() surfaces the raw
// conn it was built on) - so setNoDelay/setKeepAlive on a TLS socket
// still reach the real OS socket, not a no-op.
func tcpConnOf(conn net.Conn) net.Conn {
	if u, ok := conn.(interface{ NetConn() net.Conn }); ok {
		return u.NetConn()
	}
	return conn
}

func getStrOpt(opts *vm.PlainObject, key, def string) string {
	if opts == nil {
		return def
	}
	if v, ok := opts.GetOwn(key); ok && !v.IsUndefined() && v.Type() != vm.TypeNull {
		if s := v.ToString(); s != "" {
			return s
		}
	}
	return def
}

func getIntOpt(opts *vm.PlainObject, key string, def int) int {
	if opts == nil {
		return def
	}
	if v, ok := opts.GetOwn(key); ok && v.IsNumber() {
		return int(v.ToFloat())
	}
	return def
}

func getBoolOpt(opts *vm.PlainObject, key string, def bool) bool {
	if opts == nil {
		return def
	}
	if v, ok := opts.GetOwn(key); ok && !v.IsUndefined() && v.Type() != vm.TypeNull {
		return v.IsTruthy()
	}
	return def
}

// portOpt reads a `port` option that real Node (and undici's connector)
// may pass as either a number or a numeric string.
func portOpt(opts *vm.PlainObject, def string) string {
	if opts == nil {
		return def
	}
	if v, ok := opts.GetOwn("port"); ok && !v.IsUndefined() && v.Type() != vm.TypeNull {
		if v.IsNumber() {
			return strconv.Itoa(int(v.ToFloat()))
		}
		return v.ToString()
	}
	return def
}

func dialTCP(opts *vm.PlainObject) (net.Conn, error) {
	host := getStrOpt(opts, "host", getStrOpt(opts, "hostname", "localhost"))
	port := portOpt(opts, "0")
	dialer := &net.Dialer{}
	if la := getStrOpt(opts, "localAddress", ""); la != "" {
		ip := net.ParseIP(la)
		dialer.LocalAddr = &net.TCPAddr{IP: ip}
	}
	return dialer.Dial("tcp", net.JoinHostPort(host, port))
}

// doNetConnect implements net.connect()/net.createConnection() - dials
// asynchronously (never blocking the VM thread on the network) and
// returns the Socket object immediately, exactly like real Node: writes
// issued before 'connect' fires are legal and simply queue, flushed once
// the dial actually completes.
func doNetConnect(vmInst *vm.VM, optsVal vm.Value, connectCb vm.Value) vm.Value {
	opts := optsVal.AsPlainObject()
	hwm := getIntOpt(opts, "highWaterMark", defaultNetHighWaterMark)
	s := newSocketState(hwm)
	obj, self := buildSocketObject(vmInst, s)
	obj.SetOwn("connecting", vm.True)

	if connectCb.IsCallable() {
		addListener(vmInst, obj, "connect", connectCb, true, false)
	}

	rt := vmInst.GetAsyncRuntime()
	rt.BeginExternalOp()
	s.beginTrackedExternalOp(rt)
	netTrace(s.traceID, "doNetConnect: dialing host=%s port=%s", getStrOpt(opts, "host", getStrOpt(opts, "hostname", "localhost")), portOpt(opts, "0"))

	var wg sync.WaitGroup
	wg.Add(2) // writer slot + reader slot; reader's Done() fires even on dial failure (see below)

	go func() { defer wg.Done(); s.writerLoop(vmInst, rt, obj, self) }()

	go func() {
		conn, err := dialTCP(opts)
		if err != nil {
			netTrace(s.traceID, "dial failed: %v", err)
			s.mu.Lock()
			s.closed = true
			s.destroyed = true
			s.mu.Unlock()
			s.cond.Broadcast()
			rt.ScheduleNextTick(func() {
				obj.SetOwn("connecting", vm.False)
				obj.SetOwn("pending", vm.True)
				emitOnObject(vmInst, obj, "error", errorValueFromGo(vmInst, err))
			})
			wg.Done()
			return
		}

		netTrace(s.traceID, "dial succeeded, local=%s remote=%s", conn.LocalAddr(), conn.RemoteAddr())
		s.mu.Lock()
		s.conn = conn
		noDelay := s.pendingNoDelay
		keepAlive := s.pendingKeepAlive
		keepAliveDelay := s.keepAliveInitDlay
		s.mu.Unlock()
		if noDelay != nil {
			applyNoDelay(conn, *noDelay)
		}
		if keepAlive != nil {
			applyKeepAlive(conn, *keepAlive, keepAliveDelay)
		}
		s.cond.Broadcast()

		rt.ScheduleNextTick(func() {
			obj.SetOwn("connecting", vm.False)
			obj.SetOwn("pending", vm.False)
			setConnAddrFields(vmInst, obj, conn)
			emitOnObject(vmInst, obj, "connect")
			emitOnObject(vmInst, obj, "ready")
		})

		go func() { defer wg.Done(); s.readerLoop(vmInst, rt, obj) }()
	}()

	go func() {
		wg.Wait()
		netTrace(s.traceID, "reader+writer wg.Wait() returned, tearing down")
		s.endTrackedExternalOp()
	}()

	return self
}

func declareNet() {
	registerJSShim("net", netShim)
}

func installNetNatives(p *driver.Paserati) {
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
	obj.SetOwn("__noderatiNetConnect", vm.NewNativeFunction(2, true, "__noderatiNetConnect", func(args []vm.Value) (vm.Value, error) {
		var optsVal vm.Value = vm.Undefined
		var cb vm.Value = vm.Undefined
		for _, a := range args {
			if a.IsCallable() && cb.IsUndefined() {
				cb = a
			} else if a.IsObject() && optsVal.IsUndefined() {
				optsVal = a
			}
		}
		return doNetConnect(vmInst, optsVal, cb), nil
	}))
	obj.SetOwn("__noderatiIsIP", vm.NewNativeFunction(1, false, "__noderatiIsIP", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			return vm.NumberValue(0), nil
		}
		s := args[0].ToString()
		if ip := net.ParseIP(s); ip != nil {
			if ip.To4() != nil {
				return vm.NumberValue(4), nil
			}
			return vm.NumberValue(6), nil
		}
		return vm.NumberValue(0), nil
	}))
}

const netShim = `const __connect = globalThis.__noderatiNetConnect;
const __isIP = globalThis.__noderatiIsIP;

function normalizeArgs(args) {
  let options = args[0];
  let cb = undefined;
  if (typeof options === "number") {
    options = { port: options, host: typeof args[1] === "string" ? args[1] : undefined };
    cb = typeof args[1] === "function" ? args[1] : args[2];
  } else if (typeof options === "object" && options !== null) {
    cb = args[1];
  }
  return [options, typeof cb === "function" ? cb : undefined];
}

function connect(...args) {
  const [options, cb] = normalizeArgs(args);
  return __connect(options || {}, cb);
}

class Socket {
  constructor(options) {
    return connect(options);
  }
}

function isIP(input) {
  return __isIP(String(input));
}
function isIPv4(input) {
  return isIP(input) === 4;
}
function isIPv6(input) {
  return isIP(input) === 6;
}

export { connect, connect as createConnection, Socket, isIP, isIPv4, isIPv6 };
export default { connect, createConnection: connect, Socket, isIP, isIPv4, isIPv6 };
`

package host

import (
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/nooga/paserati/pkg/vm"
)

// nodeSignals maps every Node-recognized signal name this host bridges to
// the matching Go/OS signal. Node's own list (see Node's os.constants.signals)
// is longer and partly platform-specific (real-time signals, Windows-only
// names); this covers the ones an ordinary CLI actually needs - graceful
// shutdown (SIGTERM/SIGHUP/SIGINT/SIGQUIT), job control (SIGCONT/SIGTSTP),
// terminal resize (SIGWINCH), and the two user-defined signals npm packages
// commonly use for IPC-ish signaling (SIGUSR1/SIGUSR2).
var nodeSignals = map[string]syscall.Signal{
	"SIGHUP":   syscall.SIGHUP,
	"SIGINT":   syscall.SIGINT,
	"SIGQUIT":  syscall.SIGQUIT,
	"SIGILL":   syscall.SIGILL,
	"SIGTRAP":  syscall.SIGTRAP,
	"SIGABRT":  syscall.SIGABRT,
	"SIGBUS":   syscall.SIGBUS,
	"SIGFPE":   syscall.SIGFPE,
	"SIGKILL":  syscall.SIGKILL,
	"SIGUSR1":  syscall.SIGUSR1,
	"SIGSEGV":  syscall.SIGSEGV,
	"SIGUSR2":  syscall.SIGUSR2,
	"SIGPIPE":  syscall.SIGPIPE,
	"SIGALRM":  syscall.SIGALRM,
	"SIGTERM":  syscall.SIGTERM,
	"SIGCHLD":  syscall.SIGCHLD,
	"SIGCONT":  syscall.SIGCONT,
	"SIGSTOP":  syscall.SIGSTOP,
	"SIGTSTP":  syscall.SIGTSTP,
	"SIGTTIN":  syscall.SIGTTIN,
	"SIGTTOU":  syscall.SIGTTOU,
	"SIGURG":   syscall.SIGURG,
	"SIGXCPU":  syscall.SIGXCPU,
	"SIGXFSZ":  syscall.SIGXFSZ,
	"SIGWINCH": syscall.SIGWINCH,
	"SIGIO":    syscall.SIGIO,
	"SIGSYS":   syscall.SIGSYS,
}

func signalNameFromValue(v vm.Value) (string, syscall.Signal, bool) {
	if v.IsNumber() {
		n := syscall.Signal(int(v.ToFloat()))
		for name, sig := range nodeSignals {
			if sig == n {
				return name, sig, true
			}
		}
		return "", n, true
	}
	name := strings.ToUpper(v.ToString())
	if !strings.HasPrefix(name, "SIG") {
		name = "SIG" + name
	}
	sig, ok := nodeSignals[name]
	return name, sig, ok
}

// installProcessKill adds process.kill(pid, signal), real Node's actual
// signal-sending API (not a metaphor for "terminate a process" - the
// default signal, per Node's own docs, is SIGTERM, and pid === process.pid
// is a completely ordinary, supported case real npm packages use to
// self-signal, e.g. @earendil-works/pi-tui's ProcessTerminal.start()
// re-sending itself SIGWINCH to force a terminal-dimension refresh after
// suspend/resume (round 65, docs/real-node-plan.md's Phase 5 section -
// found via marker-based tracing after process.kill's total absence turned
// a synchronous TypeError into a silently-hung process with zero output,
// the same failure shape as the prependListener gap fixed just before it).
func installProcessKill(vmInstance *vm.VM, processObj *vm.PlainObject) {
	processObj.SetOwn("kill", vm.NewNativeFunction(2, false, "kill", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 || !args[0].IsNumber() {
			return vm.False, nil
		}
		pid := int(args[0].ToFloat())
		sig := syscall.SIGTERM
		if len(args) > 1 && !args[1].IsUndefined() {
			_, parsed, ok := signalNameFromValue(args[1])
			if !ok {
				return vm.Undefined, simpleNodeError(vmInstance, "ERR_UNKNOWN_SIGNAL", "Unknown signal: "+args[1].ToString())
			}
			sig = parsed
		}
		// Signal 0 is real Node's "does this process exist" probe - no
		// actual signal is sent.
		if sig == 0 {
			proc, err := os.FindProcess(pid)
			if err != nil {
				return vm.Undefined, simpleNodeError(vmInstance, "ESRCH", "kill ESRCH")
			}
			if err := proc.Signal(syscall.Signal(0)); err != nil {
				return vm.Undefined, simpleNodeError(vmInstance, "ESRCH", "kill ESRCH")
			}
			return vm.True, nil
		}
		proc, err := os.FindProcess(pid)
		if err != nil {
			return vm.Undefined, simpleNodeError(vmInstance, "ESRCH", "kill ESRCH")
		}
		if err := proc.Signal(sig); err != nil {
			return vm.Undefined, simpleNodeError(vmInstance, "ESRCH", "kill ESRCH: "+err.Error())
		}
		return vm.True, nil
	}))
}

// simpleNodeError builds a Go error that throws as a real Error object
// with a Node-style .code property set, the same shape fs_errors.go's
// wrapFsErr builds for filesystem errors - kept generic (not fs-specific)
// here since process.kill's errors (ESRCH, ERR_UNKNOWN_SIGNAL) aren't
// filesystem errors at all.
type simpleException struct {
	exception vm.Value
	message   string
}

func (e *simpleException) Error() string               { return e.message }
func (e *simpleException) GetExceptionValue() vm.Value { return e.exception }

func simpleNodeError(vmInst *vm.VM, code, message string) error {
	exception, built := vm.Undefined, false
	if errCtor, ok := vmInst.GetGlobal("Error"); ok {
		if v, cerr := vmInst.Construct(errCtor, []vm.Value{vm.NewString(message)}); cerr == nil {
			exception, built = v, true
		}
	}
	if !built {
		obj := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
		obj.SetOwn("name", vm.NewString("Error"))
		obj.SetOwn("message", vm.NewString(message))
		exception = vm.NewValueFromPlainObject(obj)
	}
	if obj := exception.AsPlainObject(); obj != nil {
		obj.SetOwn("code", vm.NewString(code))
	}
	return &simpleException{exception: exception, message: message}
}

// startSignalBridge re-emits real OS signals as "signalName" events on
// processObj, matching real Node's process being a genuine target for
// process.on("SIGTERM", ...) etc. Without this, code that calls
// process.kill(process.pid, "SIGWINCH") to self-signal (or a real
// `kill -TERM <pid>` from outside) would have the signal correctly
// delivered at the OS level but nothing in this VM would ever know it
// happened - registerSignalHandlers-shaped code would sit registered but
// silently inert.
//
// Bridging a signal is gated on JS actually listening for it - the fix for
// a real, user-visible bug found empirically (docs/real-node-plan.md,
// Round 102/103): the previous version called signal.Notify for every
// bridgeable signal unconditionally at startup, which - as signal.Notify
// always does - replaces the OS's own default disposition (terminate the
// process, for SIGINT/SIGTERM/SIGHUP/SIGQUIT) with "relay to this
// process's own event loop", even when nothing in JS was listening for it.
// A running noderati process with zero signal listeners registered simply
// ignored Ctrl-C forever, unlike every other real Node CLI ever written -
// real Node only intercepts a signal's default disposition once JS adds
// its first process.on(signalName, ...) listener, via libuv's own
// uv_signal_start, and restores the OS default the instant the last such
// listener is removed. signalBridge (below) reproduces exactly that:
// activate()/deactivate() are called from wireProcessSignalListeners, which
// overrides processObj's own on/addListener/once/prependListener/
// prependOnceListener/off/removeListener/removeAllListeners to detect a
// signal-shaped event name's listener count crossing 0<->1.
//
// The relay goroutine itself is unconditional and permanent - it just
// blocks on an unsubscribed channel until the first signal is ever
// actively bridged, at zero cost. Each delivery is scheduled onto the VM's
// own event loop via ScheduleNextTick rather than emitted directly from
// this goroutine, since VM state must only be touched from the VM's own
// execution flow.
func startSignalBridge(vmInstance *vm.VM, processObj *vm.PlainObject) {
	sigBySignal := make(map[os.Signal]string, len(nodeSignals))
	for name, sig := range nodeSignals {
		// SIGKILL/SIGSTOP are deliberately still in nodeSignals (so
		// process.kill(pid, "SIGKILL") can still send them - that part is
		// real and unconditional) but are never bridged: no process can
		// catch either one, on any OS, ever - real Node doesn't attempt to
		// bridge them either (and throws if JS tries to listen for them).
		// This is an intentional asymmetry between the two signal tables
		// installProcessKill and startSignalBridge draw from, not a bug -
		// don't "fix" it by removing them from nodeSignals.
		if sig == syscall.SIGKILL || sig == syscall.SIGSTOP {
			continue
		}
		sigBySignal[sig] = name
	}

	ch := make(chan os.Signal, 8)
	bridge := &signalBridge{ch: ch, active: make(map[syscall.Signal]bool)}
	rt := vmInstance.GetAsyncRuntime()
	go func() {
		for sig := range ch {
			name, ok := sigBySignal[sig]
			if !ok {
				continue
			}
			rt.ScheduleNextTick(func() {
				emitOnObject(vmInstance, processObj, name)
				// A once()-registered listener (the common shutdown-hook
				// shape: process.once("SIGINT", cleanup)) has already
				// self-removed by the time emitOnObject returns - via
				// addListener's onceWrapper calling the bare
				// removeListener helper directly (emitter.go), never
				// through wireProcessSignalListeners' own on/off
				// overrides below. Left unchecked, that self-removal
				// would drop listenerCount to 0 without ever calling
				// bridge.deactivate - the exact bug this whole fix exists
				// to close, back in a narrower, very real shape: a
				// process.once("SIGINT", ...) shutdown hook fires once,
				// silently leaves SIGINT intercepted forever, and a
				// second Ctrl-C (the real-world "cleanup hung, force
				// it" gesture) does nothing. Checked here, once per
				// delivery, on the VM thread (where listener bookkeeping
				// is stable) rather than relying solely on the
				// registration-side overrides to catch every removal
				// path.
				if listenerCount(processObj, name) == 0 {
					if s, ok := sig.(syscall.Signal); ok {
						bridge.deactivate(s)
					}
				}
			})
		}
	}()

	wireProcessSignalListeners(vmInstance, processObj, bridge)
}

// signalBridge tracks which signals JS currently has at least one listener
// for, and keeps the OS-level subscription on ch in sync with that set -
// the dynamic, listener-gated replacement for startSignalBridge's old
// unconditional signal.Notify. All mutation goes through activate/
// deactivate/deactivateAll, each holding mu for its own check-then-resync.
type signalBridge struct {
	mu     sync.Mutex
	ch     chan os.Signal
	active map[syscall.Signal]bool
}

func (b *signalBridge) activate(sig syscall.Signal) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.active[sig] {
		return
	}
	b.active[sig] = true
	b.resync()
}

func (b *signalBridge) deactivate(sig syscall.Signal) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.active[sig] {
		return
	}
	delete(b.active, sig)
	b.resync()
}

func (b *signalBridge) deactivateAll() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.active) == 0 {
		return
	}
	b.active = make(map[syscall.Signal]bool)
	b.resync()
}

// resync must be called with mu held. signal.Notify/Stop don't offer a
// per-signal "stop relaying just this one, keep the rest" on a shared
// channel - Stop(ch) always undoes every prior Notify(ch, ...) for that
// channel at once - so the whole active set is recomputed from scratch on
// every transition instead of attempting incremental adds/removes. Stop
// also restores each now-unwanted signal's OS-level default disposition
// (Go's own os/signal semantics: a signal keeps its default action for as
// long as nothing has ever Notify'd it, and Stop un-Notifies it the moment
// no channel is left registered for it) - exactly the "no listener means
// default OS behavior" contract this bridge exists to provide.
func (b *signalBridge) resync() {
	signal.Stop(b.ch)
	if len(b.active) == 0 {
		return
	}
	sigs := make([]os.Signal, 0, len(b.active))
	for sig := range b.active {
		sigs = append(sigs, sig)
	}
	signal.Notify(b.ch, sigs...)
}

// wireProcessSignalListeners overrides processObj's own on/addListener/
// once/prependListener/prependOnceListener/off/removeListener/
// removeAllListeners - already installed generically by
// newEventEmitterObject - so that registering or removing a listener for a
// signal-shaped event name (exactly "SIGINT", "SIGTERM", etc. - Node signal
// event names are exact, case-sensitive strings, never normalized the way
// process.kill's more permissive signal argument is) also
// activates/deactivates that signal's OS-level bridging. Every other event
// name (e.g. "exit", "uncaughtException") passes straight through to the
// same generic addListener/removeListener/removeAllListeners helpers
// newEventEmitterObject itself already uses - this is a thin wrapper
// around them, not a second listener-bookkeeping implementation.
func wireProcessSignalListeners(vmInstance *vm.VM, processObj *vm.PlainObject, bridge *signalBridge) {
	bridgeableSignal := func(name string) (syscall.Signal, bool) {
		sig, ok := nodeSignals[name]
		if !ok || sig == syscall.SIGKILL || sig == syscall.SIGSTOP {
			return 0, false
		}
		return sig, true
	}

	registerAdd := func(name string, once, prepend bool) {
		processObj.SetOwn(name, vm.NewNativeFunction(2, false, name, func(args []vm.Value) (vm.Value, error) {
			self := vm.NewValueFromPlainObject(processObj)
			if len(args) < 2 {
				return self, nil
			}
			event := args[0].ToString()
			before := listenerCount(processObj, event)
			result := addListener(vmInstance, processObj, event, args[1], once, prepend)
			if before == 0 {
				if sig, ok := bridgeableSignal(event); ok {
					bridge.activate(sig)
				}
			}
			return result, nil
		}))
	}
	registerAdd("on", false, false)
	registerAdd("addListener", false, false)
	registerAdd("once", true, false)
	registerAdd("prependListener", false, true)
	registerAdd("prependOnceListener", true, true)

	registerRemove := func(name string) {
		processObj.SetOwn(name, vm.NewNativeFunction(2, false, name, func(args []vm.Value) (vm.Value, error) {
			self := vm.NewValueFromPlainObject(processObj)
			if len(args) < 2 {
				return self, nil
			}
			event := args[0].ToString()
			result := removeListener(processObj, event, args[1])
			if sig, ok := bridgeableSignal(event); ok && listenerCount(processObj, event) == 0 {
				bridge.deactivate(sig)
			}
			return result, nil
		}))
	}
	registerRemove("off")
	registerRemove("removeListener")

	processObj.SetOwn("removeAllListeners", vm.NewNativeFunction(1, false, "removeAllListeners", func(args []vm.Value) (vm.Value, error) {
		if len(args) == 0 {
			// No event name - real Node clears every listener for every
			// event; every signal this bridge currently has active loses
			// its only listener(s) at once, so deactivate all of them.
			result := removeAllListeners(processObj, args)
			bridge.deactivateAll()
			return result, nil
		}
		event := args[0].ToString()
		result := removeAllListeners(processObj, args)
		if sig, ok := bridgeableSignal(event); ok {
			bridge.deactivate(sig)
		}
		return result, nil
	}))
}

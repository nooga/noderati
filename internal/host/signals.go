package host

import (
	"os"
	"os/signal"
	"strings"
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

// startSignalBridge subscribes to every real OS signal this host recognizes
// and re-emits each as a "signalName" event on processObj, matching real
// Node's process being a genuine target for process.on("SIGTERM", ...) etc.
// Without this, code that calls process.kill(process.pid, "SIGWINCH") to
// self-signal (or a real `kill -TERM <pid>` from outside) would have the
// signal correctly delivered at the OS level but nothing in this VM would
// ever know it happened - registerSignalHandlers-shaped code would sit
// registered but silently inert.
//
// Runs on its own goroutine reading from the OS; each delivery is scheduled
// onto the VM's own event loop via ScheduleNextTick rather than emitted
// directly from that goroutine, since VM state must only be touched from
// the VM's own execution flow.
func startSignalBridge(vmInstance *vm.VM, processObj *vm.PlainObject) {
	names := make([]string, 0, len(nodeSignals))
	sigs := make([]os.Signal, 0, len(nodeSignals))
	for name, sig := range nodeSignals {
		// SIGKILL/SIGSTOP are deliberately still in nodeSignals (so
		// process.kill(pid, "SIGKILL") can still send them - that part is
		// real and unconditional) but are skipped here rather than passed
		// to signal.Notify: no process can catch or bridge either one, on
		// any OS, ever - real Node doesn't attempt to bridge them either.
		// This is an intentional asymmetry between the two signal tables
		// installProcessKill and startSignalBridge draw from, not a bug -
		// don't "fix" it by removing them from nodeSignals.
		if sig == syscall.SIGKILL || sig == syscall.SIGSTOP {
			continue
		}
		names = append(names, name)
		sigs = append(sigs, sig)
	}
	sigBySignal := make(map[os.Signal]string, len(names))
	for i, sig := range sigs {
		sigBySignal[sig] = names[i]
	}

	ch := make(chan os.Signal, 8)
	signal.Notify(ch, sigs...)
	rt := vmInstance.GetAsyncRuntime()
	go func() {
		for sig := range ch {
			name, ok := sigBySignal[sig]
			if !ok {
				continue
			}
			rt.ScheduleNextTick(func() {
				emitOnObject(vmInstance, processObj, name)
			})
		}
	}()
}

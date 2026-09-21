package host

import (
	"fmt"
	"os"

	"github.com/nooga/paserati/pkg/vm"
)

// osExit is os.Exit, overridden in tests so they can observe the exit
// request (code, and that it happened) without killing the test binary.
// Mirrors paserati's own identical pattern (pkg/driver/host_timers.go),
// added alongside #484.
var osExit = os.Exit

// reportUncaughtCallbackException reports an exception thrown from a host
// callback dispatch with no catching JS context above it -- a scheduled
// setTimeout/setImmediate callback, most concretely -- the same way real
// Node crashes the process for an uncaught exception escaping an
// event-loop callback, and the same way paserati's own setTimeout/
// nextTick now do (paserati#484, fixed upstream in host_timers.go/
// process_init.go via vm.FormatUncaughtCallError).
//
// This file exists because that upstream fix doesn't reach every timer
// call site in this codebase: timeout_object.go wraps paserati's real
// setTimeout to return a Node-shaped Timeout object, and for the
// ref-counted path it reschedules the user's callback directly through
// the AsyncRuntime (`rt.ScheduleTimer`/`rt.ScheduleUnrefTimer`) rather
// than going back through paserati's own (now-fixed) setTimeoutFn - so it
// silently reintroduced the exact bug #484 just fixed, entirely within
// noderati's own code. immediate_object.go's setImmediate has never gone
// through paserati's dispatcher at all (setImmediate doesn't exist in
// paserati - it's a noderati-only global), so it never got #484's fix
// either. Confirmed as a real, current gap this round: a real webpack
// compile's own `AsyncQueue` schedules its queue-draining function via
// `setImmediate(root._ensureProcessing)`, and once `_ensureProcessing`
// throws, the exception vanished with zero diagnostics even after
// pulling #484 - the crash never reached webpack because it never went
// through paserati's fixed code path.
func reportUncaughtCallbackException(vmInst *vm.VM, err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, vmInst.FormatUncaughtCallError(err))
	osExit(1)
}

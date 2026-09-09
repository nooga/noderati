package host

import (
	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// message_port_global.go implements the WHATWG `MessagePort` global -
// found missing the same way `File` was (round 73, docs/real-node-plan.md):
// real undici's lib/web/webidl/index.js does `webidl.is.MessagePort =
// webidl.util.MakeTypeAssertion(MessagePort)` at its own module top
// level, so a missing `MessagePort` throws immediately at require()
// time. Grepped every other real MessagePort reference in the vendored
// package before writing this: all of them (lib/web/websocket/events.js)
// are WebIDL converter setup for MessageEvent's own `source`/`ports`
// fields - WebSocket-specific, never reached by a plain fetch() call,
// and none of them ever actually construct a MessagePort or call a
// method on one.
//
// Built as a real, if genuinely minimal, EventEmitter-shaped
// constructible class (on newEventEmitterObject - the same base every
// other real emitter in this codebase already uses) rather than a bare
// placeholder function: postMessage/start are honest no-ops (there is
// no paired port for a message to actually flow to - this is not
// MessageChannel, which would need two ports wired together, and
// nothing reachable here ever constructs one), and close() is real -
// it actually emits 'close', matching real Node's own documented
// behavior for a port that's been shut down. A native Go constructor,
// not JS run through EvalCode/RunCode, for the same reason
// file_global.go is (see its own doc comment - a real, filed paserati
// bug, #298, makes a second top-level script run on the same instance
// corrupt later exception propagation).
func installMessagePortGlobal(p *driver.Paserati) {
	vmInst := p.GetVM()
	if vmInst == nil {
		return
	}
	gt, ok := vmInst.GetGlobal("globalThis")
	if !ok {
		return
	}
	gobj := gt.AsPlainObject()
	if gobj == nil {
		return
	}
	if _, exists := gobj.GetOwn("MessagePort"); exists {
		return
	}

	ctor := vm.NewConstructorWithProps(0, false, "MessagePort", func(_ []vm.Value) (vm.Value, error) {
		obj := newEventEmitterObject(vmInst)
		self := vm.NewValueFromPlainObject(obj)
		obj.SetOwn("postMessage", vm.NewNativeFunction(0, true, "postMessage", func(_ []vm.Value) (vm.Value, error) {
			return vm.Undefined, nil
		}))
		obj.SetOwn("start", vm.NewNativeFunction(0, false, "start", func(_ []vm.Value) (vm.Value, error) {
			return vm.Undefined, nil
		}))
		obj.SetOwn("close", vm.NewNativeFunction(0, false, "close", func(_ []vm.Value) (vm.Value, error) {
			scheduleEmit(vmInst, obj, "close")
			return vm.Undefined, nil
		}))
		return self, nil
	})

	gobj.SetOwn("MessagePort", ctor)
}

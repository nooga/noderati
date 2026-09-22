package host

import (
	"sync"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// message_port_global.go implements MessageChannel/MessagePort (globals
// and node:worker_threads exports) and receiveMessageOnPort, in-process:
// two entangled ports, each with an incoming queue. postMessage
// structured-clones the value (transferred ports pass through by
// identity, since both ends live in this one VM) onto the peer's queue;
// a started port drains its queue as 'message' events. Listening via
// on('message') starts a port implicitly, as in Node, and a started port
// keeps the event loop alive until it's closed or unref()'d.
//
// Real tinypool (vitest's worker pool) routes every task through one of
// these; the previous MessagePort was a placeholder whose postMessage
// dropped every message.

type messagePort struct {
	vmInst   *vm.VM
	obj      *vm.PlainObject
	peer     *messagePort
	queue    []vm.Value
	started  bool
	closed   bool
	refed    bool
	held     bool
	draining bool
}

func portOf(v vm.Value) *messagePort {
	if v.Type() != vm.TypeObject {
		return nil
	}
	p, _ := v.AsPlainObject().InternalSlots().(*messagePort)
	return p
}

func (p *messagePort) updateHold() {
	want := p.started && !p.closed && p.refed
	rt := p.vmInst.GetAsyncRuntime()
	if want && !p.held {
		p.held = true
		rt.BeginExternalOp()
	} else if !want && p.held {
		p.held = false
		rt.EndExternalOp()
	}
}

func (p *messagePort) start() {
	if p.started || p.closed {
		return
	}
	p.started = true
	p.updateHold()
	p.scheduleDrain()
}

func (p *messagePort) scheduleDrain() {
	if p.draining || !p.started || len(p.queue) == 0 {
		return
	}
	p.draining = true
	p.vmInst.GetAsyncRuntime().ScheduleNextTick(func() {
		p.draining = false
		for len(p.queue) > 0 && p.started && !p.closed {
			msg := p.queue[0]
			p.queue = p.queue[1:]
			emitOnObject(p.vmInst, p.obj, "message", msg)
		}
	})
}

func (p *messagePort) close() {
	for _, q := range []*messagePort{p, p.peer} {
		if q == nil || q.closed {
			continue
		}
		q.closed = true
		q.queue = nil
		q.updateHold()
		scheduleEmit(q.vmInst, q.obj, "close")
	}
}

func dataCloneError(vmInst *vm.VM, message string) error {
	errVal := newJSError(vmInst, message)
	if obj := errVal.AsPlainObject(); obj != nil {
		obj.SetOwn("name", vm.NewString("DataCloneError"))
		obj.SetOwn("code", vm.NumberValue(25))
	}
	return &fsSystemError{exception: errVal, message: message}
}

func (p *messagePort) postMessage(args []vm.Value) error {
	value := argAt(args, 0)
	seen := make(map[any]vm.Value)
	transfer := argAt(args, 1)
	if transfer.Type() == vm.TypeObject {
		transfer, _ = objOption(transfer, "transfer")
	}
	if transfer.Type() == vm.TypeArray {
		for _, t := range arrayValues(transfer.AsArray()) {
			if portOf(t) != nil {
				seen[t.AsPlainObject()] = t
			}
		}
	}
	clone, err := structuredCloneValue(p.vmInst, value, seen)
	if err != nil {
		return dataCloneError(p.vmInst, err.Error())
	}
	if p.closed || p.peer == nil || p.peer.closed {
		return nil
	}
	p.peer.queue = append(p.peer.queue, clone)
	p.peer.scheduleDrain()
	return nil
}

func newMessagePort(vmInst *vm.VM, proto vm.Value) *messagePort {
	obj := newEventEmitterObject(vmInst)
	obj.SetPrototype(proto)
	// Node's MessagePort is EventTarget-based: no enumerable own state.
	if events, ok := obj.GetOwn("_events"); ok {
		yes, no := true, false
		obj.DefineOwnProperty("_events", events, &yes, &no, &yes)
	}
	p := &messagePort{vmInst: vmInst, obj: obj, refed: true}
	obj.SetInternalSlots(p)
	self := vm.NewValueFromPlainObject(obj)

	obj.SetOwn("postMessage", vm.NewNativeFunction(1, true, "postMessage", func(args []vm.Value) (vm.Value, error) {
		return vm.Undefined, p.postMessage(args)
	}))
	obj.SetOwn("start", vm.NewNativeFunction(0, false, "start", func(_ []vm.Value) (vm.Value, error) {
		p.start()
		return vm.Undefined, nil
	}))
	obj.SetOwn("close", vm.NewNativeFunction(0, false, "close", func(_ []vm.Value) (vm.Value, error) {
		p.close()
		return vm.Undefined, nil
	}))
	obj.SetOwn("ref", vm.NewNativeFunction(0, false, "ref", func(_ []vm.Value) (vm.Value, error) {
		p.refed = true
		p.updateHold()
		return self, nil
	}))
	obj.SetOwn("unref", vm.NewNativeFunction(0, false, "unref", func(_ []vm.Value) (vm.Value, error) {
		p.refed = false
		p.updateHold()
		return self, nil
	}))
	obj.SetOwn("hasRef", vm.NewNativeFunction(0, false, "hasRef", func(_ []vm.Value) (vm.Value, error) {
		return vm.BooleanValue(p.held), nil
	}))
	// A 'message' listener added through the EventEmitter API starts the
	// port, like Node's NodeEventTarget does.
	for _, name := range []string{"on", "addListener", "once", "prependListener", "prependOnceListener"} {
		orig, _ := obj.GetOwn(name)
		obj.SetOwn(name, vm.NewNativeFunction(2, false, name, func(args []vm.Value) (vm.Value, error) {
			res, err := vmInst.Call(orig, self, args)
			if len(args) > 0 && args[0].ToString() == "message" {
				p.start()
			}
			return res, err
		}))
	}
	// EventTarget-style listeners receive a MessageEvent, not the raw value.
	obj.SetOwn("addEventListener", vm.NewNativeFunction(2, true, "addEventListener", func(args []vm.Value) (vm.Value, error) {
		if len(args) < 2 || !args[1].IsCallable() {
			return vm.Undefined, nil
		}
		typ, fn := args[0].ToString(), args[1]
		wrapper := vm.NewNativeFunction(1, false, "listener", func(evArgs []vm.Value) (vm.Value, error) {
			ev := newMessageEvent(vmInst, typ, argAt(evArgs, 0))
			return vmInst.Call(fn, self, []vm.Value{ev})
		})
		addListener(vmInst, obj, typ, wrapper, false, false)
		return vm.Undefined, nil
	}))
	return p
}

func newMessageEvent(vmInst *vm.VM, typ string, data vm.Value) vm.Value {
	if ctor, ok := globalValue(vmInst, "MessageEvent"); ok {
		init := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
		init.SetOwn("data", data)
		if ev, err := vmInst.Construct(ctor, []vm.Value{vm.NewString(typ), vm.NewValueFromPlainObject(init)}); err == nil {
			return ev
		}
	}
	ev := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	ev.SetOwn("type", vm.NewString(typ))
	ev.SetOwn("data", data)
	return vm.NewValueFromPlainObject(ev)
}

func globalValue(vmInst *vm.VM, name string) (vm.Value, bool) {
	gt, ok := vmInst.GetGlobal("globalThis")
	if !ok {
		return vm.Undefined, false
	}
	gobj := gt.AsPlainObject()
	if gobj == nil {
		return vm.Undefined, false
	}
	v, ok := gobj.GetOwn(name)
	return v, ok && !v.IsUndefined()
}

// messageChannelAPI builds MessagePort, MessageChannel and
// receiveMessageOnPort once per VM, shared by the globals and
// node:worker_threads.
type messageChannelAPI struct {
	port, channel, receive vm.Value
}

var messageChannelAPIs sync.Map // *vm.VM -> *messageChannelAPI

func messageChannelFor(vmInst *vm.VM) *messageChannelAPI {
	if api, ok := messageChannelAPIs.Load(vmInst); ok {
		return api.(*messageChannelAPI)
	}
	portProto := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	portProtoVal := vm.NewValueFromPlainObject(portProto)
	portCtor := vm.NewConstructorWithProps(0, false, "MessagePort", func(_ []vm.Value) (vm.Value, error) {
		return vm.Undefined, newNodeTypeError(vmInst, "ERR_CONSTRUCT_CALL_INVALID", "Illegal constructor")
	})
	if props := portCtor.AsNativeFunctionWithProps(); props != nil && props.Properties != nil {
		props.Properties.DefineFixedProperty("prototype", portProtoVal)
	}
	portProto.SetOwnNonEnumerable("constructor", portCtor)

	channelProto := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	channelCtor := vm.NewConstructorWithProps(0, false, "MessageChannel", func(_ []vm.Value) (vm.Value, error) {
		a := newMessagePort(vmInst, portProtoVal)
		b := newMessagePort(vmInst, portProtoVal)
		a.peer, b.peer = b, a
		ch := vm.NewObject(vm.NewValueFromPlainObject(channelProto)).AsPlainObject()
		ch.SetOwn("port1", vm.NewValueFromPlainObject(a.obj))
		ch.SetOwn("port2", vm.NewValueFromPlainObject(b.obj))
		return vm.NewValueFromPlainObject(ch), nil
	})
	if props := channelCtor.AsNativeFunctionWithProps(); props != nil && props.Properties != nil {
		props.Properties.DefineFixedProperty("prototype", vm.NewValueFromPlainObject(channelProto))
	}
	channelProto.SetOwnNonEnumerable("constructor", channelCtor)

	// receiveMessageOnPort takes the next queued message synchronously.
	receive := vm.NewNativeFunction(1, false, "receiveMessageOnPort", func(args []vm.Value) (vm.Value, error) {
		p := portOf(argAt(args, 0))
		if p == nil {
			return vm.Undefined, newNodeTypeError(vmInst, "ERR_INVALID_ARG_TYPE", `The "port" argument must be a MessagePort instance`)
		}
		if len(p.queue) == 0 {
			return vm.Undefined, nil
		}
		msg := p.queue[0]
		p.queue = p.queue[1:]
		out := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
		out.SetOwn("message", msg)
		return vm.NewValueFromPlainObject(out), nil
	})
	api := &messageChannelAPI{port: portCtor, channel: channelCtor, receive: receive}
	actual, _ := messageChannelAPIs.LoadOrStore(vmInst, api)
	return actual.(*messageChannelAPI)
}

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
	api := messageChannelFor(vmInst)
	gobj.SetOwn("MessagePort", api.port)
	gobj.SetOwn("MessageChannel", api.channel)
}

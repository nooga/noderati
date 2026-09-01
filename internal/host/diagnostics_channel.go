package host

// diagnosticsChannelShim implements Node's node:diagnostics_channel API.
// Real packages (lru-cache's node build is what surfaced this) probe for
// it defensively for optional instrumentation — channel()/hasSubscribers/
// publish()/tracingChannel(). Since this module has no real subscribers
// wired up anywhere in noderati, the important behavioral contract is
// just that hasSubscribers stays false and every trace*/publish call is
// a correct no-op that still runs the wrapped function and returns its
// result — not that any of this actually delivers diagnostics anywhere.
//
// Written as a pure JS shim (not a Go native module) because
// tracingChannel's trace* methods take a JS callback as their first
// argument, and paserati's ModuleBuilder.Function can't currently
// receive a raw JS function value through a vm.Value-typed Go parameter
// (see https://github.com/nooga/paserati/issues/162) — there's nothing
// here that needs real Go-side capability anyway.
const diagnosticsChannelShim = `class Channel {
  constructor(name) {
    this.name = name;
    this._subscribers = [];
  }
  get hasSubscribers() {
    return this._subscribers.length > 0;
  }
  publish(message) {
    for (const fn of this._subscribers.slice()) {
      try {
        fn(message, this.name);
      } catch {
        // A subscriber throwing must not break the publisher.
      }
    }
  }
  subscribe(onMessage) {
    this._subscribers.push(onMessage);
  }
  unsubscribe(onMessage) {
    const i = this._subscribers.indexOf(onMessage);
    if (i === -1) return false;
    this._subscribers.splice(i, 1);
    return true;
  }
  bindStore(_store, _transform) {
    // AsyncLocalStorage integration -- not implemented, no-op.
  }
  unbindStore() {}
  runStores(_context, fn, thisArg, ...args) {
    return fn.apply(thisArg, args);
  }
}

const channels = new Map();

export function channel(name) {
  let ch = channels.get(name);
  if (!ch) {
    ch = new Channel(name);
    channels.set(name, ch);
  }
  return ch;
}

export function hasSubscribers(name) {
  return channel(name).hasSubscribers;
}

export function subscribe(name, onMessage) {
  channel(name).subscribe(onMessage);
}

export function unsubscribe(name, onMessage) {
  return channel(name).unsubscribe(onMessage);
}

const TRACE_EVENTS = ["start", "end", "asyncStart", "asyncEnd", "error"];

class TracingChannel {
  constructor(nameOrChannels) {
    if (typeof nameOrChannels === "string") {
      for (const key of TRACE_EVENTS) {
        this[key] = channel("tracing:" + nameOrChannels + ":" + key);
      }
    } else {
      for (const key of TRACE_EVENTS) {
        this[key] = nameOrChannels[key];
      }
    }
  }
  get hasSubscribers() {
    return TRACE_EVENTS.some((key) => this[key].hasSubscribers);
  }
  subscribe(handlers) {
    for (const key of TRACE_EVENTS) {
      if (handlers[key]) this[key].subscribe(handlers[key]);
    }
  }
  unsubscribe(handlers) {
    for (const key of TRACE_EVENTS) {
      if (handlers[key]) this[key].unsubscribe(handlers[key]);
    }
  }
  traceSync(fn, context = {}, thisArg, ...args) {
    this.start.publish(context);
    try {
      const result = fn.apply(thisArg, args);
      context.result = result;
      return result;
    } catch (err) {
      context.error = err;
      this.error.publish(context);
      throw err;
    } finally {
      this.end.publish(context);
    }
  }
  tracePromise(fn, context = {}, thisArg, ...args) {
    this.start.publish(context);
    let promise;
    try {
      promise = fn.apply(thisArg, args);
    } catch (err) {
      context.error = err;
      this.error.publish(context);
      this.end.publish(context);
      throw err;
    }
    this.end.publish(context);
    this.asyncStart.publish(context);
    return Promise.resolve(promise).then(
      (result) => {
        context.result = result;
        this.asyncEnd.publish(context);
        return result;
      },
      (err) => {
        context.error = err;
        this.error.publish(context);
        this.asyncEnd.publish(context);
        throw err;
      }
    );
  }
  traceCallback(fn, position = -1, context = {}, thisArg, ...args) {
    const idx = position < 0 ? args.length + position : position;
    const cb = args[idx];
    if (typeof cb !== "function") {
      throw new TypeError("callback argument must be a function");
    }
    const argsCopy = args.slice();
    argsCopy[idx] = (err, ...cbArgs) => {
      if (err) {
        context.error = err;
        this.error.publish(context);
      } else {
        context.result = cbArgs.length === 1 ? cbArgs[0] : cbArgs;
      }
      this.asyncStart.publish(context);
      try {
        return cb.apply(this, [err, ...cbArgs]);
      } finally {
        this.asyncEnd.publish(context);
      }
    };
    this.start.publish(context);
    try {
      return fn.apply(thisArg, argsCopy);
    } catch (err) {
      context.error = err;
      this.error.publish(context);
      throw err;
    } finally {
      this.end.publish(context);
    }
  }
}

export function tracingChannel(nameOrChannels) {
  return new TracingChannel(nameOrChannels);
}

export default { channel, hasSubscribers, subscribe, unsubscribe, tracingChannel, Channel, TracingChannel };
`

func declareDiagnosticsChannel() {
	registerJSShim("diagnostics_channel", diagnosticsChannelShim)
}

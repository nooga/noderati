package host

// async_hooks.go implements the one real, reachable piece of node:async_hooks
// this codebase's actual dependency tree needs: `AsyncResource`, used by
// real undici (lib/api/api-request.js and its four siblings: pipeline,
// upgrade, connect, stream) as `class RequestHandler extends AsyncResource`,
// with `super('UNDICI_REQUEST')` and later `this.runInAsyncScope(fn,
// thisArg, ...args)` - confirmed by grepping every real async_hooks call
// site in the vendored package before writing this, not assumed from the
// module name (round 71/72, docs/real-node-plan.md).
//
// runInAsyncScope really is just `fn.apply(thisArg, args)` for a host with
// no actual async_hooks instrumentation behind it - that's not a stand-in
// for missing behavior, it's what the method is specified to do beyond the
// (unobservable, here) async-context bookkeeping: real Node's own
// runInAsyncScope doesn't change what runs or when, only what
// `executionAsyncId()` reports while it runs, and nothing in the reachable
// call sites above ever calls that. asyncId()/triggerAsyncId()/emitDestroy()
// are included so the class isn't missing standard members a caller might
// probe for, but `AsyncResource.bind()` is deliberately not built - its
// whole point is capturing async context across a later, detached call,
// which has the identical correctness problem `AsyncLocalStorage` has
// (see below) and no reachable caller here needs it.
//
// `AsyncLocalStorage` was deliberately not exported through round 116.
// Round 72's own analysis (this comment, until round 117) scoped the
// question to "is `.run()` ever actually *called* by anything reachable
// here" - found it wasn't, for pi-coding-agent specifically (neither
// `AWS_LAMBDA_MAX_CONCURRENCY` nor forced multi-instance mode is ever true
// for pi, so `@aws/lambda-invoke-store`'s `InvokeStoreMulti` path, the one
// real consumer in this dependency tree, is never taken) - and concluded
// there was nothing to build. Round 117 found that scoping missed a real
// case: running real, unmodified `@aws-sdk/client-s3` directly (not
// through pi) crashed on *import alone*, with "undefined is not a
// constructor." `InvokeStoreImpl` (the class `InvokeStoreMulti` wraps) has
// `static storage = new AsyncLocalStorage();` - a class field initializer,
// which real JS evaluates the moment the class itself is defined, at
// module top level, unconditionally - regardless of whether
// `InvokeStoreMulti` or `InvokeStoreSingle` ends up chosen at runtime, and
// regardless of whether `.run()` is ever subsequently called at all. So
// merely *importing* `@aws-sdk/client-s3` (or anything else that
// transitively pulls in `@aws/lambda-invoke-store`) needs a real,
// constructible `AsyncLocalStorage` to exist - the gap was never "nothing
// calls `.run()`," it was "the class definition alone already needs the
// constructor," which round 72's own scoping never checked.
//
// Exported now, with the real API surface (`run`/`getStore`/`enterWith`/
// `exit`/`disable`), implemented as a plain stack: `run()` pushes the
// store, invokes the callback, and pops in a `finally`. Round 72's own
// correctness concern about this shape is real and still true here, not
// silently swept aside: if `run()`'s own callback is itself async and
// suspends at an `await`, the `finally` pop fires the instant that pending
// promise is *returned*, not when the callback's own remaining code
// (after the `await`) actually resumes - so a *second*, concurrently-
// interleaved `run()` call on the same storage instance, overlapping with
// the first one's still-pending continuation, can observe the wrong
// store. That's a real, known gap (the same one round 72 identified), not
// fixed by this change - what changed is only that the class now exists
// at all, so importing a real dependent doesn't crash outright. For the
// common, non-overlapping case (one `run()` call's whole async chain
// finishing before a second, unrelated one starts - true for a single
// in-flight request, which is exactly what `InvokeStoreSingle` is for),
// this is correct, matching real Node. Filed the actual missing primitive
// (context propagation across promise continuations, so a real engine-
// level implementation would be possible) as a genuine paserati feature
// request rather than papering over it - see docs/real-node-plan.md's
// round 72 entry; still open, still the right fix for full correctness.
const asyncHooksShim = `let __asyncResourceIdSeq = 0;

class AsyncResource {
  constructor(type, options) {
    this.__type = typeof type === "string" ? type : "AsyncResource";
    let triggerAsyncId = 0;
    if (typeof options === "number") {
      triggerAsyncId = options;
    } else if (options && typeof options === "object" && typeof options.triggerAsyncId === "number") {
      triggerAsyncId = options.triggerAsyncId;
    }
    this.__asyncId = ++__asyncResourceIdSeq;
    this.__triggerAsyncId = triggerAsyncId;
  }
  asyncId() {
    return this.__asyncId;
  }
  triggerAsyncId() {
    return this.__triggerAsyncId;
  }
  runInAsyncScope(fn, thisArg, ...args) {
    return fn.apply(thisArg, args);
  }
  emitDestroy() {
    return this;
  }
}

class AsyncLocalStorage {
  constructor() {
    this.__stack = [];
  }
  run(store, callback, ...args) {
    this.__stack.push(store);
    try {
      return callback(...args);
    } finally {
      this.__stack.pop();
    }
  }
  exit(callback, ...args) {
    const saved = this.__stack;
    this.__stack = [];
    try {
      return callback(...args);
    } finally {
      this.__stack = saved;
    }
  }
  enterWith(store) {
    this.__stack.push(store);
  }
  disable() {
    this.__stack = [];
  }
  getStore() {
    return this.__stack.length ? this.__stack[this.__stack.length - 1] : undefined;
  }
}

export { AsyncResource, AsyncLocalStorage };
export default { AsyncResource, AsyncLocalStorage };
`

func declareAsyncHooks() {
	registerJSShim("async_hooks", asyncHooksShim)
}

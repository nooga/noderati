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
// `AsyncLocalStorage` is deliberately NOT exported. It has exactly one real
// consumer anywhere in this dependency tree - @aws/lambda-invoke-store's
// `InvokeStoreMulti.create()` (used by @aws-sdk/core, on Bedrock's own
// request-middleware path) - and that path is only reached when
// `AWS_LAMBDA_MAX_CONCURRENCY` is set in the environment or a caller
// explicitly forces multi-instance mode (checked directly in the real,
// vendored source before deciding this, not assumed): neither is true for
// pi running as a CLI tool, which always takes the sibling
// `InvokeStoreSingle` path instead (no async_hooks at all). A real
// AsyncLocalStorage needs the store to survive across an `await` inside
// `run()`'s callback - Node does this by hooking every promise
// continuation at the engine level, saving/restoring the active context
// around each one. A host-level JS shim can't reach that: a naive
// push-onto-a-stack-then-pop-in-finally implementation pops the instant
// `run()`'s async callback returns its (still-pending) promise, not when
// the callback actually finishes running - so `getStore()` would silently
// return the wrong thing after the first `await` inside it, for exactly
// the shape `InvokeStoreMulti.run(context, fn)` is actually called with.
// Shipping that behind the real `AsyncLocalStorage` name would be a lying
// no-op wearing the right API shape, not an honest gap - and since nothing
// reachable here even calls into it, there's no reason to build (and get
// wrong) something with no real caller. Filed the actual missing
// primitive (a way to propagate a context value across promise
// continuations) as a genuine paserati feature request rather than
// guessing at a shim for it - see docs/real-node-plan.md's round 72 entry.
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

export { AsyncResource };
export default { AsyncResource };
`

func declareAsyncHooks() {
	registerJSShim("async_hooks", asyncHooksShim)
}

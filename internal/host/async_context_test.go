package host

import "testing"

// Expected values below were produced by real Node v26.

func TestAsyncLocalStoragePropagatesLikeNode(t *testing.T) {
	got := runScriptString(t, `
		import { AsyncLocalStorage } from "node:async_hooks";
		import EE from "node:events";
		const als = new AsyncLocalStorage();
		const log = [];
		await new Promise((resolve) => {
			let pending = 9;
			const done = (s) => { log.push(s); if (--pending === 0) resolve(); };
			als.run("A", () => {
				setTimeout(() => done("timeout:" + als.getStore()), 1);
				setImmediate(() => done("immediate:" + als.getStore()));
				process.nextTick(() => done("tick:" + als.getStore()));
				queueMicrotask(() => done("micro:" + als.getStore()));
				Promise.resolve().then(() => done("then:" + als.getStore()));
				(async () => { await null; await new Promise((r) => setTimeout(r, 2)); done("await:" + als.getStore()); })();
				const e = new EE();
				e.on("x", () => done("emit:" + als.getStore()));
				setTimeout(() => e.emit("x"), 3);
				const iv = setInterval(() => { done("interval:" + als.getStore()); clearInterval(iv); }, 1);
			});
			als.run("B", () => setTimeout(() => done("timeoutB:" + als.getStore()), 1));
		});
		log.sort();
		const out = [log.join(",")];
		out.push(String(als.getStore()));
		als.enterWith("E");
		out.push(als.getStore());
		out.push(String(als.exit(() => als.getStore())), als.getStore());
		JSON.stringify(out)
	`)
	want := `["await:A,emit:A,immediate:A,interval:A,micro:A,then:A,tick:A,timeout:A,timeoutB:B","undefined","E","undefined","E"]`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestAsyncHooksSurfaceMatchesNode(t *testing.T) {
	got := runScriptString(t, `
		import ah, { AsyncLocalStorage, AsyncResource, executionAsyncId, triggerAsyncId } from "node:async_hooks";
		const als = new AsyncLocalStorage();
		const out = [Object.keys(ah), executionAsyncId(), triggerAsyncId(), typeof ah.executionAsyncResource()];
		out.push(Object.keys(ah.asyncWrapProviders).length, ah.asyncWrapProviders.ZLIB, Object.isFrozen(ah.asyncWrapProviders));
		const r = als.run("R", () => new AsyncResource("X"));
		const bound = als.run("BOUND", () => AsyncResource.bind(() => als.getStore()));
		const snap = als.run("SNAP", () => AsyncLocalStorage.snapshot());
		const alsBound = als.run("ALSBIND", () => AsyncLocalStorage.bind(() => als.getStore()));
		out.push(r.runInAsyncScope(() => als.getStore()), bound(), snap(() => als.getStore()), alsBound());
		out.push(r.bind(function () { return als.getStore() + this; }, "T")());
		out.push(typeof r.asyncId(), r.asyncId() > 1, r.triggerAsyncId());
		const named = new AsyncLocalStorage({ name: "nm", defaultValue: "dv" });
		out.push(named.name, named.getStore());
		als.enterWith("E");
		{ using _ = als.withScope("W"); out.push(als.getStore()); }
		out.push(als.getStore());
		const hook = ah.createHook({ init() {} });
		out.push(hook.enable() === hook, hook.disable() === hook);
		let threw = "";
		try { new AsyncResource(); } catch (e) { threw = e.code; }
		out.push(threw);
		const ids = await new Promise((res) => setTimeout(() => res([executionAsyncId() > 1, triggerAsyncId() >= 1]), 1));
		out.push(ids);
		JSON.stringify(out)
	`)
	want := `[["AsyncLocalStorage","createHook","executionAsyncId","triggerAsyncId","executionAsyncResource","asyncWrapProviders","AsyncResource"],` +
		`1,0,"object",68,52,true,"R","BOUND","SNAP","ALSBIND","RT","number",true,1,"nm","dv","W","E",true,true,"ERR_INVALID_ARG_TYPE",[true,true]]`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestAsyncHooksCreateHookFires(t *testing.T) {
	got := runScriptString(t, `
		import { createHook } from "node:async_hooks";
		const types = new Set();
		let before = 0, after = 0;
		const hook = createHook({
			init(id, type) { types.add(type); },
			before() { before++; },
			after() { after++; },
		}).enable();
		await new Promise((resolve) => {
			setTimeout(() => setImmediate(() => process.nextTick(resolve)), 1);
		});
		hook.disable();
		JSON.stringify([types.has("Timeout"), types.has("Immediate"), types.has("TickObject"), before > 0 && before === after])
	`)
	if got != `[true,true,true,true]` {
		t.Errorf("got %s", got)
	}
}

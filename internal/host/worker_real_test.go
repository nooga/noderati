package host

import "testing"

// Expected values below were produced by real Node v26.

func TestWorkerThreadsRealWorkerMatchesNode(t *testing.T) {
	got := runScriptString(t, `import { Worker, isMainThread, threadId, SHARE_ENV, threadName, isInternalThread, setEnvironmentData } from "node:worker_threads";
const out = [isMainThread, threadId, typeof SHARE_ENV, String(SHARE_ENV), threadName, isInternalThread];
setEnvironmentData("k", [1, 2]);
const w = new Worker(` + "`" + `
  const wt = require("worker_threads");
  wt.parentPort.postMessage([wt.isMainThread, wt.threadId > 0, wt.workerData, wt.getEnvironmentData("k"), wt.threadName, process.env.ZZ, process.argv.slice(2)]);
  wt.parentPort.on("message", (m) => {
    if (m === "throw") throw new TypeError("boom");
    wt.parentPort.postMessage({ echo: m, map: new Map([[1, 2]]), date: new Date(0), u8: Buffer.from("hi") });
  });
` + "`" + `, { eval: true, workerData: { a: [1, { b: 2 }] }, env: { ZZ: "zz" }, argv: ["x", 1], name: "nm" });
const events = [];
const done = new Promise((resolve) => {
  w.on("online", () => events.push("online"));
  w.on("message", (m) => {
    if (Array.isArray(m)) {
      events.push(m);
      w.postMessage("ping");
    } else {
      events.push([m.echo, m.map instanceof Map && [...m.map], m.date instanceof Date && m.date.getTime(), m.u8.constructor.name, [...m.u8]]);
      w.postMessage("throw");
    }
  });
  w.on("error", (e) => events.push(["error", e.constructor.name, e.message]));
  w.on("exit", (c) => { events.push(["exit", c]); resolve(); });
});
let cloneErr;
try { w.postMessage(() => {}); } catch (e) { cloneErr = e.name; }
out.push(cloneErr, w.threadId > 0);
await done;
out.push(events);
const w2 = new Worker("setInterval(() => {}, 1000)", { eval: true });
await new Promise((r) => w2.on("online", r));
out.push(await w2.terminate());
const w3 = new Worker("require('worker_threads').parentPort.on('message', () => process.exit(7))", { eval: true });
w3.postMessage(1);
out.push(await new Promise((r) => w3.on("exit", r)));
JSON.stringify(out)
`)
	want := `[true,0,"symbol","Symbol(nodejs.worker_threads.SHARE_ENV)","",false,"DataCloneError",true,["online",[false,true,{"a":[1,{"b":2}]},[1,2],"nm","zz",["x","1"]],["ping",[[1,2]],0,"Uint8Array",[104,105]],["error","TypeError","boom"],["exit",1]],1,7]`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestWorkerThreadsBroadcastLocksAndThreadMessagesMatchNode(t *testing.T) {
	got := runScriptString(t, `import wt from "node:worker_threads";
const { Worker, locks, markAsUntransferable, isMarkedAsUntransferable, getEnvironmentData, setEnvironmentData } = wt;
const out = [typeof BroadcastChannel, Object.keys(wt).sort().join(" ")];
setEnvironmentData("a", 1);
setEnvironmentData("b", 2);
setEnvironmentData("b", undefined);
out.push(getEnvironmentData("a"), getEnvironmentData("b"));
const ab = new ArrayBuffer(4);
markAsUntransferable(ab);
out.push(isMarkedAsUntransferable(ab), isMarkedAsUntransferable({}));
try { new Worker("child.mjs"); } catch (e) { out.push(e.code, e.name); }
const got = [];
process.on("workerMessage", (v, src) => got.push(["workerMessage", v, src > 0]));
const bc = new BroadcastChannel("chan");
const bcDone = new Promise((r) => { bc.onmessage = (e) => { got.push(["main bc", e.data]); bc.close(); r(); }; });
const w = new Worker(` + "`" + `
  const { parentPort, threadId, postMessageToThread } = require("worker_threads");
  const bc = new BroadcastChannel("chan");
  bc.onmessage = (e) => { bc.postMessage("pong-" + e.data); bc.close(); };
  postMessageToThread(0, { from: "worker" }).then(() => parentPort.postMessage("ready"));
  setTimeout(() => Promise.reject(new RangeError("late")), 30);
` + "`" + `, { eval: true });
const exited = new Promise((r) => {
  w.on("message", (m) => { got.push(["msg", m]); bc.postMessage("ping"); });
  w.on("error", (e) => got.push(["error", e.constructor.name, e.message]));
  w.on("exit", (c) => { got.push(["exit", c]); r(); });
});
await Promise.all([bcDone, exited]);
out.push(got);
const order = [];
const p1 = locks.request("r", async (l) => { order.push("a:" + l.name + ":" + l.mode); await new Promise((r) => setTimeout(r, 5)); order.push("a-done"); });
const p2 = locks.request("r", { mode: "shared" }, async (l) => { order.push("b:" + l.mode); });
const q = await locks.query();
out.push([q.held.length, q.pending.length, q.held[0].name, q.pending[0].mode]);
await locks.request("r", { ifAvailable: true }, (l) => order.push("c:" + l));
await Promise.all([p1, p2]);
out.push(order, await locks.request("x", () => 42));
let threadErr;
await wt.postMessageToThread(999, 1).catch((e) => { threadErr = e.code; });
out.push(threadErr);
JSON.stringify(out)
`)
	want := `["function","BroadcastChannel MessageChannel MessagePort SHARE_ENV Worker getEnvironmentData isInternalThread isMainThread isMarkedAsUntransferable locks markAsUncloneable markAsUntransferable moveMessagePortToContext parentPort postMessageToThread receiveMessageOnPort resourceLimits setEnvironmentData threadId threadName workerData",1,null,true,false,"ERR_WORKER_PATH","TypeError",[["workerMessage",{"from":"worker"},true],["msg","ready"],["main bc","pong-ping"],["error","RangeError","late"],["exit",1]],[1,1,"r","shared"],["a:r:exclusive","c:null","a-done","b:shared"],42,"ERR_WORKER_MESSAGING_FAILED"]`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

package host

import "testing"

// Expected values below were produced by real Node v26.

func TestPerfHooksTimelineMatchesNode(t *testing.T) {
	got := runScriptString(t, `
		import ph from "node:perf_hooks";
		const { performance } = ph;
		const out = [];
		out.push(performance === globalThis.performance, Object.getPrototypeOf(performance) === ph.Performance.prototype);
		out.push(typeof performance.timeOrigin, performance.timeOrigin > 1e12, Object.keys(performance.toJSON()));
		const m = performance.mark("a", { detail: { x: 1 } });
		out.push(m.constructor === ph.PerformanceMark, m.name, m.entryType, m.duration, m.detail, Object.keys(m.toJSON()));
		const me = performance.measure("m", "a");
		out.push(me.entryType, me.detail);
		const me2 = performance.measure("m2", { start: 0, end: 10, detail: "d" });
		out.push(me2.startTime, me2.duration, me2.detail);
		const me3 = performance.measure("m3", { start: 5, duration: 10 });
		out.push(me3.startTime, me3.duration);
		out.push(performance.getEntries().map((e) => e.name + ":" + e.entryType));
		out.push(performance.getEntriesByType("mark").length, performance.getEntriesByName("a").length, performance.getEntriesByName("a", "measure").length);
		performance.clearMarks("a");
		out.push(performance.getEntriesByType("mark").length);
		performance.clearMeasures();
		out.push(performance.getEntries().length);
		const err = (f) => { try { f(); return "ok"; } catch (e) { return e.name + "|" + e.code + "|" + e.message; } };
		out.push(err(() => new ph.PerformanceEntry()));
		out.push(err(() => performance.measure("x", "nope")));
		out.push(err(() => performance.mark()));
		const pm = new ph.PerformanceMark("direct", { startTime: 5, detail: [1] });
		out.push(pm.startTime, pm.detail, performance.getEntries().length);
		out.push(ph.PerformanceObserver.supportedEntryTypes);
		out.push(Object.keys(performance.eventLoopUtilization()));
		const f = performance.timerify(function foo(a) { return a * 2; });
		out.push(f.name, f.length, f(21));
		out.push(String(performance), Object.prototype.toString.call(m));
		out.push(globalThis.PerformanceObserver === ph.PerformanceObserver, typeof ph.constants.NODE_PERFORMANCE_GC_MAJOR);
		JSON.stringify(out)
	`)
	want := `[true,true,"number",true,["nodeTiming","timeOrigin","eventLoopUtilization"],true,"a","mark",0,{"x":1},` +
		`["name","entryType","startTime","duration","detail"],"measure",null,0,10,"d",5,10,` +
		`["m2:measure","m3:measure","a:mark","m:measure"],1,1,0,0,0,` +
		`"TypeError|ERR_ILLEGAL_CONSTRUCTOR|Illegal constructor",` +
		`"SyntaxError|12|The \"nope\" performance mark has not been set",` +
		`"TypeError|ERR_MISSING_ARGS|The \"name\" argument must be specified",` +
		`5,[1],0,["dns","function","gc","http","http2","mark","measure","net","quic","resource"],` +
		`["idle","active","utilization"],"timerified foo",1,42,"[object Performance]","[object PerformanceMark]",true,"number"]`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestPerfHooksHistogramMatchesNode(t *testing.T) {
	got := runScriptString(t, `
		import { createHistogram, monitorEventLoopDelay } from "node:perf_hooks";
		const h = createHistogram();
		const out = [h.min, h.max, h.count, Number.isNaN(h.mean), h.percentile(50), [...h.percentiles], String(h.minBigInt)];
		for (const v of [1, 2, 3, 4, 100]) h.record(v);
		out.push(h.min, h.max, h.mean, h.stddev, h.count, h.percentile(50), h.percentile(99), [...h.percentiles], h.toJSON());
		for (const v of [5000, 123456, 99999999]) h.record(v);
		out.push(h.min, h.max, h.percentile(90), h.mean, h.stddev, [...h.percentiles]);
		const err = (f) => { try { f(); return "ok"; } catch (e) { return e.code + "|" + e.message; } };
		out.push(err(() => h.record(0)), err(() => h.percentile(0)));
		h.reset();
		out.push(h.count, h.max);
		const h2 = createHistogram(); h2.record(5); h.add(h2);
		out.push(h.count, h.min);
		const ld = monitorEventLoopDelay({ resolution: 10 });
		out.push(ld.enable(), ld.enable(), ld.disable(), ld.disable());
		JSON.stringify(out)
	`)
	want := `[9223372036854776000,0,0,true,0,[[100,0]],"9223372036854775807",1,100,22,39.01281840626232,5,3,100,` +
		`[[0,1],[50,3],[75,4],[87.5,100],[100,100]],` +
		`{"count":5,"min":1,"max":100,"mean":22,"exceeds":0,"stddev":39.01281840626232,"percentiles":{"0":1,"50":3,"75":4,"100":100,"87.5":100}},` +
		`1,100007935,123519,12512971,33057627.608786587,[[0,1],[50,4],[75,5000],[87.5,123456],[93.75,99942400],[100,99942400]],` +
		`"ERR_OUT_OF_RANGE|The value of \"val\" is out of range. It must be >= 1 && <= 9007199254740991. Received 0",` +
		`"ERR_OUT_OF_RANGE|The value of \"percentile\" is out of range. It must be > 0 && <= 100. Received 0",` +
		`0,0,1,5,true,false,true,false]`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestPerfHooksObserverDelivery(t *testing.T) {
	got := runScriptString(t, `
		import { performance, PerformanceObserver } from "node:perf_hooks";
		const log = [];
		const err = (f) => { try { f(); return "ok"; } catch (e) { return e.code + "|" + e.message; } };
		log.push(err(() => new PerformanceObserver()));
		await new Promise((resolve) => {
			const obs = new PerformanceObserver((list, o) => {
				log.push(["observer", list.getEntries().map((e) => e.name), list.getEntriesByType("mark").length, list.getEntriesByName("o1").length, o === obs]);
				obs.disconnect();
				resolve();
			});
			obs.observe({ entryTypes: ["mark", "measure"] });
			log.push(err(() => obs.observe({})));
			log.push(err(() => obs.observe({ entryTypes: ["mark"], type: "mark" })));
			performance.mark("o1");
			performance.mark("o2");
			const obs3 = new PerformanceObserver(() => {});
			obs3.observe({ type: "mark" });
			performance.mark("t1");
			log.push(["take", obs3.takeRecords().map((e) => e.name), obs3.takeRecords().length]);
			obs3.disconnect();
		});
		await new Promise((resolve) => {
			const obs2 = new PerformanceObserver((list) => { log.push(["buffered", list.getEntries().map((e) => e.name)]); obs2.disconnect(); resolve(); });
			obs2.observe({ type: "mark", buffered: true });
		});
		JSON.stringify(log)
	`)
	want := `["ERR_INVALID_ARG_TYPE|The \"callback\" argument must be of type function. Received undefined",` +
		`"ERR_MISSING_ARGS|The \"options.entryTypes\" and \"options.type\" arguments must be specified",` +
		`"ERR_INVALID_ARG_VALUE|The property 'options.entryTypes' options.entryTypes can not set with options.type together. Received [ 'mark' ]",` +
		`["take",["t1"],0],["observer",["o1","o2","t1"],3,1,true],["buffered",["o1","o2","t1"]]]`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// Expected values produced by real Node v26.
func TestPerformancePrototypeEnumerationAndConstantsMatchNode(t *testing.T) {
	got := runScriptString(t, `
		import { performance, constants } from "node:perf_hooks";
		const before = Object.keys(Object.getPrototypeOf(performance)).join(" ");
		performance.mark("m");
		const after = [];
		for (const k in performance) if (!["addEventListener", "removeEventListener", "dispatchEvent"].includes(k)) after.push(k);
		JSON.stringify([before, after.join(" "), Object.keys(constants).length, constants.NODE_PERFORMANCE_MILESTONE_LOOP_EXIT, constants.NODE_PERFORMANCE_ENTRY_TYPE_DNS])
	`)
	keys := "clearMarks clearMeasures clearResourceTimings getEntries getEntriesByName getEntriesByType mark measure now setResourceTimingBufferSize timeOrigin toJSON onresourcetimingbufferfull"
	want := `["` + keys + `","` + keys + `",25,6,4]`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

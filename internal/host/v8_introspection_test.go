package host

import "testing"

// Expected values below were produced by real Node v26.

func TestV8IntrospectionShapesMatchNode(t *testing.T) {
	got := runScriptString(t, `
		import v8 from "node:v8";
		const out = [Object.keys(v8).sort().join(" ")];
		out.push(Object.keys(v8.getHeapSpaceStatistics()[2]).join(","), v8.getHeapSpaceStatistics().length);
		out.push(Object.keys(v8.getHeapCodeStatistics()).join(","));
		out.push(Object.keys(v8.getCppHeapStatistics("brief")).join(","), v8.getCppHeapStatistics().detail_level);
		out.push(typeof v8.cachedDataVersionTag(), Object.keys(v8.promiseHooks).join(","));
		out.push(v8.isStringOneByteRepresentation("abc"), v8.isStringOneByteRepresentation("ąbc"));
		const g = new v8.GCProfiler(); g.start(); out.push(Object.keys(g.stop()).join(","));
		for (const f of [() => v8.isStringOneByteRepresentation(1), () => v8.getCppHeapStatistics("x"), () => v8.setHeapSnapshotNearHeapLimit(-1)]) {
			try { f(); } catch (e) { out.push(e.code + "|" + e.message); }
		}
		out.push(Object.keys(JSON.parse(v8.startCpuProfile().stop())).join(","));
		out.push(Object.keys(JSON.parse(v8.startHeapProfile().stop()).head).join(","));
		const snap = await new Promise((res) => { let d = ""; const s = v8.getHeapSnapshot(); s.on("data", (c) => d += c); s.on("end", () => res(d)); });
		out.push(Object.keys(JSON.parse(snap)).join(","));
		JSON.stringify(out)
	`)
	want := `["DefaultDeserializer DefaultSerializer Deserializer GCProfiler Serializer cachedDataVersionTag deserialize getCppHeapStatistics getHeapCodeStatistics getHeapSnapshot getHeapSpaceStatistics getHeapStatistics isStringOneByteRepresentation promiseHooks queryObjects serialize setFlagsFromString setHeapSnapshotNearHeapLimit startCpuProfile startHeapProfile startupSnapshot stopCoverage takeCoverage writeHeapSnapshot",` +
		`"space_name,space_size,space_used_size,space_available_size,physical_space_size",13,` +
		`"code_and_metadata_size,bytecode_and_metadata_size,external_script_source_size,cpu_profiler_metadata_size",` +
		`"committed_size_bytes,resident_size_bytes,used_size_bytes,space_statistics,type_names,detail_level","detailed",` +
		`"number","createHook,onInit,onBefore,onAfter,onSettled",true,false,"version,startTime,statistics,endTime",` +
		`"ERR_INVALID_ARG_TYPE|The \"content\" argument must be of type string. Received type number (1)",` +
		`"ERR_INVALID_ARG_VALUE|The argument 'type' must be one of: 'brief', 'detailed'. Received 'x'",` +
		`"ERR_OUT_OF_RANGE|The value of \"limit\" is out of range. It must be >= 1 && <= 4294967295. Received -1",` +
		`"nodes,startTime,endTime,samples,timeDeltas","selfSize,id,callFrame,children",` +
		`"snapshot,nodes,edges,trace_function_infos,trace_tree,samples,locations,strings"]`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

package host

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// wasmFixtureModuleJS loads the checked-in fixture wasm binary (see
// testdata/wasm_fixture.wat for the source) as a JS expression string
// building a real Uint8Array of its bytes, for embedding directly into
// RunCode scripts below. A real .wasm file, not a hand-encoded byte
// literal, per this project's own "real artifacts, not stand-ins"
// discipline.
func wasmFixtureBytes(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "wasm_fixture.wasm"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return data
}

// installWasmFixtureBuffer exposes the fixture's bytes to JS as a global
// `FIXTURE_WASM_BYTES` real Uint8Array, via the same Go-side
// ArrayBuffer/TypedArray construction webassembly_global.go itself uses
// - avoids round-tripping the binary through a JS source literal
// (base64 string in the test file) just to get it into the VM.
func installWasmFixtureBuffer(t *testing.T, p *driver.Paserati) {
	t.Helper()
	installWasmBytesAsGlobal(t, p, "FIXTURE_WASM_BYTES", wasmFixtureBytes(t))
}

// installWasmBytesAsGlobal exposes arbitrary wasm bytes to JS as a real
// Uint8Array global under the given name.
func installWasmBytesAsGlobal(t *testing.T, p *driver.Paserati, name string, data []byte) {
	t.Helper()
	vmInst := p.GetVM()
	ab := vm.NewArrayBuffer(len(data))
	copy(ab.AsArrayBuffer().GetData(), data)
	u8 := vm.NewTypedArray(vm.TypedArrayUint8, ab.AsArrayBuffer(), 0, -1)
	gt, ok := vmInst.GetGlobal("globalThis")
	if !ok {
		t.Fatal("no globalThis")
	}
	gt.AsPlainObject().SetOwn(name, u8)
}

// realLLHTTPWasmBytes loads a real, unmodified copy of undici@7.11.0's
// own vendored `lib/llhttp/llhttp-wasm.js` payload (MIT licensed, same
// license as undici itself), extracted once via `Buffer.from(base64,
// 'base64')` in a real Node process and checked in as a plain .wasm
// binary - see testdata/llhttp-real.wasm. This is the actual production
// wasm binary real undici's own `lazyllhttp()` instantiates on every
// real HTTP/1.1 request, not a synthetic stand-in.
func realLLHTTPWasmBytes(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "llhttp-real.wasm"))
	if err != nil {
		t.Fatalf("reading real llhttp wasm fixture: %v", err)
	}
	return data
}

func TestWebAssemblyGlobalsExist(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		JSON.stringify({
			hasWasm: typeof WebAssembly === "object",
			hasModule: typeof WebAssembly.Module === "function",
			hasInstance: typeof WebAssembly.Instance === "function",
			hasMemory: typeof WebAssembly.Memory === "function",
			hasCompileError: typeof WebAssembly.CompileError === "function",
			hasLinkError: typeof WebAssembly.LinkError === "function",
			hasRuntimeError: typeof WebAssembly.RuntimeError === "function",
		})
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"hasWasm":true,"hasModule":true,"hasInstance":true,"hasMemory":true,"hasCompileError":true,"hasLinkError":true,"hasRuntimeError":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestWebAssemblyCompileErrorIsCatchable is exactly the shape real
// undici's lazyllhttp() relies on: `try { new WebAssembly.Module(bad) }
// catch {}` must actually catch, not crash the process, so its non-SIMD
// fallback module load can run next.
func TestWebAssemblyCompileErrorIsCatchable(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		let caught = null;
		try {
			new WebAssembly.Module(new Uint8Array([1, 2, 3, 4]));
		} catch (e) {
			caught = e;
		}
		JSON.stringify({
			caught: caught !== null,
			isCompileError: caught instanceof WebAssembly.CompileError,
			isError: caught instanceof Error,
		})
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"caught":true,"isCompileError":true,"isError":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestWebAssemblyAsyncCompileAndInstantiate exercises the async
// `WebAssembly.compile`/`instantiate` statics real undici@7.11.0's own
// current lazyllhttp() actually uses (`await WebAssembly.compile(...)`
// then `await WebAssembly.instantiate(mod, {...})`) - found necessary via
// a real end-to-end probe against a real installed copy, not assumed
// from paserati#375's own issue text (which quoted an undici version
// using the synchronous constructors directly). Covers both
// `instantiate(module, imports)` (resolves to just the Instance) and
// `instantiate(bytesSource, imports)` (resolves to {module, instance}).
func TestWebAssemblyAsyncCompileAndInstantiate(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	installWasmFixtureBuffer(t, p)
	val, errs := p.RunCode(`
		const mod = await WebAssembly.compile(FIXTURE_WASM_BYTES);
		const inst = await WebAssembly.instantiate(mod, { env: { host_add: (a, b) => a + b } });
		const r1 = inst.exports.add_via_host(2, 3);

		const { module, instance } = await WebAssembly.instantiate(FIXTURE_WASM_BYTES, { env: { host_add: (a, b) => a * b } });
		const r2 = instance.exports.add_via_host(2, 3);

		JSON.stringify({
			r1, r2,
			moduleIsModule: module instanceof WebAssembly.Module,
			instanceIsInstance: instance instanceof WebAssembly.Instance,
		})
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"r1":5,"r2":6,"moduleIsModule":true,"instanceIsInstance":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestWebAssemblyTwoInstancesDontCollide is the test to write before any
// bridge code, per review: the same compiled Module instantiated twice,
// with two *different* import objects, must route each Instance's calls
// to its own JS import function - not the other's, and not whichever was
// registered first on some shared runtime.
func TestWebAssemblyTwoInstancesDontCollide(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	installWasmFixtureBuffer(t, p)
	val, errs := p.RunCode(`
		const mod = new WebAssembly.Module(FIXTURE_WASM_BYTES);

		const calls1 = [];
		const inst1 = new WebAssembly.Instance(mod, {
			env: { host_add: (a, b) => { calls1.push([a, b]); return a + b + 100; } }
		});

		const calls2 = [];
		const inst2 = new WebAssembly.Instance(mod, {
			env: { host_add: (a, b) => { calls2.push([a, b]); return a + b + 200; } }
		});

		const r1 = inst1.exports.add_via_host(1, 2);
		const r2 = inst2.exports.add_via_host(3, 4);

		JSON.stringify({
			r1, r2,
			calls1Len: calls1.length, calls2Len: calls2.length,
			calls1: calls1[0], calls2: calls2[0],
		})
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"r1":103,"r2":207,"calls1Len":1,"calls2Len":1,"calls1":[1,2],"calls2":[3,4]}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestWebAssemblyHostImportReentrancy exercises the deepest real path:
// JS calls a native exported function -> wazero -> a Go host trampoline
// -> vm.Call back into a real JS import function -> back through wazero
// -> back to the JS caller. One level deeper than any existing
// JS-callback bridge in this codebase (emitter.go's is a single hop).
func TestWebAssemblyHostImportReentrancy(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	installWasmFixtureBuffer(t, p)
	val, errs := p.RunCode(`
		const mod = new WebAssembly.Module(FIXTURE_WASM_BYTES);
		let sawInJS = null;
		const inst = new WebAssembly.Instance(mod, {
			env: { host_add: (a, b) => {
				sawInJS = a + b;
				return sawInJS * 2;
			} }
		});
		const result = inst.exports.add_via_host(5, 6);
		JSON.stringify({ result, sawInJS })
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"result":22,"sawInJS":11}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestWebAssemblyMemoryReadWrite exercises the exported-memory bridge:
// a byte written via JS's `.buffer` must be visible to wasm code after
// crossing into an exported call, and a byte written by wasm must be
// visible back on the JS side afterward.
func TestWebAssemblyMemoryReadWrite(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	installWasmFixtureBuffer(t, p)
	val, errs := p.RunCode(`
		const mod = new WebAssembly.Module(FIXTURE_WASM_BYTES);
		const inst = new WebAssembly.Instance(mod, { env: { host_add: (a, b) => a + b } });
		const mem = inst.exports.memory;

		// JS -> wasm: write via JS's ArrayBuffer view, read back via a
		// real exported wasm function.
		new Uint8Array(mem.buffer)[200] = 42;
		const readBack = inst.exports.read_byte(200);

		// wasm -> JS: write via a real exported wasm function, read back
		// via JS's ArrayBuffer view (fresh access, per real undici's own
		// call pattern of never caching .buffer across a call).
		inst.exports.write_byte(300, 77);
		const viaJS = new Uint8Array(mem.buffer)[300];

		// mem.buffer instanceof ArrayBuffer matters for real: webidl
		// converter code in real vendored packages checks exactly this.
		// Was paserati#377 (fixed upstream, pulled as of paserati@c6a66eda)
		// before this could be asserted meaningfully.
		JSON.stringify({ readBack, viaJS, isArrayBuffer: mem.buffer instanceof ArrayBuffer })
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"readBack":42,"viaJS":77,"isArrayBuffer":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestWebAssemblyMemoryOffsetView exercises the exact shape real
// undici's Parser.execute uses: `new Uint8Array(memory.buffer, ptr,
// len).set(data)` - a *view* into the buffer at a nonzero offset, not
// the whole-buffer form TestWebAssemblyMemoryReadWrite uses. This is
// what actually proves syncIn pushes a JS-side write that landed inside
// a view, not just one written to buffer[0].
func TestWebAssemblyMemoryOffsetView(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	installWasmFixtureBuffer(t, p)
	val, errs := p.RunCode(`
		const mod = new WebAssembly.Module(FIXTURE_WASM_BYTES);
		const inst = new WebAssembly.Instance(mod, { env: { host_add: (a, b) => a + b } });
		const mem = inst.exports.memory;

		const ptr = 1000;
		const data = new Uint8Array([10, 20, 30, 40]);
		new Uint8Array(mem.buffer, ptr, data.length).set(data);

		const readBack = [0, 1, 2, 3].map(i => inst.exports.read_byte(ptr + i));
		JSON.stringify({ readBack })
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"readBack":[10,20,30,40]}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestWebAssemblyImportThrowPropagates guards the fix that replaced
// silently-zeroed results on a thrown JS import callback: real undici's
// wasm_on_* callbacks do throw in real use (e.g. maxHeaderSize
// exceeded), and that exception must come out of the exported call that
// triggered it as the *same* real JS exception, not be swallowed.
func TestWebAssemblyImportThrowPropagates(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	installWasmFixtureBuffer(t, p)
	val, errs := p.RunCode(`
		const mod = new WebAssembly.Module(FIXTURE_WASM_BYTES);
		const inst = new WebAssembly.Instance(mod, {
			env: { host_add: () => { throw new RangeError("host_add refuses"); } }
		});
		let caught = null;
		try {
			inst.exports.add_via_host(1, 2);
		} catch (e) {
			caught = e;
		}
		JSON.stringify({
			isRangeError: caught instanceof RangeError,
			message: caught && caught.message,
		})
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"isRangeError":true,"message":"host_add refuses"}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestWebAssemblyMemoryGrow checks growth: the byte length must reflect
// the new size, previously-written bytes must survive the grow, and
// `.grow()` must return the *previous* page count per spec.
func TestWebAssemblyMemoryGrow(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	installWasmFixtureBuffer(t, p)
	val, errs := p.RunCode(`
		const mod = new WebAssembly.Module(FIXTURE_WASM_BYTES);
		const inst = new WebAssembly.Instance(mod, { env: { host_add: (a, b) => a + b } });
		const mem = inst.exports.memory;

		inst.exports.write_byte(50, 9);
		const beforeLen = mem.buffer.byteLength;
		const prevPages = mem.grow(2);
		const afterLen = mem.buffer.byteLength;
		const survived = inst.exports.read_byte(50);

		JSON.stringify({ beforeLen, prevPages, afterLen, survived, grew: afterLen > beforeLen })
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"beforeLen":65536,"prevPages":1,"afterLen":196608,"survived":9,"grew":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestWebAssemblyBufferSourceUnsupported checks the honest-refusal path:
// something that isn't a BufferSource must throw a real, catchable
// CompileError, not panic.
func TestWebAssemblyModuleRejectsNonBufferSource(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		let caught = null;
		try {
			new WebAssembly.Module("not a buffer");
		} catch (e) {
			caught = e;
		}
		JSON.stringify({ isCompileError: caught instanceof WebAssembly.CompileError })
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"isCompileError":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestWebAssemblyInstanceRejectsNonModuleArgument guards a real bug
// found while adding the async compile/instantiate statics: passing a
// non-object (or a plain object that isn't a real WebAssembly.Module)
// as the first argument to `new WebAssembly.Instance(...)` or
// `WebAssembly.instantiate(bufferSource, ...)`'s internal module-type
// check used to panic the whole VM (`value.AsPlainObject()` panics on
// any non-TypeObject value - confirmed directly in pkg/vm/value.go -
// and this file called it unconditionally on a raw user argument)
// instead of throwing a catchable TypeError.
func TestWebAssemblyInstanceRejectsNonModuleArgument(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		let caught = null;
		try {
			new WebAssembly.Instance(new Uint8Array([1, 2, 3]), {});
		} catch (e) {
			caught = e;
		}
		JSON.stringify({ caught: caught !== null, isTypeError: caught instanceof TypeError })
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"caught":true,"isTypeError":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestWebAssemblyRealLLHTTPParsesRealHTTPResponse is the actual
// end-to-end proof this whole bridge exists for: compile and instantiate
// real undici's own vendored llhttp wasm binary (not a synthetic
// fixture), feed it a real, complete HTTP/1.1 response byte-for-byte the
// way real undici's own `Parser.execute()` does (`new
// Uint8Array(memory.buffer, ptr, len).set(chunk)` then
// `llhttp_execute(ptr, ptr, len)`), and confirm every one of llhttp's
// real wasm_on_* callbacks fires, in the right order, with the right
// argument values, and llhttp_execute returns 0 (HPE_OK).
//
// Verified directly (not assumed) against a real, unmodified, currently
// installed undici@7.11.0 before writing this: a standalone script using
// exactly this call pattern against the exact same wasm binary produced
// this exact same callback sequence. Confirms the actual hard technical
// risk this whole feature existed to resolve - the real WASM<->JS memory
// bridge and host-function trampolines - works against production wasm,
// independent of whatever separate, unrelated issue exists further up
// undici's own Client/socket dispatch layer (not attempted here - see
// docs/real-node-plan.md's Round 76 entry).
func TestWebAssemblyRealLLHTTPParsesRealHTTPResponse(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	installWasmBytesAsGlobal(t, p, "LLHTTP_WASM_BYTES", realLLHTTPWasmBytes(t))

	val, errs := p.RunCode(`
		const TYPE_RESPONSE = 2;
		const log = [];
		const importObject = {
			env: {
				wasm_on_url: (p, at, len) => { log.push(["on_url", at, len]); return 0; },
				wasm_on_status: (p, at, len) => { log.push(["on_status", at, len]); return 0; },
				wasm_on_message_begin: (p) => { log.push(["on_message_begin"]); return 0; },
				wasm_on_header_field: (p, at, len) => { log.push(["on_header_field", at, len]); return 0; },
				wasm_on_header_value: (p, at, len) => { log.push(["on_header_value", at, len]); return 0; },
				wasm_on_headers_complete: (p, statusCode, upgrade, shouldKeepAlive) => {
					log.push(["on_headers_complete", statusCode, upgrade, shouldKeepAlive]);
					return 0;
				},
				wasm_on_body: (p, at, len) => { log.push(["on_body", at, len]); return 0; },
				wasm_on_message_complete: (p) => { log.push(["on_message_complete"]); return 0; },
			}
		};

		const mod = await WebAssembly.compile(LLHTTP_WASM_BYTES);
		const instance = await WebAssembly.instantiate(mod, importObject);
		const llhttp = instance.exports;

		const ptr = llhttp.llhttp_alloc(TYPE_RESPONSE);

		const response = "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: 5\r\n\r\nhello";
		const chunk = new TextEncoder().encode(response);

		const bufSize = Math.ceil(chunk.length / 4096) * 4096;
		const bufPtr = llhttp.malloc(bufSize);
		new Uint8Array(llhttp.memory.buffer, bufPtr, bufSize).set(chunk);

		const ret = llhttp.llhttp_execute(ptr, bufPtr, chunk.length);
		llhttp.llhttp_free(ptr);

		JSON.stringify({
			ret,
			eventNames: log.map(e => e[0]),
			headersComplete: log.find(e => e[0] === "on_headers_complete"),
			bodyLength: log.find(e => e[0] === "on_body")[2],
		})
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"ret":0,"eventNames":["on_message_begin","on_status","on_header_field","on_header_value","on_header_field","on_header_value","on_headers_complete","on_body","on_message_complete"],"headersComplete":["on_headers_complete",200,0,1],"bodyLength":5}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

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
	data := wasmFixtureBytes(t)
	vmInst := p.GetVM()
	ab := vm.NewArrayBuffer(len(data))
	copy(ab.AsArrayBuffer().GetData(), data)
	u8 := vm.NewTypedArray(vm.TypedArrayUint8, ab.AsArrayBuffer(), 0, -1)
	gt, ok := vmInst.GetGlobal("globalThis")
	if !ok {
		t.Fatal("no globalThis")
	}
	gt.AsPlainObject().SetOwn("FIXTURE_WASM_BYTES", u8)
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

		JSON.stringify({ readBack, viaJS, isArrayBuffer: mem.buffer.constructor.name })
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"readBack":42,"viaJS":77,"isArrayBuffer":"ArrayBuffer"}`
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

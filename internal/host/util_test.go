package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

// TestUtilTypesPredicates drives the exact confirmed-reachable subset
// grepped out of real undici's own lib/mock/mock-utils.js et al. before
// deciding which predicates to build (see util.go's installUtilNatives
// doc comment) - not the full ~30-name real Node surface.
func TestUtilTypesPredicates(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import util from "node:util";
		const { types } = util;
		JSON.stringify({
			isPromise_yes: types.isPromise(Promise.resolve(1)),
			isPromise_no: types.isPromise({}),
			isArrayBuffer_yes: types.isArrayBuffer(new ArrayBuffer(4)),
			isArrayBuffer_no: types.isArrayBuffer(new Uint8Array(4)),
			isTypedArray_yes: types.isTypedArray(new Uint8Array(4)),
			isDataView_yes: types.isDataView(new DataView(new ArrayBuffer(4))),
			isArrayBufferView_typed: types.isArrayBufferView(new Uint8Array(4)),
			isArrayBufferView_dataview: types.isArrayBufferView(new DataView(new ArrayBuffer(4))),
			isArrayBufferView_no: types.isArrayBufferView(new ArrayBuffer(4)),
		})
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"isPromise_yes":true,"isPromise_no":false,"isArrayBuffer_yes":true,"isArrayBuffer_no":false,"isTypedArray_yes":true,"isDataView_yes":true,"isArrayBufferView_typed":true,"isArrayBufferView_dataview":true,"isArrayBufferView_no":false}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestUtilTextEncoderDecoderAlias confirms real Node's long-standing
// legacy alias - `require('util').TextEncoder === TextEncoder` is `true`
// in real Node - holds for both the `require('util')` CJS shape and the
// `import util from "node:util"` ESM shape. Found the hard way probing
// real @silvia-odwyer/photon-node: its wasm-bindgen glue does
// `const { TextEncoder, TextDecoder } = require('util')` at module top
// level, so a missing pair here threw "undefined is not a constructor"
// before the module's own WASM instantiation ever ran.
func TestUtilTextEncoderDecoderAlias(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import util from "node:util";
		JSON.stringify({
			sameAsGlobal_encoder: util.TextEncoder === globalThis.TextEncoder,
			sameAsGlobal_decoder: util.TextDecoder === globalThis.TextDecoder,
			encoderWorks: new util.TextEncoder().encode("hi").length === 2,
			decoderWorks: new util.TextDecoder().decode(new util.TextEncoder().encode("hi")) === "hi",
		})
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"sameAsGlobal_encoder":true,"sameAsGlobal_decoder":true,"encoderWorks":true,"decoderWorks":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestUtilTextEncoderDecoderAliasCJS is the exact real call shape that
// broke @silvia-odwyer/photon-node's wasm-bindgen glue:
// `const { TextEncoder, TextDecoder } = require('util')` at CJS module
// top level.
func TestUtilTextEncoderDecoderAliasCJS(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := RunCJS(p, `
		const { TextEncoder, TextDecoder } = require('util');
		module.exports = JSON.stringify({
			sameAsGlobal_encoder: TextEncoder === globalThis.TextEncoder,
			sameAsGlobal_decoder: TextDecoder === globalThis.TextDecoder,
			decoderWorks: new TextDecoder().decode(new TextEncoder().encode("hi")) === "hi",
		});
	`, "/virtual/test.js")
	if len(errs) > 0 {
		t.Fatalf("RunCJS: %v", errs[0])
	}
	want := `{"sameAsGlobal_encoder":true,"sameAsGlobal_decoder":true,"decoderWorks":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestUtilTypesSubpathModule drives the exact real call shape from
// undici's lib/web/websocket/websocket.js and lib/web/fetch/util.js:
// `require('node:util/types')` as its own distinct module, not
// util.go's `.types` property, destructuring isArrayBuffer/isUint8Array
// directly from it.
func TestUtilTypesSubpathModule(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { isArrayBuffer, isUint8Array } from "node:util/types";
		JSON.stringify({
			isArrayBuffer_yes: isArrayBuffer(new ArrayBuffer(4)),
			isUint8Array_yes: isUint8Array(new Uint8Array(4)),
			isUint8Array_no_int16: isUint8Array(new Int16Array(4)),
			isUint8Array_no_plain: isUint8Array({}),
		})
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"isArrayBuffer_yes":true,"isUint8Array_yes":true,"isUint8Array_no_int16":false,"isUint8Array_no_plain":false}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

func TestUtilTypesSubpathCJSRequire(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := RunCJS(p, `
		const { isArrayBuffer } = require('node:util/types');
		module.exports = isArrayBuffer(new ArrayBuffer(4));
	`, "/virtual/test.js")
	if len(errs) > 0 {
		t.Fatalf("RunCJS: %v", errs[0])
	}
	if !val.IsTruthy() {
		t.Error("expected require('node:util/types').isArrayBuffer to work and return true")
	}
}

// TestUtilPromisifyResolvesAndRejects mirrors real undici's own actual
// call shape (lib/mock/mock-client.js's `close()`:
// `await promisify(this[kOriginalClose])()`) - a Node-style
// (...args, callback(err, result)) function wrapped into a real Promise.
func TestUtilPromisifyResolvesAndRejects(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { promisify } from "node:util";

		function readStyle(value, cb) {
			setTimeout(() => cb(null, value * 2), 1);
		}
		function failStyle(cb) {
			setTimeout(() => cb(new Error("nope")), 1);
		}

		const doubled = await promisify(readStyle)(21);
		let caught = null;
		try {
			await promisify(failStyle)();
		} catch (e) {
			caught = e.message;
		}
		JSON.stringify({ doubled, caught })
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"doubled":42,"caught":"nope"}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

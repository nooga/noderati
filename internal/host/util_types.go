package host

// util_types.go implements node:util/types as its own real, distinct
// module - not just an alias for util.go's `.types` property. Real
// undici destructures directly from it in three places
// (lib/web/websocket/websocket.js's `isArrayBuffer`, lib/web/fetch/
// util.js and body.js's `isUint8Array`), confirmed by grepping every
// real `node:util/types` call site before writing this. Reuses the
// exact same predicate functions util.go's installUtilNatives already
// built (via globalThis.__noderatiUtilTypes) rather than duplicating
// the ValueType-tag logic a second time.
const utilTypesShim = `const t = globalThis.__noderatiUtilTypes;

export const isPromise = t.isPromise;
export const isProxy = t.isProxy;
export const isArrayBuffer = t.isArrayBuffer;
export const isSharedArrayBuffer = t.isSharedArrayBuffer;
export const isAnyArrayBuffer = t.isAnyArrayBuffer;
export const isDataView = t.isDataView;
export const isTypedArray = t.isTypedArray;
export const isArrayBufferView = t.isArrayBufferView;
export const isUint8Array = t.isUint8Array;

export default t;
`

func declareUtilTypes() {
	registerJSShim("util/types", utilTypesShim)
}

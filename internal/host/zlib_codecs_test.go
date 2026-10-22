package host

import "testing"

// Expected values below were produced by real Node v26.

func TestZlibBrotliZstdDecodeNodePayloads(t *testing.T) {
	got := runScriptString(t, `
		import zlib from "node:zlib";
		const want = "hello brotli and zstd ".repeat(20);
		const br = Buffer.from("G7cB+MVtbF33NTxBFJSrGoTBKuEcnXLFjivBFv4sAA==", "base64");
		const zs = Buffer.from("KLUv/WS4AO0AALBoZWxsbyBicm90bGkgYW5kIHpzdGQgAQA+U51NmFlrPw==", "base64");
		const gz = Buffer.from("H4sIAAAAAAAAE8tIzcnJV0gqyi/JyVRIzEtRqCouSVHIGBUd5KIANO9sLLgBAAA=", "base64");
		JSON.stringify([
			zlib.brotliDecompressSync(br).toString() === want,
			zlib.zstdDecompressSync(zs).toString() === want,
			zlib.gunzipSync(gz).toString() === want,
			zlib.unzipSync(gz).toString() === want,
		])
	`)
	if got != "[true,true,true,true]" {
		t.Errorf("got %s", got)
	}
}

func TestZlibBrotliZstdRoundTripWithParams(t *testing.T) {
	got := runScriptString(t, `
		import zlib from "node:zlib";
		const C = zlib.constants;
		const text = "round trip ".repeat(3000);
		const out = [];
		for (const q of [0, 4, 11]) {
			const c = zlib.brotliCompressSync(text, { params: { [C.BROTLI_PARAM_QUALITY]: q, [C.BROTLI_PARAM_LGWIN]: 18, [C.BROTLI_PARAM_SIZE_HINT]: text.length, [C.BROTLI_PARAM_MODE]: C.BROTLI_MODE_TEXT } });
			out.push(zlib.brotliDecompressSync(c).toString() === text && c.length < text.length);
		}
		for (const l of [1, 3, 19]) {
			const c = zlib.zstdCompressSync(text, { params: { [C.ZSTD_c_compressionLevel]: l, [C.ZSTD_c_checksumFlag]: 1 } });
			out.push(zlib.zstdDecompressSync(c).toString() === text && c.length < text.length);
		}
		out.push(zlib.brotliDecompressSync(zlib.brotliCompressSync("")).length === 0);
		out.push(zlib.zstdDecompressSync(zlib.zstdCompressSync("")).length === 0);
		JSON.stringify(out)
	`)
	if got != "[true,true,true,true,true,true,true,true]" {
		t.Errorf("got %s", got)
	}
}

func TestZlibBrotliZstdStreams(t *testing.T) {
	got := runScriptString(t, `
		import zlib from "node:zlib";
		const text = "streamed payload ".repeat(2000);
		function through(c, d) {
			return new Promise((resolve, reject) => {
				const chunks = [];
				d.on("data", (x) => chunks.push(x));
				d.on("end", () => resolve(Buffer.concat(chunks).toString()));
				d.on("error", reject);
				c.on("data", (x) => d.write(x));
				c.on("end", () => d.end());
				c.on("error", reject);
				c.write(text.slice(0, 1000));
				c.write(text.slice(1000));
				c.end();
			});
		}
		const r = [];
		r.push((await through(zlib.createBrotliCompress(), zlib.createBrotliDecompress())) === text);
		r.push((await through(zlib.createZstdCompress(), zlib.createZstdDecompress())) === text);
		r.push((await through(zlib.createGzip(), zlib.createGunzip())) === text);
		r.push(zlib.createBrotliCompress() instanceof zlib.BrotliCompress);
		r.push(zlib.createZstdDecompress() instanceof zlib.ZstdDecompress);
		r.push(zlib.createGunzip() instanceof zlib.Gunzip);
		const b = await new Promise((res, rej) => zlib.brotliCompress(text, (e, v) => e ? rej(e) : res(v)));
		r.push(await new Promise((res, rej) => zlib.brotliDecompress(b, (e, v) => e ? rej(e) : res(v.toString() === text))));
		const z = await new Promise((res, rej) => zlib.zstdCompress(text, (e, v) => e ? rej(e) : res(v)));
		r.push(await new Promise((res, rej) => zlib.zstdDecompress(z, (e, v) => e ? rej(e) : res(v.toString() === text))));
		JSON.stringify(r)
	`)
	if got != "[true,true,true,true,true,true,true,true]" {
		t.Errorf("got %s", got)
	}
}

func TestZlibCodecErrorsMatchNode(t *testing.T) {
	got := runScriptString(t, `
		import zlib from "node:zlib";
		const C = zlib.constants;
		const show = (f) => { try { f(); return "ok"; } catch (e) { return [e.name, e.code, e.errno, e.message].join("|"); } };
		const withDict = zlib.deflateSync("hello world hello", { dictionary: Buffer.from("hello world dictionary") });
		JSON.stringify([
			show(() => zlib.gunzipSync(Buffer.from("nope"))),
			show(() => zlib.inflateSync(Buffer.from("nope"))),
			show(() => zlib.gunzipSync(zlib.gzipSync("hello").subarray(0, 10))),
			show(() => zlib.brotliDecompressSync(Buffer.from("nope nope"))),
			show(() => zlib.brotliDecompressSync(Buffer.alloc(0))),
			show(() => zlib.zstdDecompressSync(Buffer.from("nope nope"))),
			show(() => { const b = zlib.zstdCompressSync("abcabcabc", { params: { [C.ZSTD_c_checksumFlag]: 1 } }); b[b.length - 1] ^= 0xff; zlib.zstdDecompressSync(b); }),
			show(() => { const b = zlib.gzipSync("abc"); b[b.length - 6] ^= 0xff; zlib.gunzipSync(b); }),
			show(() => { const b = zlib.gzipSync("abc"); b[b.length - 2] ^= 0xff; zlib.gunzipSync(b); }),
			show(() => zlib.inflateSync(withDict)),
			show(() => zlib.inflateSync(withDict, { dictionary: Buffer.from("nope") })),
			zlib.inflateSync(withDict, { dictionary: Buffer.from("hello world dictionary") }).toString(),
			show(() => zlib.gunzipSync(zlib.gzipSync("x".repeat(1000)), { maxOutputLength: 10 })),
			show(() => zlib.brotliCompressSync("hello", { params: { 999: 4 } })),
			show(() => zlib.zstdCompressSync("hello", { params: { 9999: 1 } })),
			show(() => zlib.gzipSync("hello", { level: 10 })),
			show(() => zlib.gzipSync("hello", { chunkSize: 3 })),
			show(() => zlib.gzipSync(5)),
			show(() => zlib.crc32(1)),
		])
	`)
	want := `["Error|Z_DATA_ERROR|-3|incorrect header check",` +
		`"Error|Z_DATA_ERROR|-3|incorrect header check",` +
		`"Error|Z_BUF_ERROR|-5|unexpected end of file",` +
		`"Error|ERR__ERROR_FORMAT_PADDING_2|-15|Decompression failed",` +
		`"Error|Z_BUF_ERROR|-5|unexpected end of file",` +
		`"Error|ZSTD_error_prefix_unknown|10|Unknown frame descriptor",` +
		`"Error|ZSTD_error_checksum_wrong|22|Restored data doesn't match checksum",` +
		`"Error|Z_DATA_ERROR|-3|incorrect data check",` +
		`"Error|Z_DATA_ERROR|-3|incorrect length check",` +
		`"Error|Z_NEED_DICT|2|Missing dictionary",` +
		`"Error|Z_NEED_DICT|2|Bad dictionary",` +
		`"hello world hello",` +
		`"RangeError|ERR_BUFFER_TOO_LARGE||Cannot create a Buffer larger than 10 bytes",` +
		`"RangeError|ERR_BROTLI_INVALID_PARAM||999 is not a valid Brotli parameter",` +
		`"RangeError|ERR_ZSTD_INVALID_PARAM||9999 is not a valid zstd parameter",` +
		`"RangeError|ERR_OUT_OF_RANGE||The value of \"options.level\" is out of range. It must be >= -1 and <= 9. Received 10",` +
		`"RangeError|ERR_OUT_OF_RANGE||The value of \"options.chunkSize\" is out of range. It must be >= 64. Received 3",` +
		`"TypeError|ERR_INVALID_ARG_TYPE||The \"buffer\" argument must be of type string or an instance of Buffer, TypedArray, DataView, or ArrayBuffer. Received type number (5)",` +
		`"TypeError|ERR_INVALID_ARG_TYPE||The \"data\" argument must be of type string or an instance of Buffer, TypedArray, or DataView. Received type number (1)"]`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestZlibMiscSurface(t *testing.T) {
	got := runScriptString(t, `
		import zlib from "node:zlib";
		const info = zlib.gzipSync("hi", { info: true });
		const d = zlib.createDeflate();
		JSON.stringify([
			zlib.crc32("hello"), zlib.crc32("world", zlib.crc32("hello ")), zlib.crc32(Buffer.from("abc")), zlib.crc32(""),
			zlib.constants.BROTLI_PARAM_QUALITY, zlib.constants.ZSTD_c_compressionLevel, zlib.constants.ZSTD_e_end,
			zlib.BROTLI_DECODER_ERROR_UNREACHABLE, Object.keys(zlib).includes("Z_OK"), zlib.Z_DATA_ERROR,
			zlib.codes.Z_BUF_ERROR, zlib.codes["-3"], Object.isFrozen(zlib.codes),
			Object.keys(info), info.engine instanceof zlib.Gzip, zlib.gunzipSync(info.buffer).toString(),
			typeof d.params, typeof d.flush, typeof d.reset, d.bytesWritten,
		])
	`)
	want := `[907060870,222957957,891568578,0,1,100,2,-31,false,-3,-5,"Z_DATA_ERROR",true,["buffer","engine"],true,"hi","function","function","function",0]`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestZlibFlushAndParamsKeepOneValidStream(t *testing.T) {
	got := runScriptString(t, `
		import zlib from "node:zlib";
		const d = zlib.createDeflate();
		const out = [];
		d.on("data", (c) => out.push(c));
		await new Promise((resolve) => {
			d.write("hello");
			d.flush(() => {
				const flushed = Buffer.concat(out);
				globalThis.__flushedTail = flushed.subarray(-4).toString("hex");
				globalThis.__bw = d.bytesWritten;
				d.params(9, 0, () => {
					d.write("world");
					d.flush(zlib.constants.Z_FINISH, resolve);
				});
			});
		});
		JSON.stringify([globalThis.__flushedTail, globalThis.__bw, zlib.inflateSync(Buffer.concat(out)).toString()])
	`)
	if got != `["0000ffff",5,"helloworld"]` {
		t.Errorf("got %s", got)
	}
}

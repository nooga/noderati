package host

import "testing"

// Parity tests for node:crypto. Every expected value below was produced
// by running the same script under real node v26 (the script's final
// expression printed with console.log).

func TestCryptoHashHmacParity(t *testing.T) {
	got := runScriptString(t, `
		import crypto from "node:crypto";
		const out = {};
		out.hashes = ["md5","sha1","sha224","sha512-256","sha3-256","ripemd160","blake2b512","blake2s256","sm3","md5-sha1","RSA-SHA256"].map((a) => crypto.createHash(a).update("abc").update(new Uint8Array([0xff, 0])).digest("hex"));
		out.shake = crypto.createHash("shake256", { outputLength: 40 }).update("abc").digest("base64url");
		const h = crypto.createHash("sha256").update("a");
		const h2 = h.copy();
		out.copy = [h.digest("hex"), h2.update("b").digest("base64")];
		out.oneShot = crypto.hash("sha1", "abc") + crypto.hash("md5", Buffer.from("x"), "base64");
		out.hmac = crypto.createHmac("sha512", crypto.createSecretKey(Buffer.from("key"))).update("data").digest("hex");
		out.hmacTwice = (() => { const m = crypto.createHmac("sha1", "k"); m.digest(); return m.digest("hex"); })();
		out.errs = [() => crypto.createHash("nope"), () => crypto.createHash(1), () => { const x = crypto.createHash("sha1"); x.digest(); x.update("a"); }, () => crypto.createHmac("nope", "k"), () => crypto.hash("sha1", "a", "bogus")].map((f) => { try { f(); return "no error"; } catch (e) { return (e.code || "") + ":" + e.message; } });
		const s = crypto.createHash("sha1");
		let streamed = "";
		s.on("data", (d) => { streamed = d.toString("hex"); });
		s.write("ab");
		s.end("c");
		await new Promise((r) => setTimeout(r, 0));
		out.streamed = streamed;
		out.isHash = s instanceof crypto.Hash && crypto.Hash("md5") instanceof crypto.Hash;
		JSON.stringify(out)
	`)
	want := `{"hashes":["e01e7b08be3d4d8a1b74b61943856ac2","35a6dd9eec07ed9764c95cf36940b278b374f7b9","dff31520dcc05d5ec602ddafb2ab0c7afd0b519b6c9c9ecf364891dc","45e40c7de715038448143014074437e612ccbd73b0f655392c558cc5c43da0c5","9403ac260343e90b651b310981002e4e34e54bc4f153cf9ee9a162ae6c94ca0b","7c110ee868f286f8e3c4ff89481e05c15c0b8be2","84ccfc52f5e2f96057494f9d80c9b40f4a1f2ef06bd4a9680a79c8b475f13ba26a30c1e5fd4fe463d3c2e4613442a57995e23c9c943eb4c53309f1d2943823e3","9eb0074372ba3adf910269c22bc68060b8b8eb415edad777653f79306ca9e7e7","0a0f29061bbfcb25f0b8bd7e8963123af24dd969569667fd86a12a639e793cf1","e01e7b08be3d4d8a1b74b61943856ac235a6dd9eec07ed9764c95cf36940b278b374f7b9","aaf2c38a3b8617bdff3f793d98c22458a3714a26faf5d06f900009885d42bdd9"],"shake":"SDNmYBNgqHccaGMIDMQRTY20RTD48eHuT5TqN-eLVznVoVvvGGpThg","copy":["ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb","+44g/C5MPySMYMOb1lLzwTRymLuXe4tNWQO4UFViBgM="],"oneShot":"a9993e364706816aba3e25717850c26c9cd0d89dndTkYSaMgDT1yFZOFVxnpg==","hmac":"3c5953a18f7303ec653ba170ae334fafa08e3846f2efe317b87efce82376253cb52a8c31ddcde5a3a2eee183c2b34cb91f85e64ddbc325f7692b199473579c58","hmacTwice":"","errs":[":Digest method not supported","ERR_INVALID_ARG_TYPE:The \"algorithm\" argument must be of type string. Received type number (1)","ERR_CRYPTO_HASH_FINALIZED:Digest already called","ERR_CRYPTO_INVALID_DIGEST:Invalid digest: nope","ERR_INVALID_ARG_VALUE:The argument 'outputEncoding' is invalid. Received 'bogus'"],"streamed":"a9993e364706816aba3e25717850c26c9cd0d89d","isHash":true}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestCryptoRandomKDFParity(t *testing.T) {
	got := runScriptString(t, `
		import crypto from "node:crypto";
		const out = {};
		out.pbkdf2 = crypto.pbkdf2Sync("password", "salt", 1000, 40, "sha256").toString("hex");
		out.scrypt = crypto.scryptSync("password", "salt", 24, { cost: 1024, blockSize: 4, parallelization: 2 }).toString("hex");
		out.hkdf = Buffer.from(crypto.hkdfSync("sha384", "ikm", "salt", "info", 50)).toString("hex");
		out.hkdfIsAB = crypto.hkdfSync("sha256", "k", "", "", 4) instanceof ArrayBuffer;
		out.argon2 = ["argon2d", "argon2i", "argon2id"].map((a) => crypto.argon2Sync(a, { message: "pw", nonce: Buffer.alloc(16, 7), parallelism: 2, tagLength: 24, memory: 32, passes: 2, secret: "s", associatedData: "ad" }).toString("hex"));
		out.async = await Promise.all([
		  new Promise((r) => crypto.pbkdf2("p", "s", 2, 8, "sha1", (e, k) => r(k.toString("hex")))),
		  new Promise((r) => crypto.scrypt("p", "s", 8, { N: 16 }, (e, k) => r(k.toString("hex")))),
		  new Promise((r) => crypto.hkdf("sha1", "k", "s", "i", 8, (e, k) => r(Buffer.from(k).toString("hex")))),
		  new Promise((r) => crypto.argon2("argon2id", { message: "pw", nonce: Buffer.alloc(16), parallelism: 1, tagLength: 8, memory: 8, passes: 1 }, (e, k) => r(k.toString("hex")))),
		  new Promise((r) => crypto.randomBytes(5, (e, b) => r(b.length))),
		  new Promise((r) => crypto.randomInt(10, 12, (e, n) => r(n >= 10 && n < 12))),
		]);
		const ri = Array.from({ length: 200 }, () => crypto.randomInt(-3, 3));
		out.randomInt = ri.every((x) => x >= -3 && x < 3) && new Set(ri).size === 6;
		out.uuids = [/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(crypto.randomUUID()), /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(crypto.randomUUIDv7())];
		const filled = crypto.randomFillSync(Buffer.alloc(8), 2, 4);
		out.fill = filled[0] === 0 && filled[1] === 0 && filled[6] === 0 && filled[7] === 0;
		out.errs = [
		  () => crypto.randomBytes(-1), () => crypto.randomInt(10, 5), () => crypto.randomInt(2 ** 48),
		  () => crypto.randomFillSync(new Uint32Array(4), 3, 2), () => crypto.getRandomValues(new Float64Array(1)),
		  () => crypto.pbkdf2Sync("p", "s", 0, 10, "sha1"), () => crypto.pbkdf2Sync("p", "s", 1, 10, "nope"),
		  () => crypto.scryptSync("p", "s", 16, { N: 3 }), () => crypto.scryptSync("p", "s", 16, { N: 16, cost: 16 }),
		  () => crypto.hkdfSync("sha256", "k", "s", "i", 255 * 32 + 1),
		  () => crypto.argon2Sync("argon2id", { message: "pw", nonce: Buffer.alloc(16), parallelism: 2, tagLength: 4, memory: 8, passes: 1 }),
		  () => crypto.timingSafeEqual(Buffer.alloc(1), Buffer.alloc(2)),
		].map((f) => { try { f(); return "no error"; } catch (e) { return e.name + ":" + (e.code || "") + ":" + e.message; } });
		JSON.stringify(out)
	`)
	want := `{"pbkdf2":"632c2812e46d4604102ba7618e9d6d7d2f8128f6266b4a03264d2a0460b7dcb388b3b1131f741bcb","scrypt":"45530a84dc7fc12ea2336d560183df2b3b531d571f86cee2","hkdf":"8a4904829f7acb5fe62bfbce3ed1a2d9428bdcba65d4db11e7471f3b7ab9eaffb058cfdad0c509f3ccd90685666fa8b2b2f6","hkdfIsAB":true,"argon2":["b9b62b3c0623cce0ff77bf4bffe5f1725beef681cf92332c","e28ca31a799dc331b4b697928d0b7325d1147f9859f0def0","e20669e688e90f03f2d92cc8f7e1059f641c1cf6a0af3a68"],"async":["ea4b66d4030c711f","4efe92b5bba5ee18","e9e6d61340247944","f6b8b333764bf229",5,true],"randomInt":true,"uuids":[true,true],"fill":true,"errs":["RangeError:ERR_OUT_OF_RANGE:The value of \"size\" is out of range. It must be >= 0 && <= 2147483647. Received -1","RangeError:ERR_OUT_OF_RANGE:The value of \"max\" is out of range. It must be greater than the value of \"min\" (10). Received 5","RangeError:ERR_OUT_OF_RANGE:The value of \"max\" is out of range. It must be <= 281474976710655. Received 281_474_976_710_656","RangeError:ERR_OUT_OF_RANGE:The value of \"size + offset\" is out of range. It must be <= 16. Received 20","TypeMismatchError:17:The data argument must be an integer-type TypedArray","RangeError:ERR_OUT_OF_RANGE:The value of \"iterations\" is out of range. It must be >= 1 && <= 2147483647. Received 0","TypeError:ERR_CRYPTO_INVALID_DIGEST:Invalid digest: nope","RangeError:ERR_CRYPTO_INVALID_SCRYPT_PARAMS:Invalid scrypt params","TypeError:ERR_INCOMPATIBLE_OPTION_PAIR:Option \"N\" cannot be used in combination with option \"cost\"","RangeError:ERR_CRYPTO_INVALID_KEYLEN:Invalid key length","RangeError:ERR_OUT_OF_RANGE:The value of \"parameters.memory\" is out of range. It must be >= 16 && <= 4294967295. Received 8","RangeError:ERR_CRYPTO_TIMING_SAFE_EQUAL_LENGTH:Input buffers must have the same byte length"]}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

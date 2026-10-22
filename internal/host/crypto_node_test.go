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
func TestCryptoCipherParity(t *testing.T) {
	got := runScriptString(t, `
		import crypto from "node:crypto";
		const out = {};
		const msg = "The quick brown fox jumps over the lazy dog";
		out.modes = [];
		for (const m of ["cbc", "ecb", "ctr", "cfb", "cfb8", "cfb1", "ofb", "gcm"]) {
		  const name = "aes-192-" + m;
		  const key = Buffer.alloc(24, 7);
		  const iv = m === "ecb" ? null : Buffer.alloc(m === "gcm" ? 12 : 16, 9);
		  const c = crypto.createCipheriv(name, key, iv);
		  if (m === "gcm") c.setAAD(Buffer.from("aad"));
		  const ct = Buffer.concat([c.update(msg.slice(0, 10)), c.update(msg.slice(10)), c.final()]);
		  const tag = m === "gcm" ? c.getAuthTag() : null;
		  const d = crypto.createDecipheriv(name, key, iv);
		  if (tag) { d.setAAD(Buffer.from("aad")); d.setAuthTag(tag); }
		  const pt = d.update(ct, undefined, "utf8") + d.final("utf8");
		  out.modes.push(ct.toString("base64") + (tag ? ":" + tag.toString("hex") : "") + (pt === msg ? "" : " MISMATCH"));
		}
		const ccm = crypto.createCipheriv("aes-128-ccm", Buffer.alloc(16, 1), Buffer.alloc(11, 2), { authTagLength: 12 });
		ccm.setAAD(Buffer.from("hdr"), { plaintextLength: msg.length });
		out.ccm = ccm.update(msg, "utf8", "hex") + ccm.final("hex") + ":" + ccm.getAuthTag().toString("hex");
		const cp = crypto.createCipheriv("chacha20-poly1305", Buffer.alloc(32, 1), Buffer.alloc(12, 2), { authTagLength: 16 });
		out.chachaPoly = cp.update(msg, "utf8", "base64url") + cp.final("base64url") + ":" + cp.getAuthTag().toString("hex");
		const ch = crypto.createCipheriv("chacha20", Buffer.alloc(32, 3), Buffer.alloc(16, 4));
		out.chacha = ch.update(msg, "utf8", "hex") + ch.final("hex");
		out.wrap = crypto.createCipheriv("id-aes256-wrap", Buffer.alloc(32, 1), Buffer.from("a6a6a6a6a6a6a6a6", "hex")).update(Buffer.alloc(24, 5)).toString("hex");
		out.wrapPad = crypto.createCipheriv("aes128-wrap-pad", Buffer.alloc(16, 1), Buffer.from("a65959a6", "hex")).update(Buffer.alloc(13, 5)).toString("hex");
		const des = crypto.createCipheriv("des-ede3-cbc", Buffer.alloc(24, 1), Buffer.alloc(8, 2));
		out.des3 = des.update(msg, "utf8", "hex") + des.final("hex");
		out.errs = [
		  () => crypto.createCipheriv("aes-128-cbc", Buffer.alloc(5), Buffer.alloc(16)),
		  () => crypto.createCipheriv("aes-128-cbc", Buffer.alloc(16), Buffer.alloc(5)),
		  () => crypto.createCipheriv("nope", Buffer.alloc(16), Buffer.alloc(16)),
		  () => crypto.createCipheriv("aes-128-ccm", Buffer.alloc(16), Buffer.alloc(12)),
		  () => { const d = crypto.createDecipheriv("aes-128-cbc", Buffer.alloc(16), Buffer.alloc(16)); d.update(Buffer.alloc(16)); d.final(); },
		  () => { const c = crypto.createCipheriv("aes-128-cbc", Buffer.alloc(16), Buffer.alloc(16)); c.setAutoPadding(false); c.update("abc"); c.final(); },
		  () => { const d = crypto.createDecipheriv("aes-128-gcm", Buffer.alloc(16), Buffer.alloc(12)); d.setAuthTag(Buffer.alloc(16)); d.update("aa", "hex"); d.final(); },
		  () => crypto.createCipheriv("aes-128-gcm", Buffer.alloc(16), Buffer.alloc(12)).getAuthTag(),
		].map((f) => { try { f(); return "no error"; } catch (e) { return e.name + ":" + (e.code || "") + ":" + e.message; } });
		out.info = [crypto.getCipherInfo("aes-256-gcm"), crypto.getCipherInfo(419), crypto.getCipherInfo("aes-128-ccm", { ivLength: 7 }), crypto.getCipherInfo("aes-128-cbc", { ivLength: 7 })];
		const s = crypto.createDecipheriv("aes-128-ctr", Buffer.alloc(16), Buffer.alloc(16));
		const chunks = [];
		s.on("data", (d) => chunks.push(d));
		s.write(Buffer.from("00ff", "hex"));
		s.end(Buffer.from("10", "hex"));
		await new Promise((r) => setTimeout(r, 0));
		out.stream = Buffer.concat(chunks).toString("hex");
		JSON.stringify(out)
	`)
	want := `{"modes":["FjWXSwMXlbySHrdxQk+xsDOkC6xWbcNJnTZZs69Rtbqaoge7mMEzU07Gdkxibb3Q","4zKyZSbHdGWNDkn9Gj+nVeLPJj0cMjix3cozFd6+C0KVOuTxW10gWUyCODgd7CD3","lNZij9e0MGa4UO7gDSsxG/it7b+t9oGyqP0hS7LLMOq6p8Eh8aOMU8ja/w==","lNZij9e0MGa4UO7gDSsxG3NTWpHnxTGX7KBM4Cd3cg/PFrfZPwa0PL/+Xg==","lJfCE40N70VycypiWCGQo+bZF3nrqtdZRpRtiYn/W9sNnMD9rDh77VmL+A==","or68aa33fsjoiN/DLW3a/l7dO7yZarhY9rLM9rCJI+oMLVh39lrjK2RNjQ==","lNZij9e0MGa4UO7gDSsxG53ujEYv6yyG0xeSF+uExzgrm7Fkk4v1rxeoMw==","0EnoKjgGGE08NxbG5sHlPE7a5TdGsfdIi3VFkA+ZMqcao8GclUFk8j+yBQ==:41191da21f9641f0e7c35eef572bab5d"],"ccm":"a37bdd437a2bc3f562549ef19f6a5329babab889a9f99995753c9f56dae6a56f2c498c367c1fbc703bf1cc:7c9a5984d7ed2ee599df286d","chachaPoly":"snSXDvzWgbkaDYuLvPyKCercFC3p3xCjZ6R0y5DkZKi7dtmZWnR_mbKkWA:1cd4b64a561e6272282558b04237042e","chacha":"0894916cb1a1724b6451f8bd91e0115d5a2003cf74fcb42098bbd09b4eab4d0e18801535b8c18d4b4b2bca","wrap":"85d0cc2cded554f89eb523ca8ade0231d52887a1073d64c16a97b52b3d51c684","wrapPad":"0bf103d8eaf785e8775bb2fc4450052e800325553a7275d8","des3":"6700d8cc46c956b200daca4a8d42519553b002a93db71c4ab36c29380da54471d22e72f5fa9f1f6f0cb076550b5296c5","errs":["RangeError:ERR_CRYPTO_INVALID_KEYLEN:Invalid key length","TypeError:ERR_CRYPTO_INVALID_IV:Invalid initialization vector","Error:ERR_CRYPTO_UNKNOWN_CIPHER:Unknown cipher","TypeError:ERR_CRYPTO_INVALID_AUTH_TAG:authTagLength required for aes-128-ccm","Error:ERR_OSSL_BAD_DECRYPT:error:1C800064:Provider routines::bad decrypt","Error:ERR_OSSL_WRONG_FINAL_BLOCK_LENGTH:error:1C80006B:Provider routines::wrong final block length","Error::Unsupported state or unable to authenticate data","Error:ERR_CRYPTO_INVALID_STATE:Invalid state for operation getAuthTag"],"info":[{"mode":"gcm","name":"id-aes256-gcm","nid":901,"keyLength":32,"blockSize":1,"ivLength":12},{"mode":"cbc","name":"aes-128-cbc","nid":419,"keyLength":16,"blockSize":16,"ivLength":16},{"mode":"ccm","name":"id-aes128-ccm","nid":896,"keyLength":16,"blockSize":1,"ivLength":7},null],"stream":"66165b"}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

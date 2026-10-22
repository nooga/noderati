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

func TestCryptoKeyObjectParity(t *testing.T) {
	got := runScriptString(t, `
		import crypto from "node:crypto";
		const K = {"rsa":"-----BEGIN PRIVATE KEY-----\nMIICdgIBADANBgkqhkiG9w0BAQEFAASCAmAwggJcAgEAAoGBAL5TBq15EQ6CCsPi\nK880Spi1zew2W94cXfpgUDjAMkVA9+s+tEFF1bgBIp1O5yDiHXgnw2TYo/TnVCM6\nfff92Os9sEYsNK8Kdi+O1d4M6fZ6OsOYmZH8c+vkghY0D0rAHOel6ae6W/FGDUte\nAyvVbriV9KHJLhK4DBgAnmgZX8m7AgMBAAECgYAz9yttlg7ILU2PKmPX0WlExO8S\nvczbmlc2k7GTrTaMni6S9q303IJ8GSNAXFjTK6nsMr8w+83YgokAjWnzmkXti18I\n6tbw/t2yIJ4AuhIt0YhpHr7Dq9+QHHKYFWm/PnRt9piKS5ZsLrn+1PDUqICUyUTr\nHEZVm/5o2R2NRTkdcQJBAP08Go+AaFPfgC2opOc4+LZC48uPLkzsDY9mejIRqbgz\ny2KYIxQvpG7udxCSsV5LT5QHgaXhFJrV+B9LBqV77l0CQQDAZw++sspr3s3ZECIc\n3Zm1zTP2s+LyaP7O2Ih3DHhBK3U0t/fmdMBc6Xj04xuJhMpLLfxTdtrjCoGg+C+K\nZyb3AkB2nfKrCTDVKLm3qWkkzbDiXs6pCPZdHaz0YMIYNeHT4J8CHdVAnVheheDL\nqIrgalJqOmHmO1oVdOh2cAQ6+6QZAkAIwqiM3kcpMjPdNkbXWZ+uD3HWQcqLjwxL\nDsDn6sFu9jOX+uhmrdo0YM0JGW4fjNRJKAbdJgRBYjEUyzge7SNxAkEApAkqHso/\n2pdvkDqyUdCaXY0JQS27DcDYAzd4eq3rQJDgF0PPvBV5Ma6NljE8nNkVJC5MLCAk\n7P2lBMTrJAGZwQ==\n-----END PRIVATE KEY-----\n","pss256":"-----BEGIN PRIVATE KEY-----\nMIICpwIBADA8BgkqhkiG9w0BAQowL6APMA0GCWCGSAFlAwQCAQUAoRwwGgYJKoZI\nhvcNAQEIMA0GCWCGSAFlAwQCAgUABIICYjCCAl4CAQACgYEA3qUEzinToZtvXGwv\nwQdSvIZF1XKVzPC0kmoHPHdQ0NhApjr1Bh3TTI9l/DvZYEz4cxw7/Fe1OJl6aKov\nhAhchHqDqi2pa6S3Z8T6L/sDBl/UJoDxO9ctUmJXDYN9QLcA94nBMP5JrhMtZgoy\nQEcKzEgMdo9dsyz4OR6WOTLHO1UCAwEAAQKBgQCzTlrsXiseP7wTxt0+q9MqGUks\nebfC8Se7ssBZJg5j44VNWrXyUxrU0bfkx55tPA/rRJXayBgemRxIJqdVwhYe6g8X\nO7pnvfsOrDB9a5oz8TtRKJrnYP0xxnp90udTA45Tplo2nwWbV+Si7fhfyYP85qkk\n60N0EtTMYKiIOoCuEQJBAPzUMI0eL92WJ2HxfYB0SWJiuOLTSECQyp4Ox4ujOHdw\nTF+6z9+Oaihgxho+zPO+qWbrVZwmgxJPJlcBtFChS6MCQQDhb+kEAZ9rxmlYvGdM\nXWwg0LJCgPnMGRe022ROFbh+uq0e0kNGXgLgdWID4SWEXUZ3yjaFtoCbwJxnwLX+\nI8ynAkEAiWs3nGws7g6gBz8/zKLoBB7OiRL3sYhBaNl1ojMsDNcv29RpCgSZNKss\n4bUCpvUZlEQTOxU/pqL4ns/hVvXkawJBAK4zXencnyYW8bzCCUh3QGCzI6xEIcLW\n+PymM4IrQp+a/L++VoH/j1WHHzBEQEtiEFvsg+kbN5Ijaji6L2esni0CQH4E/ahB\nXErlp58gwdq5Eaav9rqHxgWdxEiwZlIo65xIm0QJS4gUqLKjZMtnv2hqBBJVqXfR\nikT6klrYG7zZgms=\n-----END PRIVATE KEY-----\n","p256":"-----BEGIN PRIVATE KEY-----\nMIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQg2RJ+FCLrfxhEZFQF\nCOm2IWO+/ma2M6ZnuLEsMS8w+HehRANCAAT7dO1PbCpyiHkzNJcj1QMBAs2j3Dam\nEhA0FpL+NH8bF0lB2LgYxRo7tiH2pXe7Yv8JOuBPc6Q/XdcmCfre232A\n-----END PRIVATE KEY-----\n","k1":"-----BEGIN PRIVATE KEY-----\nMIGEAgEAMBAGByqGSM49AgEGBSuBBAAKBG0wawIBAQQgReqrRq4+Vg5fzsDGJUnY\nZXczs/eFV45gKSwtqqpbPkyhRANCAAShoZgmA237xXoBUbWIjhFok8EJyrW42DJw\niGD5Qg83jZNutGxQdmh/GGpwNHG2BuJ3Xq/bw6n8/J2gp7WoXSoU\n-----END PRIVATE KEY-----\n","ed":"-----BEGIN PRIVATE KEY-----\nMC4CAQAwBQYDK2VwBCIEIB7d98m9NAP8jZMb/PPbW07yxTMMgPLMBfSsz2qIGoAd\n-----END PRIVATE KEY-----\n","x":"-----BEGIN PRIVATE KEY-----\nMC4CAQAwBQYDK2VuBCIEIOjKv67nOn2huUanFJqqi++kUPQVT61a3rDm2mkxEetN\n-----END PRIVATE KEY-----\n","rsaEncPkcs8":"-----BEGIN ENCRYPTED PRIVATE KEY-----\nMIIC5TBfBgkqhkiG9w0BBQ0wUjAxBgkqhkiG9w0BBQwwJAQQqWFvmvVTzjsM33t/\nJx2iLwICCAAwDAYIKoZIhvcNAgkFADAdBglghkgBZQMEASoEEOzW281XACB+qsQ0\nBC6yLP4EggKAmyiCVAvlQyc4JZoNyP7ogS/4fLGtbceRnc2Yqej6zKD7eUFmFzu3\noS6MhqcRneVRI9J6+efYC+wyHcCkDM3NXiI56ErjSOB0zOQ96ANyzVQX1MYbi2ZO\nmETjrzLqRcubyypHu1O7khkcgGOOiApSnGzLOb31ZmtKkg7QdTjtyaXdYy2zEzxT\nhiwCPqEfVI+ujdqgYKH2/O66X8cruLdRZYeOX9WXqG4hIt4ILL7lmeV45rIb4aHg\nx+RzX3meH2BuqGG/dY+seXhtfldZrX68ad6/poksT4gcOptbEu/eze6NbOFE1caB\nKzMyQR4X09sHvUR3oa3fFe+3byuadwrGuly+2ZXuafzg5pXiYIWuNEWfp8G6ItA3\n9hrWZTi61I5glqgT/Ol2rB7XYcIx4bsEhMdAX/alAelx1LxQqmkzfhkSnQgOuDQ5\nEmsMto+QfrSOa3GDOChavzBqFTjHWFmsKMA//JZGAxnZfIqWDALzYCllIWwTKx0f\naXzo3mP8WpM+mzz6jHrMf3iBIeSu26EFdAEw3VAdak8VH0G5EN2AsLcxjYH4r0dD\nqsic6ST/gsB1f/1ePFew9vZHgmo3AhaTNgJLKvBqP0MLx8AByN1DNdRvbXoCmbNB\nYxsfHD2sAYOjOWp2BjJKbbfOWoAS/Fdk3A6vvnjZEg/LD/Nrttb98KfExQmrheDn\ntrUiVBCtzXPZ7/0arFPL+PSZGVjJrySG9TE7auh+J1lWoYoZPBBTNNKbfhAuK6v1\nbWkSrDKS5jwn0jXR6NloAiGHDt82drIJhfdRxZnqDBlGnT91IbHrMpCwhiBkGHml\nsThSNsKYC3Abcet0aY54IKymH7x4ekDFhw==\n-----END ENCRYPTED PRIVATE KEY-----\n","ecEncSec1":"-----BEGIN EC PRIVATE KEY-----\nProc-Type: 4,ENCRYPTED\nDEK-Info: DES-EDE3-CBC,2ACAE7C7A7FF32E4\n\n4nCqv10uRd9LeVBNfyP7X5xXgNmoULUBJ2oNGCgU+lN0BGf/cSj9FzzKDuKZ1nDj\nVPQf5ujoFy6+Br2AU398rY2FubTkF0roUZqAqLuMJ41r4ayHWtjzy0QEU+NTrf90\nc7I05hoCSutauKKNyXzT3we985hGmUoqMbskQLMHMO4=\n-----END EC PRIVATE KEY-----\n"};
		const out = {};
		const det = (k) => JSON.stringify(k.asymmetricKeyDetails, (a, v) => typeof v === "bigint" ? v + "n" : v);
		for (const name of ["rsa", "pss256", "p256", "k1", "ed", "x"]) {
		  const priv = crypto.createPrivateKey(K[name]);
		  const pub = crypto.createPublicKey(K[name]);
		  const r = [priv.asymmetricKeyType, det(pub), priv.export({ format: "pem", type: "pkcs8" }) === K[name]];
		  r.push(pub.export({ format: "der", type: "spki" }).toString("base64"));
		  try { r.push(JSON.stringify(priv.export({ format: "jwk" }))); } catch (e) { r.push(e.code); }
		  if (name === "p256" || name === "k1") r.push(priv.export({ format: "der", type: "sec1" }).toString("base64"));
		  if (name === "rsa") r.push(pub.export({ format: "pem", type: "pkcs1" }));
		  r.push(pub.equals(crypto.createPublicKey(priv)), priv.equals(pub));
		  out[name] = r;
		}
		out.encrypted = [
		  crypto.createPrivateKey({ key: K.rsaEncPkcs8, passphrase: "pw" }).equals(crypto.createPrivateKey(K.rsa)),
		  crypto.createPrivateKey({ key: K.ecEncSec1, passphrase: "pw" }).equals(crypto.createPrivateKey(K.p256)),
		];
		const ek = crypto.createPrivateKey(K.p256);
		out.reencrypt = crypto.createPrivateKey({ key: ek.export({ format: "pem", type: "pkcs8", cipher: "aes-256-cbc", passphrase: "x" }), passphrase: "x" }).equals(ek);
		out.generated = ["rsa", "rsa-pss", "ec", "ed25519", "x25519", "ml-dsa-44", "ml-kem-768"].map((t) => {
		  const { publicKey, privateKey } = crypto.generateKeyPairSync(t, { modulusLength: 512, namedCurve: "P-521" });
		  return t + ":" + privateKey.asymmetricKeyType + ":" + publicKey.equals(crypto.createPublicKey(privateKey));
		});
		out.errs = [
		  () => crypto.createPrivateKey("garbage"),
		  () => crypto.createPrivateKey({ key: K.rsaEncPkcs8, passphrase: "nope" }),
		  () => crypto.createPrivateKey(crypto.createPublicKey(K.rsa)),
		  () => crypto.createPublicKey({ key: { kty: "EC", crv: "P-256" }, format: "jwk" }),
		  () => crypto.createPrivateKey(K.p256).export({ format: "pem", type: "pkcs1" }),
		  () => crypto.generateKeyPairSync("ec", { namedCurve: "nope" }),
		  () => crypto.generateKeyPairSync("rsa", {}),
		  () => crypto.generateKeySync("aes", { length: 100 }),
		].map((f) => { try { f(); return "no error"; } catch (e) { return e.name + ":" + (e.code || "") + ":" + e.message; } });
		out.secret = [crypto.generateKeySync("hmac", { length: 72 }).symmetricKeySize, crypto.createSecretKey(Buffer.from("abc")).export({ format: "jwk" }).k];
		JSON.stringify(out)
	`)
	want := `{"rsa":["rsa","{\"modulusLength\":1024,\"publicExponent\":\"65537n\"}",true,"MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQC+UwateREOggrD4ivPNEqYtc3sNlveHF36YFA4wDJFQPfrPrRBRdW4ASKdTucg4h14J8Nk2KP051QjOn33/djrPbBGLDSvCnYvjtXeDOn2ejrDmJmR/HPr5IIWNA9KwBznpemnulvxRg1LXgMr1W64lfShyS4SuAwYAJ5oGV/JuwIDAQAB","{\"kty\":\"RSA\",\"n\":\"vlMGrXkRDoIKw-IrzzRKmLXN7DZb3hxd-mBQOMAyRUD36z60QUXVuAEinU7nIOIdeCfDZNij9OdUIzp99_3Y6z2wRiw0rwp2L47V3gzp9no6w5iZkfxz6-SCFjQPSsAc56Xpp7pb8UYNS14DK9VuuJX0ockuErgMGACeaBlfybs\",\"e\":\"AQAB\",\"d\":\"M_crbZYOyC1Njypj19FpRMTvEr3M25pXNpOxk602jJ4ukvat9NyCfBkjQFxY0yup7DK_MPvN2IKJAI1p85pF7YtfCOrW8P7dsiCeALoSLdGIaR6-w6vfkBxymBVpvz50bfaYikuWbC65_tTw1KiAlMlE6xxGVZv-aNkdjUU5HXE\",\"p\":\"_Twaj4BoU9-ALaik5zj4tkLjy48uTOwNj2Z6MhGpuDPLYpgjFC-kbu53EJKxXktPlAeBpeEUmtX4H0sGpXvuXQ\",\"q\":\"wGcPvrLKa97N2RAiHN2Ztc0z9rPi8mj-ztiIdwx4QSt1NLf35nTAXOl49OMbiYTKSy38U3ba4wqBoPgvimcm9w\",\"dp\":\"dp3yqwkw1Si5t6lpJM2w4l7OqQj2XR2s9GDCGDXh0-CfAh3VQJ1YXoXgy6iK4GpSajph5jtaFXTodnAEOvukGQ\",\"dq\":\"CMKojN5HKTIz3TZG11mfrg9x1kHKi48MSw7A5-rBbvYzl_roZq3aNGDNCRluH4zUSSgG3SYEQWIxFMs4Hu0jcQ\",\"qi\":\"pAkqHso_2pdvkDqyUdCaXY0JQS27DcDYAzd4eq3rQJDgF0PPvBV5Ma6NljE8nNkVJC5MLCAk7P2lBMTrJAGZwQ\"}","-----BEGIN RSA PUBLIC KEY-----\nMIGJAoGBAL5TBq15EQ6CCsPiK880Spi1zew2W94cXfpgUDjAMkVA9+s+tEFF1bgB\nIp1O5yDiHXgnw2TYo/TnVCM6fff92Os9sEYsNK8Kdi+O1d4M6fZ6OsOYmZH8c+vk\nghY0D0rAHOel6ae6W/FGDUteAyvVbriV9KHJLhK4DBgAnmgZX8m7AgMBAAE=\n-----END RSA PUBLIC KEY-----\n",true,false],"pss256":["rsa-pss","{\"modulusLength\":1024,\"publicExponent\":\"65537n\",\"hashAlgorithm\":\"sha256\",\"mgf1HashAlgorithm\":\"sha384\",\"saltLength\":20}",true,"MIHOMDwGCSqGSIb3DQEBCjAvoA8wDQYJYIZIAWUDBAIBBQChHDAaBgkqhkiG9w0BAQgwDQYJYIZIAWUDBAICBQADgY0AMIGJAoGBAN6lBM4p06Gbb1xsL8EHUryGRdVylczwtJJqBzx3UNDYQKY69QYd00yPZfw72WBM+HMcO/xXtTiZemiqL4QIXIR6g6otqWukt2fE+i/7AwZf1CaA8TvXLVJiVw2DfUC3APeJwTD+Sa4TLWYKMkBHCsxIDHaPXbMs+DkeljkyxztVAgMBAAE=","ERR_CRYPTO_JWK_UNSUPPORTED_KEY_TYPE",true,false],"p256":["ec","{\"namedCurve\":\"prime256v1\"}",true,"MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE+3TtT2wqcoh5MzSXI9UDAQLNo9w2phIQNBaS/jR/GxdJQdi4GMUaO7Yh9qV3u2L/CTrgT3OkP13XJgn63tt9gA==","{\"kty\":\"EC\",\"x\":\"-3TtT2wqcoh5MzSXI9UDAQLNo9w2phIQNBaS_jR_Gxc\",\"y\":\"SUHYuBjFGju2Ifald7ti_wk64E9zpD9d1yYJ-t7bfYA\",\"crv\":\"P-256\",\"d\":\"2RJ-FCLrfxhEZFQFCOm2IWO-_ma2M6ZnuLEsMS8w-Hc\"}","MHcCAQEEINkSfhQi638YRGRUBQjptiFjvv5mtjOmZ7ixLDEvMPh3oAoGCCqGSM49AwEHoUQDQgAE+3TtT2wqcoh5MzSXI9UDAQLNo9w2phIQNBaS/jR/GxdJQdi4GMUaO7Yh9qV3u2L/CTrgT3OkP13XJgn63tt9gA==",true,false],"k1":["ec","{\"namedCurve\":\"secp256k1\"}",true,"MFYwEAYHKoZIzj0CAQYFK4EEAAoDQgAEoaGYJgNt+8V6AVG1iI4RaJPBCcq1uNgycIhg+UIPN42TbrRsUHZofxhqcDRxtgbid16v28Op/PydoKe1qF0qFA==","{\"kty\":\"EC\",\"x\":\"oaGYJgNt-8V6AVG1iI4RaJPBCcq1uNgycIhg-UIPN40\",\"y\":\"k260bFB2aH8YanA0cbYG4nder9vDqfz8naCntahdKhQ\",\"crv\":\"secp256k1\",\"d\":\"ReqrRq4-Vg5fzsDGJUnYZXczs_eFV45gKSwtqqpbPkw\"}","MHQCAQEEIEXqq0auPlYOX87AxiVJ2GV3M7P3hVeOYCksLaqqWz5MoAcGBSuBBAAKoUQDQgAEoaGYJgNt+8V6AVG1iI4RaJPBCcq1uNgycIhg+UIPN42TbrRsUHZofxhqcDRxtgbid16v28Op/PydoKe1qF0qFA==",true,false],"ed":["ed25519","{}",true,"MCowBQYDK2VwAyEAidEivEMqRmeW2/uGq/OFI5Xy6J7T80sO4Nv6zyTXHHU=","{\"crv\":\"Ed25519\",\"d\":\"Ht33yb00A_yNkxv889tbTvLFMwyA8swF9KzPaogagB0\",\"x\":\"idEivEMqRmeW2_uGq_OFI5Xy6J7T80sO4Nv6zyTXHHU\",\"kty\":\"OKP\"}",true,false],"x":["x25519","{}",true,"MCowBQYDK2VuAyEA1mx7+nQLH6AfGy82SlXC+il72NKL0EOQHj2e5DLWSHA=","{\"crv\":\"X25519\",\"d\":\"6Mq_ruc6faG5RqcUmqqL76RQ9BVPrVresObaaTER600\",\"x\":\"1mx7-nQLH6AfGy82SlXC-il72NKL0EOQHj2e5DLWSHA\",\"kty\":\"OKP\"}",true,false],"encrypted":[true,true],"reencrypt":true,"generated":["rsa:rsa:true","rsa-pss:rsa-pss:true","ec:ec:true","ed25519:ed25519:true","x25519:x25519:true","ml-dsa-44:ml-dsa-44:true","ml-kem-768:ml-kem-768:true"],"errs":["Error:ERR_OSSL_UNSUPPORTED:error:1E08010C:DECODER routines::unsupported","Error:ERR_OSSL_BAD_DECRYPT:error:1C800064:Provider routines::bad decrypt","TypeError:ERR_INVALID_ARG_TYPE:The \"key\" argument must be of type string or an instance of ArrayBuffer, Buffer, TypedArray, or DataView. Received an instance of PublicKeyObject","TypeError:ERR_CRYPTO_INVALID_JWK:Invalid JWK EC key","Error:ERR_CRYPTO_INCOMPATIBLE_KEY_OPTIONS:The selected key encoding pkcs1 can only be used for RSA keys.","TypeError:ERR_CRYPTO_INVALID_CURVE:Invalid EC curve name","TypeError:ERR_INVALID_ARG_TYPE:The \"options.modulusLength\" property must be of type number. Received undefined","TypeError:ERR_INVALID_ARG_VALUE:The property 'options.length' must be one of: 128, 192, 256. Received 100"],"secret":[9,"YWJj"]}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestCryptoSignAsymX509Parity(t *testing.T) {
	got := runScriptString(t, `
		import crypto from "node:crypto";
		const K = {"rsa":"-----BEGIN PRIVATE KEY-----\nMIICdgIBADANBgkqhkiG9w0BAQEFAASCAmAwggJcAgEAAoGBAL5TBq15EQ6CCsPi\nK880Spi1zew2W94cXfpgUDjAMkVA9+s+tEFF1bgBIp1O5yDiHXgnw2TYo/TnVCM6\nfff92Os9sEYsNK8Kdi+O1d4M6fZ6OsOYmZH8c+vkghY0D0rAHOel6ae6W/FGDUte\nAyvVbriV9KHJLhK4DBgAnmgZX8m7AgMBAAECgYAz9yttlg7ILU2PKmPX0WlExO8S\nvczbmlc2k7GTrTaMni6S9q303IJ8GSNAXFjTK6nsMr8w+83YgokAjWnzmkXti18I\n6tbw/t2yIJ4AuhIt0YhpHr7Dq9+QHHKYFWm/PnRt9piKS5ZsLrn+1PDUqICUyUTr\nHEZVm/5o2R2NRTkdcQJBAP08Go+AaFPfgC2opOc4+LZC48uPLkzsDY9mejIRqbgz\ny2KYIxQvpG7udxCSsV5LT5QHgaXhFJrV+B9LBqV77l0CQQDAZw++sspr3s3ZECIc\n3Zm1zTP2s+LyaP7O2Ih3DHhBK3U0t/fmdMBc6Xj04xuJhMpLLfxTdtrjCoGg+C+K\nZyb3AkB2nfKrCTDVKLm3qWkkzbDiXs6pCPZdHaz0YMIYNeHT4J8CHdVAnVheheDL\nqIrgalJqOmHmO1oVdOh2cAQ6+6QZAkAIwqiM3kcpMjPdNkbXWZ+uD3HWQcqLjwxL\nDsDn6sFu9jOX+uhmrdo0YM0JGW4fjNRJKAbdJgRBYjEUyzge7SNxAkEApAkqHso/\n2pdvkDqyUdCaXY0JQS27DcDYAzd4eq3rQJDgF0PPvBV5Ma6NljE8nNkVJC5MLCAk\n7P2lBMTrJAGZwQ==\n-----END PRIVATE KEY-----\n","p256":"-----BEGIN PRIVATE KEY-----\nMIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQg2RJ+FCLrfxhEZFQF\nCOm2IWO+/ma2M6ZnuLEsMS8w+HehRANCAAT7dO1PbCpyiHkzNJcj1QMBAs2j3Dam\nEhA0FpL+NH8bF0lB2LgYxRo7tiH2pXe7Yv8JOuBPc6Q/XdcmCfre232A\n-----END PRIVATE KEY-----\n","k1":"-----BEGIN PRIVATE KEY-----\nMIGEAgEAMBAGByqGSM49AgEGBSuBBAAKBG0wawIBAQQgReqrRq4+Vg5fzsDGJUnY\nZXczs/eFV45gKSwtqqpbPkyhRANCAAShoZgmA237xXoBUbWIjhFok8EJyrW42DJw\niGD5Qg83jZNutGxQdmh/GGpwNHG2BuJ3Xq/bw6n8/J2gp7WoXSoU\n-----END PRIVATE KEY-----\n","ed":"-----BEGIN PRIVATE KEY-----\nMC4CAQAwBQYDK2VwBCIEIB7d98m9NAP8jZMb/PPbW07yxTMMgPLMBfSsz2qIGoAd\n-----END PRIVATE KEY-----\n","x":"-----BEGIN PRIVATE KEY-----\nMC4CAQAwBQYDK2VuBCIEIOjKv67nOn2huUanFJqqi++kUPQVT61a3rDm2mkxEetN\n-----END PRIVATE KEY-----\n","ca":"-----BEGIN CERTIFICATE-----\nMIIDQDCCAiigAwIBAgIUVX0cytXUuIH6nEEuljNtoHjpILkwDQYJKoZIhvcNAQEL\nBQAwQDELMAkGA1UEBhMCVVMxCzAJBgNVBAgMAkNBMRAwDgYDVQQKDAdUZXN0IENB\nMRIwEAYDVQQDDAlUZXN0IFJvb3QwHhcNMjYxMDIyMDAyOTM2WhcNMzYxMDE5MDAy\nOTM2WjBAMQswCQYDVQQGEwJVUzELMAkGA1UECAwCQ0ExEDAOBgNVBAoMB1Rlc3Qg\nQ0ExEjAQBgNVBAMMCVRlc3QgUm9vdDCCASIwDQYJKoZIhvcNAQEBBQADggEPADCC\nAQoCggEBAMIZ7tbtsqRxsrvoB0I0gF8tZWI/yjbeIUef+Sg5XzW+n0t/5eMcHWeE\nmrzKywYKnNocxYh8GfrWbawGmGRKRDiVzK35zZwebHcnwIuZ5mNsvjdF+PExJ74f\nStUO/MMNbW9SerIxgkK9JVUWKoYoK24uDT1KzvqX5jz06TL5sj+ePMlYe+iUCCur\niSbQKRkbEcgfZtO2yxA5Q/K2d0rGhHRC8Jrsx1mA8VTfZLtMKvaulmV61X6G5p1h\nBO9hGq56ZcQ8//sSrkIU/NhL2Af6vqXjW3DrMUmIODv0s6ba/MiRJxmw7IjrcAw1\nOoxHZgNU47913VCAyZqxrBCyqmNY+YUCAwEAAaMyMDAwHQYDVR0OBBYEFIyUHCew\nsBA84uYv/EegyjdkhUrVMA8GA1UdEwEB/wQFMAMBAf8wDQYJKoZIhvcNAQELBQAD\nggEBAJnAbtW+kgGtivMFKofhkdeBttPOSeY+IRGXqYNsF03MSUezNCmKj/M0PY5Y\nguDOQWJlfS33xiMXg0v+Q2Y1G3y3VguK+VTfD3LD+yBexFGNUaRZbZHebYeORXO7\nz66fHBmaG92LstDBTZX8AzXqcokG8oarcqTYfm967SDK7g5VwFNQGuysBEbeIyNO\n+zdUNvW1LeZzcdrK9p+DA3B1lF4sht+xOBVmck3K552csMgfd1grBw/3Ce68ds+u\nZFwaRnG6YOzqzCpafBvTVr8Tr5gvovanuD1wWzfRfbuVb3LwWy0A4IFXm5Wn+saW\nivbvVzYEt5leZzrXLLDEoVvtRX8=\n-----END CERTIFICATE-----\n","leaf":"-----BEGIN CERTIFICATE-----\nMIIDBTCCAe2gAwIBAgIUPz+t2XbL4RgRW5jeLnvSCVEtKaswDQYJKoZIhvcNAQEL\nBQAwQDELMAkGA1UEBhMCVVMxCzAJBgNVBAgMAkNBMRAwDgYDVQQKDAdUZXN0IENB\nMRIwEAYDVQQDDAlUZXN0IFJvb3QwHhcNMjYxMDIyMDAyOTM2WhcNMjkwMTI0MDAy\nOTM2WjBEMQswCQYDVQQGEwJQTDERMA8GA1UECgwITm9kZXJhdGkxDDAKBgNVBAsM\nA0RldjEUMBIGA1UEAwwLZXhhbXBsZS5jb20wWTATBgcqhkjOPQIBBggqhkjOPQMB\nBwNCAASpMZLrHko1ZdAx9aWhKccKxXdArUHXWAfleo3UTgEmw3bNgzaAZWk54da4\npHF0wEzlje9z1w5fQzt4O2UIR119o4G9MIG6MDoGA1UdEQQzMDGCC2V4YW1wbGUu\nY29tgg0qLmV4YW1wbGUub3JnhwR/AAABgQ1hQGV4YW1wbGUuY29tMB0GA1UdJQQW\nMBQGCCsGAQUFBwMBBggrBgEFBQcDAjBdBggrBgEFBQcBAQRRME8wIwYIKwYBBQUH\nMAGGF2h0dHA6Ly9vY3NwLmV4YW1wbGUuY29tMCgGCCsGAQUFBzAChhxodHRwOi8v\nY2EuZXhhbXBsZS5jb20vY2EuY3J0MA0GCSqGSIb3DQEBCwUAA4IBAQCqVfh8EW8c\nXIfxrs7sOeytl+G8HZ0rkcaNHnQWGal0yvIEVOlXvcFI4VYfTPIe3oMEMpVZ8o5o\nxsdIDE9KJjgQ3CAZfaUuKuRdx3usU5J8CWSIEiWFWuUrTLKCCYZaS+1qftRrH4d9\navU1FeD1NNR18dlQVwOThZpvt66ynM4MKP9SjrB7Vii1klFAPE9fPPZw0YU9x2Hw\nr3kLZolHc3OrQ6+94JIKZD8rGru8RY2MkSZOYiYPWkfjFmW9PVkudx3jJunxp/Sp\ngyImYlHy5TZV6S/gmvlkMWO0gRmOQ8wL6FQzon3UTpIjfPQrP2Dx21Dp4OxU0BnR\nL6GaePPEFE1r\n-----END CERTIFICATE-----\n","leafKey":"-----BEGIN PRIVATE KEY-----\nMIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQg6UkNRXyzLT3ADTjH\nFtbNNgiEJJ5ckJfx+w8hbmGxj3WhRANCAASpMZLrHko1ZdAx9aWhKccKxXdArUHX\nWAfleo3UTgEmw3bNgzaAZWk54da4pHF0wEzlje9z1w5fQzt4O2UIR119\n-----END PRIVATE KEY-----\n","edCert":"-----BEGIN CERTIFICATE-----\nMIICZzCCAhmgAwIBAgIUUoG67hxNBf85Ec3XZb92GZkcjXkwBQYDK2VwMIGJMR0w\nCgYDVQQLDANhImIwDwYDVQQDDAhFZCwgSW5jLjEkMCIGA1UECgwbWmHDhcK8w4PC\ns8OFwoLDhMKHIC9MPSBsZWFkMRQwEgYJKoZIhvcNAQkBFgV4QHkuejETMBEGCgmS\nJomT8ixkARkWA2NvbTEXMBUGCgmSJomT8ixkARkWB2V4YW1wbGUwHhcNMjYxMDIy\nMDAzMjU2WhcNMzYxMDE5MDAzMjU2WjCBiTEdMAoGA1UECwwDYSJiMA8GA1UEAwwI\nRWQsIEluYy4xJDAiBgNVBAoMG1phw4XCvMODwrPDhcKCw4TChyAvTD0gbGVhZDEU\nMBIGCSqGSIb3DQEJARYFeEB5LnoxEzARBgoJkiaJk/IsZAEZFgNjb20xFzAVBgoJ\nkiaJk/IsZAEZFgdleGFtcGxlMCowBQYDK2VwAyEAcFq5zN5EXTJHWOlbXxCvYovU\nWFa4LqMG7/TZ0IqdY9WjgZAwgY0wcAYDVR0RBGkwZ4IHYSxiLmNvbYYMaHR0cDov\nL3gvYSxihxAAAAAAAAAAAAAAAAAAAAABhxAgAQ24AAAAAAAAAAAAAAAFgQVxQHIu\nc4IOZiouZXhhbXBsZS5jb22gDgYDKgMEoAcMBWhlbGxvggNpdHMwDAYDVR0TAQH/\nBAIwADALBgNVHQ8EBAMCB4AwBQYDK2VwA0EA0dMGOiHy/+L6Ajnpew/cZYzJziAC\nYPcBwt5xrSVcf/mPyyqlOKbbLFgosx7TubM5QYHQXMRxzLPqGvqVhVV2AQ==\n-----END CERTIFICATE-----\n","pssCert":"-----BEGIN CERTIFICATE-----\nMIIDRDCCAfigAwIBAgIUbJsh8UVA3aCmqv3GzeYtXB8ICJUwQQYJKoZIhvcNAQEK\nMDSgDzANBglghkgBZQMEAgEFAKEcMBoGCSqGSIb3DQEBCDANBglghkgBZQMEAgEF\nAKIDAgEgMA4xDDAKBgNVBAMMA3BzczAeFw0yNjEwMjIwMDMyNTZaFw0zNjEwMTkw\nMDMyNTZaMA4xDDAKBgNVBAMMA3BzczCCASIwDQYJKoZIhvcNAQEBBQADggEPADCC\nAQoCggEBAMavP6jnBQMq4hdPKs4nxHxtojbjZbvmv9BVBpJT0+TwEbfmnNPqSxSn\n0OHKTHvyCNVWCRxdfqBOEa2zKi/W+U7jrG0cWztT4vWDSWaW+wkVTIFhLpB5YyvQ\np7jk64jun0FDqU7kVsN2X9ooqc4Bg+6Re5jH5Z7tcwSeohXYAgWCUXH9k9wUkAaN\nDgIDJCEFQ7pgOaDaKL6ocBu1j11k10wKPqrlkAlV14Ps9utVjdHYE9mrSksLX0/p\ncSmg7kpNMicLDZdIvjhQyXM3T/kSahrNdbPkGb5/Uy1Kfu0rtTeT3qyDsngqWP86\nMhDahxxldMUxOeDvfjR2fPyYrbYXjo8CAwEAAaMyMDAwHQYDVR0OBBYEFJoyCVBY\nAkNl7E5qzvdjMfQaIF4RMA8GA1UdEwEB/wQFMAMBAf8wQQYJKoZIhvcNAQEKMDSg\nDzANBglghkgBZQMEAgEFAKEcMBoGCSqGSIb3DQEBCDANBglghkgBZQMEAgEFAKID\nAgEgA4IBAQA4OAM7hKon46e1WuBqyH87yK+FPskn1g1Gy3NpLsI3mCJzP9VMWp2n\nPA8707bzs/VTpNyribBCAoH2VzvqsbXYoxtbuudBPDqTWNuGhJXezouSCg/ff+p6\n7uwbov/J+6dzVjO2Od+ArG28PaaTrlkitiPkkVkZeBFATsVcUWTfHTjaoKENut26\neOIubgxEqR7YkNnAKl4KB4j7zUDlTStZCeLZbFnalfAsjvjeJs0EGfOLDIv3kn2l\nw6VGkX8ED9RnrVDgI19dPJc8asl0IBdpCu0oyth29phvezo1YbF0/95EktJ5y25P\nvoXn4NNeDcmzKHNdw05/tAiUbLKeUX9P\n-----END CERTIFICATE-----\n","k1Cert":"-----BEGIN CERTIFICATE-----\nMIIBiTCCAS+gAwIBAgIUA9hbOioIbUXAmKSBZ4SHIWzQLgAwCgYIKoZIzj0EAwIw\nLDELMAkGA1UEAwwCazExHTAbBgkqhkiG9w0BCQEWDksxQEV4YW1wbGUuQ09NMB4X\nDTI2MTAyMjAwMzI1NloXDTM2MTAxOTAwMzI1NlowLDELMAkGA1UEAwwCazExHTAb\nBgkqhkiG9w0BCQEWDksxQEV4YW1wbGUuQ09NMFYwEAYHKoZIzj0CAQYFK4EEAAoD\nQgAEiVgPZ7sbO8hL48jtIkS3PUsfK84IDdp0IyRaYejuTcS0oSmIrCTj0ghB3WmH\nXHd48uVKQEzK/Rv31vNtvfp0aKMyMDAwHQYDVR0OBBYEFHKreM8Ys1N0Nv409XwT\n/fOkatboMA8GA1UdEwEB/wQFMAMBAf8wCgYIKoZIzj0EAwIDSAAwRQIgPBgD1R6B\nyuQyK1y69vohXX/fcSFIx1BSZA5192xvApkCIQDFMntLXCeWbNzBN78cSu76xXaz\nyZjjIjYYmMOEwBaCFA==\n-----END CERTIFICATE-----\n"};
		const N = {"ecdsaDer":"304602210095b605870d941af2375647a2eb7bc5e2a22942a0eeddb4abaaf2e2b23aca19570221008f54d0f37772d7ddec564997dc654c6bd26efd783d4fbcb400cf488177da2ea9","ecdsaP1363":"5573b2efc5f4fe40554fd0b8080c50ea5b34f8e9ce8838ea231fb4e1734165d87577c19e8ae3c3b4ea9a0410a1046eb813418f3ec12f64c624a0e6115eab01d6","k1":"304502206dad3ef8e720f33ff9ab44545a6b2d205a183d44b472994831712bf053017bc8022100e23cb8dea1feae48c574b56e070eda93749e3387ee699af9026ada474cfe332b","pss":"22839110b0276a9b436a1300c0168c8980dce66f081beeed6cfc8fd1c59100f4afe9cfc9f866a9e65787c54e2437b58746e72e51fae533e2ab5875ce166fb63ccc08c0dbbfdf76767280436d47e0913b9df1fddb5c4988d11d6042a2d2af885c217055bb49d7c0598bc0906beb225d642fc81eb121908cb7338cdb68f3f91da5","oaep":"8e426bdb9fc5f4ee859b4f7495ca96d10951296dd46765ed93690b66c07aab55140eb198f5f62ae7df3acd8894f18207f348a4f205713d93a260db018359d7b06413ba943f4f7ac0044888b9a9dc913d9bdacdaa06447848e063ef3fd8138a2d21df530ceba13534ee91585f4d1efbb7fb396c1d82d371ef50e943c4c7e75bcc","pkcs1":"04466b947813116883a8aa0e89d57d7e3fb4439acef6ec17d03cbb077b682e147bbf1daa2cc890513c34ab744f729ae175e7332a49872cf128f89f260d142eea07bdf22d98cfec19cc36dfd6e8f76272184ebab14f604c04ce29cea3fb6a07ff4232ec42ed4e3675695332721f0df840cf9d57b44138f6ee21e9ce5fcdd4f442"};
		const msg = Buffer.from("noderati says hi");
		const hex = (b) => Buffer.from(b).toString("hex");
		const err = (f) => { try { return f(); } catch (e) { return [e.name, e.code, e.message].join("|"); } };
		const out = {};
		const pub = (k) => crypto.createPublicKey(k);
		out.sign = [
		  hex(crypto.sign("sha256", msg, K.rsa)),
		  hex(crypto.sign(null, msg, K.ed)),
		  crypto.createSign("sha512").update("noderati ").update("says hi").sign(K.rsa, "base64"),
		  crypto.createVerify("sha512").update(msg).verify(pub(K.rsa), crypto.createSign("sha512").update(msg).sign(K.rsa)),
		];
		out.verify = [
		  crypto.verify("sha256", msg, K.p256, Buffer.from(N.ecdsaDer, "hex")),
		  crypto.verify("sha384", msg, { key: pub(K.p256), dsaEncoding: "ieee-p1363" }, Buffer.from(N.ecdsaP1363, "hex")),
		  crypto.verify("sha256", msg, pub(K.k1), Buffer.from(N.k1, "hex")),
		  crypto.verify("sha256", msg, { key: K.rsa, padding: crypto.constants.RSA_PKCS1_PSS_PADDING, saltLength: 20 }, Buffer.from(N.pss, "hex")),
		  crypto.verify("sha256", Buffer.from("other"), K.p256, Buffer.from(N.ecdsaDer, "hex")),
		  crypto.verify("sha256", msg, K.p256, crypto.sign("sha256", msg, K.p256)),
		  crypto.verify(null, msg, pub(K.ed), crypto.sign(null, msg, K.ed)),
		  crypto.verify("sha256", msg, { key: K.p256, dsaEncoding: "ieee-p1363" }, crypto.sign("sha256", msg, { key: K.p256, dsaEncoding: "ieee-p1363" })),
		];
		out.rsa = [
		  crypto.privateDecrypt({ key: K.rsa, oaepHash: "sha256" }, Buffer.from(N.oaep, "hex")).toString(),
		  crypto.privateDecrypt({ key: K.rsa, padding: crypto.constants.RSA_PKCS1_PADDING }, Buffer.from(N.pkcs1, "hex")).toString(),
		  hex(crypto.privateEncrypt(K.rsa, msg)),
		  crypto.publicDecrypt(pub(K.rsa), crypto.privateEncrypt(K.rsa, msg)).toString(),
		  crypto.privateDecrypt(K.rsa, crypto.publicEncrypt(pub(K.rsa), msg)).toString(),
		  err(() => crypto.privateDecrypt(K.rsa, Buffer.alloc(128, 1))),
		];
		const ecdhA = crypto.createECDH("prime256v1");
		ecdhA.setPrivateKey(Buffer.alloc(32, 7));
		const ecdhB = crypto.createECDH("prime256v1");
		ecdhB.setPrivateKey(Buffer.alloc(32, 9));
		out.ecdh = [
		  ecdhA.getPublicKey("hex"), ecdhA.getPublicKey("hex", "compressed"),
		  ecdhA.computeSecret(ecdhB.getPublicKey()).toString("hex"),
		  crypto.ECDH.convertKey(ecdhA.getPublicKey("hex", "compressed"), "prime256v1", "hex", "hex", "uncompressed"),
		  err(() => ecdhA.computeSecret(Buffer.from("0400", "hex"))),
		  err(() => crypto.createECDH("nope")),
		];
		const x2 = crypto.generateKeyPairSync("x25519");
		out.dh = [
		  hex(crypto.diffieHellman({ privateKey: crypto.createPrivateKey(K.x), publicKey: x2.publicKey })) === hex(
		    crypto.diffieHellman({ privateKey: x2.privateKey, publicKey: pub(K.x) }))
		];
		const g0 = crypto.getDiffieHellman("modp14"); const g = crypto.createDiffieHellman(g0.getPrime(), g0.getGenerator());
		g.setPrivateKey(Buffer.alloc(32, 3));
		g.generateKeys();
		const g2 = crypto.createDiffieHellman(crypto.createDiffieHellmanGroup("modp14").getPrime(), 2);
		g2.setPrivateKey(Buffer.alloc(32, 5));
		g2.generateKeys();
		const d1 = crypto.createDiffieHellman(Buffer.from("ffffffffffffffffc90fdaa22168c234c4c6628b80dc1cd1", "hex"), 2);
		out.modp = [
		  g.getPublicKey("hex").slice(0, 64), g.getPrime().length, g.getGenerator("hex"), g.verifyError,
		  hex(g.computeSecret(g2.getPublicKey())) === hex(g2.computeSecret(g.getPublicKey())),
		  d1.verifyError, d1.getPrime("base64"),
		  err(() => crypto.getDiffieHellman("modp99")),
		];
		out.prime = [
		  crypto.checkPrimeSync(7n), crypto.checkPrimeSync(Buffer.from([0x0f])), crypto.checkPrimeSync(2n ** 61n - 1n),
		  crypto.checkPrimeSync(2n ** 61n + 1n),
		  crypto.generatePrimeSync(32, { bigint: true }) < 2n ** 32n,
		  crypto.checkPrimeSync(crypto.generatePrimeSync(64)),
		  err(() => crypto.checkPrimeSync("x")),
		];
		const C = new crypto.X509Certificate(K.ca);
		const L = new crypto.X509Certificate(Buffer.from(K.leaf));
		const E = new crypto.X509Certificate(K.edCert);
		const P = new crypto.X509Certificate(K.pssCert);
		const S = new crypto.X509Certificate(K.k1Cert);
		out.x509 = [C, L, E, P, S].map((x) => [x.subject, x.issuer, x.subjectAltName, x.infoAccess, x.validFrom, x.validTo,
		  x.validToDate.getTime(), x.fingerprint, x.fingerprint256, x.fingerprint512, x.keyUsage, x.serialNumber, x.ca,
		  x.signatureAlgorithm, x.signatureAlgorithmOid, x.raw.length, x.publicKey.asymmetricKeyType,
		  x.verify(x.publicKey), x.verify(C.publicKey), x.checkIssued(x), x.checkIssued(C), x.toString() === x.toJSON()]);
		const lo = L.toLegacyObject();
		out.legacy = [Object.keys(lo).join(), JSON.stringify(lo.subject), JSON.stringify(lo.infoAccess), lo.pubkey.length,
		  lo.bits, lo.asn1Curve, lo.nistCurve, Object.getPrototypeOf(lo.subject) === null,
		  C.toLegacyObject().modulus.slice(0, 16), C.toLegacyObject().exponent, C.toLegacyObject().pubkey.length];
		out.checks = [
		  L.checkHost("example.com"), L.checkHost("foo.example.org"), L.checkHost("a.b.example.org"),
		  L.checkHost("foo.example.org", { wildcards: false }), L.checkHost("example.com", { subject: "always" }),
		  E.checkHost("foo.example.com"), E.checkHost("oo.example.com"), P.checkHost("pss"), P.checkHost("pss", { subject: "never" }),
		  L.checkEmail("a@example.com"), L.checkEmail("b@example.com"), E.checkEmail("x@y.z"), E.checkEmail("x@y.z", { subject: "always" }),
		  S.checkEmail("K1@example.com"), S.checkEmail("k1@Example.COM"),
		  L.checkIP("127.0.0.1"), L.checkIP("127.0.0.2"), E.checkIP("0:0:0:0:0:0:0:1"),
		  L.checkPrivateKey(crypto.createPrivateKey(K.leafKey)), C.checkPrivateKey(crypto.createPrivateKey(K.leafKey)),
		  new crypto.X509Certificate(L.raw).fingerprint256 === L.fingerprint256,
		  err(() => L.checkIP("foo")), err(() => L.checkHost("x", { subject: "bogus" })),
		  err(() => L.checkIssued({})), err(() => L.verify(crypto.createPrivateKey(K.leafKey))),
		  err(() => new crypto.X509Certificate("garbage")), err(() => new crypto.X509Certificate(1)),
		];
		JSON.stringify(out)
	`)
	want := `{"sign":["83537dcc5984d863d5a3e310f45f385befbb44f19b59f05e399caf600e20526fd23a6e805d424a36290ba5860f8b69af942ffa44642a96848f517139fb24ff6a58e1884fff1428bc0fdd51cd3be4eb185e6285be4a468853bb835501ce456cfdac137df267cf883425feb25b1abcc8740577b1c60d8c493c519ece6379100815","e02a034f20127b7f41e9f07df2d288246111def569c9370a3cee90374c15e8ea319661405ce233201439123dc45eb0b381f224356183df229e8f59529026e60f","lpk4vC82i9PcrElwYZe574vV/hKnjwBtRpTlq7O2bNrwWCnEAgpZnDC2l22ekNVCq2FRZIc+1VSX3fX1eZ3ZtO8lGLE/oMcKESBZy/Y2b992Y0Qn7XYNfBOez/sAxzjEpKRVIFSyitxHYq3bkJkqVSWZqrPmBqcAMTuu/jrqSK0=",true],"verify":[true,true,true,true,false,true,true,true],"rsa":["noderati says hi","noderati says hi","b78a98d9cda1fd0522d4a7462d80cd44c6f8d86c3b1e0133a2a55f539a83bddf073ed103e301a94a67a433031e8c812e9ed3b3e54415d977a7d572a8200cc1a2594346e503366e81ac5e195c2e21314db9d8b359856e1b9b2abe68d2a21bc444e2e31e101e54da3e3724c8f151041d2bf19892caca8036e618807c0ad275e5ae","noderati says hi","noderati says hi","Error|ERR_OSSL_RSA_OAEP_DECODING_ERROR|error:02000079:rsa routines::oaep decoding error"],"ecdh":["041e18532fd4754c02f3041d9c75ceb33b83ffd81ac7ce4fe882ccb1c98bc5896ea46c311c4e2ff40dd96a3653e6e45445d32dfe486eced75c7a90c6a18881c0a3","031e18532fd4754c02f3041d9c75ceb33b83ffd81ac7ce4fe882ccb1c98bc5896e","69d220c6ab7e6194927a8ca5da112e573c08dfccb743d3f7f76900484511feb5","041e18532fd4754c02f3041d9c75ceb33b83ffd81ac7ce4fe882ccb1c98bc5896ea46c311c4e2ff40dd96a3653e6e45445d32dfe486eced75c7a90c6a18881c0a3","Error|ERR_CRYPTO_ECDH_INVALID_PUBLIC_KEY|Public key is not valid for specified curve","TypeError|ERR_CRYPTO_INVALID_CURVE|Invalid EC curve name"],"dh":[true],"modp":["9df1f69060e547317b68f06574fd06c26e47cc9194763f3c98896ee82412c545",256,"02",0,true,129,"///////////JD9qiIWjCNMTGYouA3BzR","Error|ERR_CRYPTO_UNKNOWN_DH_GROUP|Unknown DH group"],"prime":[true,false,true,false,true,true,"TypeError|ERR_INVALID_ARG_TYPE|The \"candidate\" argument must be of type bigint or an instance of ArrayBuffer, TypedArray, Buffer, or DataView. Received type string ('x')"],"x509":[["C=US\nST=CA\nO=Test CA\nCN=Test Root","C=US\nST=CA\nO=Test CA\nCN=Test Root",null,null,"Oct 22 00:29:36 2026 GMT","Oct 19 00:29:36 2036 GMT",2107988976000,"5E:88:8C:98:BD:6B:CE:44:A0:23:70:89:BD:22:53:FC:2B:32:E9:3A","1D:0A:90:74:F5:44:0F:7E:6F:EA:EC:E7:3E:31:8B:C9:31:8E:4F:06:98:9B:62:7D:4D:DF:AA:45:12:AF:6A:92","CC:64:F9:3C:42:50:A0:26:5A:02:42:12:61:D9:40:C3:22:E4:D4:77:70:52:62:33:F7:7B:5A:30:42:1E:0F:34:F6:60:01:5C:52:F8:2B:5F:C8:39:7F:CC:65:86:EC:D4:EE:A6:14:DF:55:DA:E1:7D:A9:7B:E4:FC:4C:34:F4:88",null,"557D1CCAD5D4B881FA9C412E96336DA078E920B9",true,"sha256WithRSAEncryption","1.2.840.113549.1.1.11",836,"rsa",true,true,true,true,true],["C=PL\nO=Noderati\nOU=Dev\nCN=example.com","C=US\nST=CA\nO=Test CA\nCN=Test Root","DNS:example.com, DNS:*.example.org, IP Address:127.0.0.1, email:a@example.com","OCSP - URI:http://ocsp.example.com\nCA Issuers - URI:http://ca.example.com/ca.crt","Oct 22 00:29:36 2026 GMT","Jan 24 00:29:36 2029 GMT",1863908976000,"55:2B:6E:25:0B:B4:58:52:64:AA:7E:B1:57:99:61:92:48:64:39:9A","37:0B:AC:10:45:79:CA:27:98:5B:DD:EA:80:73:E1:14:F3:DC:35:01:EE:16:27:10:7C:82:17:EC:0A:9A:4A:F2","6D:62:AF:F6:1E:83:55:37:A6:CD:76:F8:27:88:74:DC:DA:EA:05:32:26:B5:DD:EA:03:B7:A5:08:D1:F3:6B:05:81:23:6C:F3:BA:7C:FD:02:92:2B:23:80:D7:C9:8F:EC:6A:39:A5:14:8C:85:F9:46:49:96:8B:C5:71:17:90:DD",["1.3.6.1.5.5.7.3.1","1.3.6.1.5.5.7.3.2"],"3F3FADD976CBE118115B98DE2E7BD209512D29AB",false,"sha256WithRSAEncryption","1.2.840.113549.1.1.11",777,"ec",false,true,false,true,true],["OU=a\\\"b + CN=Ed\\, Inc.\nO=ZaÅ¼Ã³ÅÄ /L= lead\nemailAddress=x@y.z\nDC=com\nDC=example","OU=a\\\"b + CN=Ed\\, Inc.\nO=ZaÅ¼Ã³ÅÄ /L= lead\nemailAddress=x@y.z\nDC=com\nDC=example","DNS:\"a\\u002cb.com\", URI:\"http://x/a\\u002cb\", IP Address:0:0:0:0:0:0:0:1, IP Address:2001:DB8:0:0:0:0:0:5, email:q@r.s, DNS:f*.example.com, othername:<unsupported>, DNS:its",null,"Oct 22 00:32:56 2026 GMT","Oct 19 00:32:56 2036 GMT",2107989176000,"D6:EA:E9:4A:90:21:B7:ED:13:52:EB:2B:61:75:42:6E:F3:D5:D8:8D","5A:6F:78:38:C1:2C:A4:1D:AC:FB:14:EA:5F:39:7E:C4:BB:0A:70:52:E6:93:65:02:46:56:A1:2B:16:39:04:0C","48:16:7D:1F:E3:BA:A0:AE:47:3D:28:B1:D0:AC:C9:B6:19:C7:2A:21:C3:4A:09:E2:88:73:6F:D5:0A:56:7D:30:C7:46:A9:16:A0:90:4E:24:F8:07:8C:E5:CD:D9:CB:A4:8B:63:DA:85:D1:7F:A2:EB:8E:77:83:6C:F3:C1:1E:29",null,"5281BAEE1C4D05FF3911CDD765BF7619991C8D79",false,"ED25519","1.3.101.112",619,"ed25519",true,false,false,false,true],["CN=pss","CN=pss",null,null,"Oct 22 00:32:56 2026 GMT","Oct 19 00:32:56 2036 GMT",2107989176000,"57:7A:2C:C7:FC:7F:83:EA:BD:78:78:A3:2D:DB:A7:F4:62:69:6B:9B","85:09:93:E7:7E:84:9B:6E:27:88:07:33:EF:32:38:DB:BB:A1:96:0C:AF:20:81:4F:AB:CE:2E:DD:8A:16:D9:EB","AD:0E:CE:F6:00:D9:41:8F:49:78:48:C3:05:40:B1:58:A6:49:73:75:07:7D:0B:55:24:B0:68:DE:50:77:AD:45:E7:8D:92:38:10:45:1D:5B:A7:98:E7:C7:20:58:4C:8B:9D:63:C4:95:25:B1:39:50:36:D0:20:AB:FC:AC:B5:93",null,"6C9B21F14540DDA0A6AAFDC6CDE62D5C1F080895",true,"rsassaPss","1.2.840.113549.1.1.10",840,"rsa",true,false,true,false,true],["CN=k1\nemailAddress=K1@Example.COM","CN=k1\nemailAddress=K1@Example.COM",null,null,"Oct 22 00:32:56 2026 GMT","Oct 19 00:32:56 2036 GMT",2107989176000,"E2:80:7D:87:54:F8:99:60:6B:C8:F8:C8:EC:07:E4:C2:13:02:66:3D","CF:5A:7D:8F:05:B2:4E:F9:72:8D:1F:08:62:36:8A:01:71:79:25:D2:41:54:BD:91:0C:49:82:53:C9:A2:27:BA","47:31:B0:C6:2B:26:0E:29:64:DE:69:D1:49:D1:BC:D3:60:03:9B:AA:C4:39:C1:39:5C:E4:5C:8B:F8:CC:D3:89:D4:32:FA:74:8F:85:A5:51:24:D4:2E:66:AF:67:BE:49:1D:1F:F8:F2:B2:DE:66:61:C6:BB:EA:ED:DE:EF:63:45",null,"03D85B3A2A086D45C098A481678487216CD02E00",true,"ecdsa-with-SHA256","1.2.840.10045.4.3.2",397,"ec",true,false,true,false,true]],"legacy":["subject,issuer,subjectaltname,infoAccess,ca,modulus,exponent,pubkey,bits,valid_from,valid_to,fingerprint,fingerprint256,fingerprint512,ext_key_usage,serialNumber,raw,asn1Curve,nistCurve","{\"C\":\"PL\",\"O\":\"Noderati\",\"OU\":\"Dev\",\"CN\":\"example.com\"}","{\"OCSP - URI\":[\"http://ocsp.example.com\"],\"CA Issuers - URI\":[\"http://ca.example.com/ca.crt\"]}",65,256,"prime256v1","P-256",true,"C219EED6EDB2A471","0x10001",294],"checks":["example.com","*.example.org",null,null,"example.com","f*.example.com",null,"pss",null,"a@example.com",null,null,"x@y.z","K1@example.com",null,"127.0.0.1",null,"0:0:0:0:0:0:0:1",true,false,true,"TypeError|ERR_INVALID_ARG_VALUE|Invalid IP","TypeError|ERR_INVALID_ARG_VALUE|The property 'options.subject' is invalid. Received 'bogus'","TypeError|ERR_INVALID_ARG_TYPE|The \"otherCert\" argument must be an instance of X509Certificate. Received an instance of Object","TypeError|ERR_INVALID_ARG_VALUE|The argument 'pkey' is invalid. Received PrivateKeyObject {}","Error|ERR_OSSL_PEM_NO_START_LINE|error:0480006C:PEM routines::no start line","TypeError|ERR_INVALID_ARG_TYPE|The \"buffer\" argument must be of type string or an instance of Buffer, TypedArray, or DataView. Received type number (1)"]}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

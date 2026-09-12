package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

func TestCryptoRandomUUID(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { randomUUID } from "node:crypto";
		typeof randomUUID() === "string" && randomUUID().length === 36 ? "ok" : "no"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("randomUUID = %q", val.ToString())
	}
}

func TestCryptoRandomBytes(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { randomBytes } from "node:crypto";
		randomBytes(8).length === 8 ? "ok" : "no"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("randomBytes = %q", val.ToString())
	}
}

func TestCryptoCreateHashSha256(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { createHash } from "node:crypto";
		const hex = createHash("sha256").update("abc").digest("hex");
		const b64 = createHash("sha256").update("abc").digest("base64");
		hex === "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" && typeof b64 === "string" && b64.length > 0 ? "ok" : "no"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("createHash = %q", val.ToString())
	}
}

// TestCryptoGetRandomValues guards a real gap found chasing the Bedrock
// investigation (docs/real-node-plan.md, round 100): real,
// unmodified @smithy/core's own dist-cjs submodules/serde/index.js
// does `const _getRandomValues = node_crypto.getRandomValues;` at
// module top level, then uses it to seed every request's real
// invocation-id UUID - a real, unconditional call, not a hypothetical
// one. Checks the real contract: fills the array in place, returns the
// same reference, and produces different bytes across calls (not a
// fixed/predictable stub).
func TestCryptoGetRandomValues(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import crypto from "node:crypto";
		const a = new Uint8Array(16);
		const returned = crypto.getRandomValues(a);
		const b = new Uint8Array(16);
		crypto.getRandomValues(b);
		JSON.stringify({
			sameReference: returned === a,
			notAllZero: a.some((x) => x !== 0),
			differentEachCall: a.join(",") !== b.join(","),
		})
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"sameReference":true,"notAllZero":true,"differentEachCall":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestCryptoCreateHmac guards a real gap found the same round:
// @smithy/signature-v4's own real SigV4 key-derivation chain
// (getSigningKey -> hmac(sha256, "AWS4"+secret, date) -> hmac(sha256,
// kDate, region) -> ... -> "aws4_request") is built entirely on
// crypto.createHmac - a real, unconditional call for every signed AWS
// request. Verified against real Node's own byte-for-byte output
// (openssl-independent, computed by hand here) for both a plain-string
// key and the exact chained-HMAC-with-a-prior-digest-as-key shape
// SigV4 actually uses.
func TestCryptoCreateHmac(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import crypto from "node:crypto";
		const plain = crypto.createHmac("sha256", "secret-key").update("hello world").digest("hex");
		const kDate = crypto.createHmac("sha256", "AWS4secretkey").update("20240101").digest();
		const kRegion = crypto.createHmac("sha256", kDate).update("us-east-1").digest("hex");
		JSON.stringify({ plain, kRegion })
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	// Independently verified against real Node for this exact input.
	want := `{"plain":"095d5a21fe6d0646db223fdf3de6436bb8dfb2fab0b51677ecf6441fcf5f2a67","kRegion":"f61dc44cc02ffd324484eafc5ee97aa0d0c0f19a012863ab6931e3b3ea396122"}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestCryptoDigestNoEncodingReturnsBuffer guards real Node's own
// hash.digest() contract: with no encoding argument, it returns a real
// Buffer of raw bytes, not a string. This used to return a plain Go
// string of the raw byte values (silently corrupting any byte >= 0x80
// the moment something downstream treated it as UTF-8 text) - the exact
// shape @smithy/signature-v4's own chained HMAC calls depend on getting
// right (see TestCryptoCreateHmac's kDate/kRegion chain above).
func TestCryptoDigestNoEncodingReturnsBuffer(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import crypto from "node:crypto";
		const buf = crypto.createHash("sha256").update("hello").digest();
		JSON.stringify({
			isBuffer: Buffer.isBuffer(buf),
			hex: buf.toString("hex"),
		})
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"isBuffer":true,"hex":"2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestCryptoUpdateWithHighByteUint8Array guards the exact real
// correctness bug found alongside createHmac (see hashHasher's own doc
// comments, crypto.go): hash.update()/hmac.update() used to accept
// only a plain Go string, so a Uint8Array argument (real
// @smithy/signature-v4 code always calls .update() with a real
// Uint8Array, never a plain string) went through paserati's generic
// value-to-Go-string conversion instead of a real byte extraction,
// silently producing a WRONG digest for any byte >= 0x80.
func TestCryptoUpdateWithHighByteUint8Array(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import crypto from "node:crypto";
		crypto.createHash("sha256").update(new Uint8Array([0xff, 0x80, 0x01, 0x02])).digest("hex")
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	// Independently verified against real Node for this exact input.
	want := "5312500438ad634a9538c140a0d148b66e1edafb61259e68cd0468c81cbe3b52"
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestCryptoGetHashesMatchesCreateHash guards that getHashes() reports
// exactly (not more, not fewer than) the algorithms createHash actually
// supports - found missing while probing real undici (round 74,
// docs/real-node-plan.md): its own subresource-integrity.js calls
// crypto.getHashes() unconditionally at module load time.
func TestCryptoGetHashesMatchesCreateHash(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import crypto from "node:crypto";
		const hashes = crypto.getHashes();
		JSON.stringify({
			isArray: Array.isArray(hashes),
			hasSha256: hashes.includes("sha256"),
			hasSha384: hashes.includes("sha384"),
			allCreatable: hashes.every((name) => {
				try { crypto.createHash(name); return true; } catch { return false; }
			}),
		})
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"isArray":true,"hasSha256":true,"hasSha384":true,"allCreatable":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

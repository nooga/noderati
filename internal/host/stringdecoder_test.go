package host

import (
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

// TestStringDecoderBasicRoundTrip is the original shim's own test
// (moved from shims_test.go once string_decoder became a real
// implementation) - a plain ASCII round trip with no split characters,
// the one case the old String(c)-based fake happened to get right too.
func TestStringDecoderBasicRoundTrip(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { StringDecoder } from "node:string_decoder";
		const d = new StringDecoder();
		d.write("hi") + d.end()
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "hi" {
		t.Errorf("StringDecoder = %q, want hi", val.ToString())
	}
}

// TestStringDecoderSplitsUTF8Across3ByteWrite is the actual reason
// StringDecoder exists: a multi-byte UTF-8 character split across two
// separate write() calls (e.g. two TCP reads) must still decode
// correctly once both halves arrive, not turn into mangled bytes or a
// premature partial decode. '€' (U+20AC) encodes to the 3 bytes
// 0xE2 0x82 0xAC - split after the first byte, matching the worst case
// (only the lead byte arrives in the first chunk).
func TestStringDecoderSplitsUTF8Across3ByteWrite(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { StringDecoder } from "node:string_decoder";
		const d = new StringDecoder("utf8");
		const first = d.write(Buffer.from([0xE2]));
		const second = d.write(Buffer.from([0x82, 0xAC]));
		JSON.stringify({ first, second, combined: first + second })
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"first":"","second":"€","combined":"€"}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestStringDecoderSplitsUTF8Across4ByteWriteThreeWays drives the
// longest UTF-8 sequence (4 bytes) split across three separate writes,
// one byte short of complete each time until the last call - the case
// utf8SplitIncomplete's "3 trailing continuation bytes means an
// already-complete 4-byte sequence" branch must not fire early on.
// '😀' (U+1F600) encodes to 0xF0 0x9F 0x98 0x80.
func TestStringDecoderSplitsUTF8Across4ByteWriteThreeWays(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { StringDecoder } from "node:string_decoder";
		const d = new StringDecoder("utf8");
		const parts = [
			d.write(Buffer.from([0xF0, 0x9F])),
			d.write(Buffer.from([0x98])),
			d.write(Buffer.from([0x80])),
		];
		JSON.stringify({ parts, combined: parts.join("") })
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"parts":["","","😀"],"combined":"😀"}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestStringDecoderEndFlushesPendingBytes checks that end() (unlike
// write()) never holds bytes back - a truncated sequence with nothing
// more ever coming must still be flushed as whatever it is, not
// silently dropped.
func TestStringDecoderEndFlushesPendingBytes(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { StringDecoder } from "node:string_decoder";
		const d = new StringDecoder("utf8");
		const w = d.write(Buffer.from([0x68, 0x69, 0xE2])); // "hi" + a truncated 3-byte lead
		const e = d.end();
		JSON.stringify({ w, eLength: e.length })
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"w":"hi","eLength":1}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestStringDecoderHexNeverBuffers checks the hex encoding: every byte
// maps to exactly 2 hex characters, so - unlike utf8 or base64 -
// nothing should ever be held back between write() calls; each call's
// output, concatenated, must match a one-shot hex encode of the same
// bytes.
func TestStringDecoderHexNeverBuffers(t *testing.T) {
	raw := []byte{0x01, 0x02, 0x03, 0x04, 0x05}
	want := hex.EncodeToString(raw)

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { StringDecoder } from "node:string_decoder";
		const d = new StringDecoder("hex");
		d.write(Buffer.from([1, 2])) + d.write(Buffer.from([3])) + d.write(Buffer.from([4, 5])) + d.end()
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != want {
		t.Errorf("got %q, want %q", val.ToString(), want)
	}
}

// TestStringDecoderBase64BuffersToGroupsOfThree checks base64: raw
// bytes are only safe to encode in multiples of 3 (a 4-character
// group) until the true end - encoding early on a non-multiple-of-3
// byte count would emit a premature "=" padding character mid-stream.
// Splits 5 raw bytes as 2+3 across two write() calls; the concatenated
// result (both writes plus end()) must match a one-shot base64 encode
// of all 5 bytes together.
func TestStringDecoderBase64BuffersToGroupsOfThree(t *testing.T) {
	raw := []byte{10, 20, 30, 40, 50}
	want := base64.StdEncoding.EncodeToString(raw)

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { StringDecoder } from "node:string_decoder";
		const d = new StringDecoder("base64");
		const first = d.write(Buffer.from([10, 20]));
		const second = d.write(Buffer.from([30, 40, 50]));
		const last = d.end();
		JSON.stringify({ first, combined: first + second + last })
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	wantJSON := `{"first":"","combined":"` + want + `"}`
	if val.ToString() != wantJSON {
		t.Errorf("got %s, want %s", val.ToString(), wantJSON)
	}
}

// TestStringDecoderAcceptsBufferArgument confirms write() takes a real
// Buffer/Uint8Array, not just a string - the actual real-world input
// shape (a socket 'data' event chunk), via valueToBytes the same way
// fs.go's writeFileSync does.
func TestStringDecoderAcceptsBufferArgument(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { StringDecoder } from "node:string_decoder";
		const d = new StringDecoder("utf8");
		const u = new Uint8Array([0x68, 0x69]);
		d.write(u) + d.end()
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "hi" {
		t.Errorf("got %q, want %q", val.ToString(), "hi")
	}
}

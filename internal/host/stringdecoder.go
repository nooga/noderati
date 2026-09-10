package host

import (
	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// stringDecoder backs the real `string_decoder` module's StringDecoder
// class. Previously this whole module was a JS-string shim
// (registerJSShim("string_decoder", ...)) whose write() just did
// `String(c)` and end() always returned "" - not a decoder at all, just
// a stub that happened to type-check. Real Node's StringDecoder exists
// specifically to decode a byte stream arriving in arbitrary chunks
// (socket reads, in particular) into text correctly even when a
// multi-byte character is split across two chunks - the whole point is
// buffering an incomplete trailing sequence from one write() call and
// completing it with the next one's leading bytes, rather than
// decoding each chunk in isolation (which would turn a split character
// into mangled/replacement-character garbage mid-stream). The old fake
// did neither: every write() just stringified its argument on the
// spot, with nothing carried between calls at all.
type stringDecoder struct {
	vmInst   *vm.VM
	encoding string // normalized via normalizeBufferEncoding (buffer.go)
	pending  []byte // bytes buffered from a previous write(), not yet safe to decode
}

// newStringDecoder is `new StringDecoder(encoding?)`'s constructor,
// closed over vmInst the same way newVMScript (vm.go) and newJSURL
// (url.go) close over host state a plain per-call constructor function
// has no other way to reach - Write/End need vmInst to pull real bytes
// out of a Buffer/Uint8Array argument via valueToBytes (net.go).
func newStringDecoder(vmInst *vm.VM) func(encoding vm.Value) *stringDecoder {
	return func(encoding vm.Value) *stringDecoder {
		enc := "utf8"
		if !encoding.IsUndefined() && !encoding.IsNull() {
			enc = normalizeBufferEncoding(encoding.ToString())
		}
		return &stringDecoder{vmInst: vmInst, encoding: enc}
	}
}

// Write decodes chunk (a Buffer/Uint8Array/string - valueToBytes
// accepts either, same as fs.go's writeFileSync) and returns whatever
// text it can safely produce right now, holding back any trailing
// bytes that might be the start of a character split across this call
// and the next.
func (d *stringDecoder) Write(chunk vm.Value) string {
	return d.process(valueToBytes(d.vmInst, chunk), false)
}

// End flushes any buffered bytes (plus an optional final chunk) and
// returns the remaining decoded text - unlike Write, nothing is held
// back afterward, matching real Node's own end(): whatever's left over
// gets decoded as-is rather than waiting for bytes that will now never
// arrive.
func (d *stringDecoder) End(chunk vm.Value) string {
	var extra []byte
	if !chunk.IsUndefined() {
		extra = valueToBytes(d.vmInst, chunk)
	}
	return d.process(extra, true)
}

func (d *stringDecoder) process(chunk []byte, flush bool) string {
	data := append(d.pending, chunk...)
	d.pending = nil
	if flush {
		return encodeBufferBytes(data, d.encoding)
	}
	switch d.encoding {
	case "base64", "base64url":
		// Base64 encodes 3 raw bytes into 4 characters at a time -
		// emitting early on a byte count that isn't a multiple of 3
		// would produce a padding character ("=") mid-stream, which a
		// concatenation of write() outputs would then contain in the
		// wrong place. Hold back whatever doesn't divide evenly; End
		// (flush=true, above) always encodes the true remainder,
		// padding included.
		n := len(data)
		keep := n % 3
		emit := n - keep
		if keep > 0 {
			d.pending = append([]byte(nil), data[emit:]...)
		}
		return encodeBufferBytes(data[:emit], d.encoding)
	case "hex", "latin1", "binary", "ascii":
		// One input byte always maps to a fixed number of output
		// characters for these encodings - never incomplete, so every
		// write() can be encoded immediately with nothing held back.
		return encodeBufferBytes(data, d.encoding)
	default:
		// utf8 - and ucs2/utf16le, which normalizeBufferEncoding
		// already collapses to utf8 (buffer.go's own established
		// simplification: paserati strings are already real Unicode,
		// so there's no genuine UTF-16LE byte layout to reproduce;
		// matching that here instead of inventing a separate, better
		// utf16 path keeps StringDecoder consistent with what
		// Buffer.prototype.toString() already does for the same
		// encoding names).
		complete, incomplete := utf8SplitIncomplete(data)
		if len(incomplete) > 0 {
			d.pending = append([]byte(nil), incomplete...)
		}
		return encodeBufferBytes(complete, "utf8")
	}
}

// utf8LeadLen classifies a UTF-8 byte: the sequence length (1-4) if b
// starts a new character, 0 if b is a continuation byte (part of a
// multi-byte sequence but not its first byte), or -1 if b can never be
// valid UTF-8 at all (a stray continuation-only byte value like 0xFF).
func utf8LeadLen(b byte) int {
	switch {
	case b&0x80 == 0x00:
		return 1
	case b&0xE0 == 0xC0:
		return 2
	case b&0xF0 == 0xE0:
		return 3
	case b&0xF8 == 0xF0:
		return 4
	case b&0xC0 == 0x80:
		return 0 // continuation byte
	default:
		return -1 // invalid lead byte
	}
}

// utf8SplitIncomplete splits data into a leading portion safe to decode
// now and a trailing portion that might be the start of a multi-byte
// UTF-8 sequence truncated by a write() boundary - the part that needs
// to wait for more bytes before it can be decoded correctly, rather
// than being turned into mangled/replacement-character text
// prematurely. Mirrors real Node's own lib/string_decoder.js algorithm
// (utf8CheckIncomplete/utf8CheckByte): look back up to 3 bytes for the
// lead byte that started the trailing sequence, then check whether
// enough bytes actually followed it to complete that sequence.
//
// The lookback never needs to go past 3 bytes: if the last 3 bytes are
// all continuation bytes with no lead byte among them, the only valid
// explanation is a complete 4-byte sequence (UTF-8's longest) whose
// lead byte is the 4th-from-last - already fully present, nothing left
// incomplete.
func utf8SplitIncomplete(data []byte) (complete, incomplete []byte) {
	n := len(data)
	limit := min(3, n)
	for back := 1; back <= limit; back++ {
		lead := data[n-back]
		need := utf8LeadLen(lead)
		if need == 0 {
			continue // continuation byte - keep looking further back
		}
		if need < 0 {
			return data, nil // not a valid lead byte; nothing to hold back
		}
		if back < need {
			return data[:n-back], data[n-back:] // sequence truncated by this write
		}
		return data, nil // sequence already complete
	}
	// Every byte examined was a continuation byte: too few bytes exist
	// to know anything yet (n < 3) - hold it all back.
	if n < 3 {
		return nil, data
	}
	return data, nil
}

func declareStringDecoder(p *driver.Paserati) {
	vmInst := p.GetVM()
	p.DeclareModule("string_decoder", func(m *driver.ModuleBuilder) {
		m.Class("StringDecoder", &stringDecoder{}, newStringDecoder(vmInst))
		m.Default(nil)
	})
	_ = p.DeclareModuleAlias("node:string_decoder", "string_decoder")
}

package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

// TestMessagePortGlobalIsConstructibleEmitter drives the exact real
// requirement found while probing undici: real undici's
// lib/web/webidl/index.js does `webidl.is.MessagePort =
// webidl.util.MakeTypeAssertion(MessagePort)` at its own module top
// level, so `MessagePort` must exist as a real global constructor. Also
// checks it's a genuine event emitter (on/close) since that's the real,
// if minimal, shape this file actually builds.
func TestMessagePortGlobalIsConstructibleEmitter(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		const port = new MessagePort();
		let closed = false;
		await new Promise((resolve) => {
			port.on("close", () => { closed = true; resolve(); });
			port.postMessage("hello");
			port.close();
		});
		JSON.stringify({
			hasPostMessage: typeof port.postMessage === "function",
			hasOn: typeof port.on === "function",
			closed,
		})
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"hasPostMessage":true,"hasOn":true,"closed":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

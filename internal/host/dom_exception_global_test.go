package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

// TestDOMExceptionGlobalBasics drives the exact real call pattern found
// while probing undici: real undici's lib/web/websocket/util.js/
// eventsource.js/websocket.js all do `throw new DOMException(message,
// name)` directly.
func TestDOMExceptionGlobalBasics(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		const e = new DOMException("bad url", "SyntaxError");
		JSON.stringify({
			message: e.message,
			name: e.name,
			code: e.code,
			staticCode: DOMException.SYNTAX_ERR,
			instanceStaticMatch: e.code === DOMException.SYNTAX_ERR,
		})
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"message":"bad url","name":"SyntaxError","code":12,"staticCode":12,"instanceStaticMatch":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestDOMExceptionGlobalUnknownNameGetsCodeZero checks a name outside
// the 25-entry legacy table gets code 0, matching real DOMException.
func TestDOMExceptionGlobalUnknownNameGetsCodeZero(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		const e = new DOMException("custom", "SomethingElseError");
		e.code
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToFloat() != 0 {
		t.Errorf("got code %v, want 0", val)
	}
}

// TestDOMExceptionGlobalSubclassableWithGetterOverride drives the exact
// real requirement found while probing undici: real undici's
// lib/web/websocket/stream/websocketerror.js does
// `class Test extends DOMException { get reason() { return "" } }` at
// its own module top level, then checks `new Test().reason !==
// undefined` to detect a real Node bug (nodejs/node#59677) - our own
// subclassing must correctly let the getter override work, which this
// test checks directly.
func TestDOMExceptionGlobalSubclassableWithGetterOverride(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		class Test extends DOMException {
			get reason() { return ""; }
		}
		const t = new Test("msg", "AbortError");
		JSON.stringify({
			reasonDefined: t.reason !== undefined,
			isDOMException: t instanceof DOMException,
			message: t.message,
			name: t.name,
		})
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"reasonDefined":true,"isDOMException":true,"message":"msg","name":"AbortError"}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

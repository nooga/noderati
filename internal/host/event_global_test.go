package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

// TestEventGlobalSubclassable drives the exact real requirement found
// while probing undici: real undici's lib/web/websocket/events.js does
// `class MessageEvent extends Event` / `class CloseEvent extends Event`
// at module top level, so `Event` must be a real, subclassable global
// constructor - not just a placeholder.
func TestEventGlobalSubclassable(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		class MyEvent extends Event {
			constructor(type, init) {
				super(type, init);
				this.extra = "hi";
			}
		}
		const e = new MyEvent("close", { cancelable: true });
		JSON.stringify({
			type: e.type,
			cancelable: e.cancelable,
			bubbles: e.bubbles,
			defaultPrevented: e.defaultPrevented,
			extra: e.extra,
			isEvent: e instanceof Event,
		})
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"type":"close","cancelable":true,"bubbles":false,"defaultPrevented":false,"extra":"hi","isEvent":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

func TestEventPreventDefaultOnlyWhenCancelable(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		const notCancelable = new Event("x");
		notCancelable.preventDefault();
		const cancelable = new Event("y", { cancelable: true });
		cancelable.preventDefault();
		JSON.stringify({
			notCancelable: notCancelable.defaultPrevented,
			cancelable: cancelable.defaultPrevented,
		})
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"notCancelable":false,"cancelable":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

func TestCustomEventDetail(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		const e = new CustomEvent("greet", { detail: { name: "world" } });
		JSON.stringify({ type: e.type, detail: e.detail })
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"type":"greet","detail":{"name":"world"}}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestEventTargetAddRemoveDispatch drives the real EventTarget
// contract: addEventListener registers, dispatchEvent invokes with the
// target set correctly and returns whether the event survived
// (uncancelled), removeEventListener actually stops future delivery,
// and { once: true } removes itself after firing.
func TestEventTargetAddRemoveDispatch(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		const target = new EventTarget();
		let calls = 0;
		let sawTarget = null;
		const handler = (e) => { calls++; sawTarget = e.target; };
		target.addEventListener("ping", handler);

		const notReturn = target.dispatchEvent(new Event("ping"));

		let onceCalls = 0;
		target.addEventListener("once-event", () => { onceCalls++; }, { once: true });
		target.dispatchEvent(new Event("once-event"));
		target.dispatchEvent(new Event("once-event"));

		target.removeEventListener("ping", handler);
		target.dispatchEvent(new Event("ping"));

		const cancelableEvent = new Event("cancel-me", { cancelable: true });
		target.addEventListener("cancel-me", (e) => e.preventDefault());
		const survivedCancel = target.dispatchEvent(cancelableEvent);

		JSON.stringify({
			calls, onceCalls,
			sawTargetIsSelf: sawTarget === target,
			notReturn, survivedCancel,
		})
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"calls":1,"onceCalls":1,"sawTargetIsSelf":true,"notReturn":true,"survivedCancel":false}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

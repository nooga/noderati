package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

// TestDNSLookupIPLiteralAll drives the exact real call shape from
// undici's own lib/interceptor/dns.js#defaultLookup:
// `lookup(hostname, { all: true, family, order: 'ipv4first' }, cb)`,
// using an IP literal so the test doesn't depend on external DNS being
// reachable in CI - Go's net.DefaultResolver still runs the same real
// resolution path for a literal, just without a network round trip.
func TestDNSLookupIPLiteralAll(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { lookup } from "node:dns";
		let result;
		await new Promise((resolve, reject) => {
			lookup("127.0.0.1", { all: true, family: 0, order: "ipv4first" }, (err, addresses) => {
				if (err) return reject(err);
				result = addresses;
				resolve();
			});
		});
		JSON.stringify(result)
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `[{"address":"127.0.0.1","family":4}]`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestDNSLookupSingleResult covers the non-`all` shape: callback(err,
// address, family) instead of callback(err, addresses).
func TestDNSLookupSingleResult(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { lookup } from "node:dns";
		let result;
		await new Promise((resolve, reject) => {
			lookup("127.0.0.1", (err, address, family) => {
				if (err) return reject(err);
				result = { address, family };
				resolve();
			});
		});
		JSON.stringify(result)
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"address":"127.0.0.1","family":4}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestDNSLookupErrorForUnresolvable guards the real error path - a
// hostname that genuinely can't resolve (an invalid TLD) must reach the
// callback's err argument, not silently resolve to nothing or hang.
func TestDNSLookupErrorForUnresolvable(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { lookup } from "node:dns";
		let gotError = false;
		await new Promise((resolve) => {
			lookup("this-host-genuinely-does-not-exist.invalid", { all: true }, (err) => {
				gotError = err instanceof Error;
				resolve();
			});
		});
		gotError
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if !val.IsTruthy() {
		t.Errorf("expected a real resolution error for an unresolvable hostname, got %v", val)
	}
}

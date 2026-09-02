package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

func newURLHost(t *testing.T) *driver.Paserati {
	t.Helper()
	p := New([]string{"noderati"})
	declareURL(p)
	p.SetSkipTypeCheck(true)
	return p
}

func TestURLFileRoundtrip(t *testing.T) {
	p := newURLHost(t)
	js := `
		import { fileURLToPath, pathToFileURL } from "url";
		const p = "/tmp/foo";
		fileURLToPath(pathToFileURL(p))
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "/tmp/foo" {
		t.Errorf("file roundtrip = %q, want %q", val.ToString(), "/tmp/foo")
	}
}

func TestURLParse(t *testing.T) {
	p := newURLHost(t)
	js := `
		import { parse } from "url";
		const u = parse("https://example.com:8080/a/b?x=1#h");
		[
			u.protocol,
			u.hostname,
			u.pathname,
			u.search,
			u.hash,
			u.host,
			u.port,
		].join("|")
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := "https:|example.com|/a/b|?x=1|#h|example.com:8080|8080"
	if val.ToString() != want {
		t.Errorf("parse = %q, want %q", val.ToString(), want)
	}
}

func TestURLNodeAlias(t *testing.T) {
	p := newURLHost(t)
	js := `
		import { resolve as resolveNamed } from "url";
		import url from "node:url";
		resolveNamed("https://example.com/a/", "b") === url.resolve("https://example.com/a/", "b")
			? url.resolve("https://example.com/a/", "b")
			: "mismatch"
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "https://example.com/a/b" {
		t.Errorf("resolve = %q", val.ToString())
	}
}

func TestURLSearchParamsFromObject(t *testing.T) {
	p := newURLHost(t)
	js := `
		import { URLSearchParams } from "url";
		const params = new URLSearchParams({ a: "1", b: "hello world" });
		params.toString()
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "a=1&b=hello+world" {
		t.Errorf("toString() = %q", val.ToString())
	}
}

func TestURLSearchParamsFromString(t *testing.T) {
	p := newURLHost(t)
	js := `
		import { URLSearchParams } from "url";
		const params = new URLSearchParams("?b=2&a=1&a=3");
		[params.toString(), params.get("a"), params.getAll("a").join(","), params.has("b")].join("|")
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := "b=2&a=1&a=3|1|1,3|true"
	if val.ToString() != want {
		t.Errorf("= %q, want %q", val.ToString(), want)
	}
}

func TestURLSearchParamsFromPairs(t *testing.T) {
	p := newURLHost(t)
	js := `
		import { URLSearchParams } from "url";
		new URLSearchParams([["x", "1"], ["y", "2"]]).toString()
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "x=1&y=2" {
		t.Errorf("toString() = %q", val.ToString())
	}
}

func TestURLSearchParamsMutation(t *testing.T) {
	p := newURLHost(t)
	js := `
		import { URLSearchParams } from "url";
		const params = new URLSearchParams("a=1&b=2&a=3");
		params.set("a", "9");   // replaces first "a", drops the second
		params.append("c", "10");
		params.delete("b");
		params.sort();
		params.toString()
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "a=9&c=10" {
		t.Errorf("toString() = %q", val.ToString())
	}
}

// TestURLSearchParamsFormEncoding checks the WHATWG
// application/x-www-form-urlencoded percent-encoding rules against values
// shaped like pi-ai's real OAuth device-code request body (see
// urlsearchparams.go's formURLEncode doc comment) — verified against real
// Node's URLSearchParams.toString() on the exact same input. Go's
// url.QueryEscape disagrees with the spec on "~" (spec encodes it, Go
// doesn't) and "*" (spec leaves it raw, Go encodes it), so this pins the
// spec's behavior rather than QueryEscape's.
func TestURLSearchParamsFormEncoding(t *testing.T) {
	p := newURLHost(t)
	js := `
		import { URLSearchParams } from "url";
		const params = new URLSearchParams({
			scope: "read:user",
			grant_type: "urn:ietf:params:oauth:grant-type:device_code",
			redirect: "https://example.com/cb?x=1",
			tilde: "a~b", star: "a*b", paren: "a(b)c", excl: "a!b", quote: "a'b",
		});
		params.toString()
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	// Matches real Node's URLSearchParams(...).toString() on this exact input.
	want := "scope=read%3Auser&grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Adevice_code&redirect=https%3A%2F%2Fexample.com%2Fcb%3Fx%3D1&tilde=a%7Eb&star=a*b&paren=a%28b%29c&excl=a%21b&quote=a%27b"
	if val.ToString() != want {
		t.Errorf("toString() = %q, want %q", val.ToString(), want)
	}
}

func TestURLSearchParamsGetMissing(t *testing.T) {
	p := newURLHost(t)
	js := `
		import { URLSearchParams } from "url";
		new URLSearchParams("a=1").get("missing") === null
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if !val.IsTruthy() {
		t.Errorf("get('missing') did not return null")
	}
}

func TestURLDomainConversion(t *testing.T) {
	p := newURLHost(t)
	js := `
		import { domainToASCII, domainToUnicode } from "url";
		domainToUnicode(domainToASCII("münchen.de"))
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "münchen.de" {
		t.Errorf("domain roundtrip = %q", val.ToString())
	}
}

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

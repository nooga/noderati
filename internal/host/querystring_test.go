package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

func TestQuerystringParse(t *testing.T) {
	p := New([]string{"noderati"})
	declareQuerystring(p)
	p.SetSkipTypeCheck(true)

	js := `
		import { parse } from "querystring";
		const q = parse("foo=bar&baz=qux");
		q.foo + "," + q.baz
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "bar,qux" {
		t.Errorf("parse = %q, want %q", val.ToString(), "bar,qux")
	}
}

func TestQuerystringStringify(t *testing.T) {
	p := New([]string{"noderati"})
	declareQuerystring(p)
	p.SetSkipTypeCheck(true)

	js := `
		import { stringify } from "querystring";
		stringify("a", "1", "b", "2")
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "a=1&b=2" {
		t.Errorf("stringify = %q, want %q", val.ToString(), "a=1&b=2")
	}
}

func TestQuerystringEscapeUnescape(t *testing.T) {
	p := New([]string{"noderati"})
	declareQuerystring(p)
	p.SetSkipTypeCheck(true)

	js := `
		import { escape, unescape } from "querystring";
		unescape(escape("a b"))
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "a b" {
		t.Errorf("unescape(escape(...)) = %q, want %q", val.ToString(), "a b")
	}
}

func TestQuerystringNodeAlias(t *testing.T) {
	p := New([]string{"noderati"})
	declareQuerystring(p)
	p.SetSkipTypeCheck(true)

	js := `
		import qs from "node:querystring";
		qs.stringify("x", "y")
	`
	val, errs := p.RunCode(js, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "x=y" {
		t.Errorf("node:querystring alias = %q, want %q", val.ToString(), "x=y")
	}
}

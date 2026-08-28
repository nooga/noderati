package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

func newWithAssert(argv []string) *driver.Paserati {
	p := New(argv)
	declareAssert(p)
	p.SetSkipTypeCheck(true)
	return p
}

func TestAssertOk(t *testing.T) {
	p := newWithAssert([]string{"noderati"})

	val, errs := p.RunCode(`
		import { ok } from "assert";
		let caught = false;
		try { ok(false); } catch (e) { caught = true; }
		ok(true);
		caught ? "ok" : "fail"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("assert.ok(false) should throw; got %q", val.ToString())
	}
}

func TestAssertEqual(t *testing.T) {
	p := newWithAssert([]string{"noderati"})

	val, errs := p.RunCode(`
		import { equal } from "assert";
		let caught = false;
		try { equal("a", "b"); } catch (e) { caught = true; }
		equal("a", "a");
		caught ? "ok" : "fail"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("assert.equal mismatch should throw; got %q", val.ToString())
	}
}

func TestAssertStrictEqual(t *testing.T) {
	p := newWithAssert([]string{"noderati"})

	val, errs := p.RunCode(`
		import { strictEqual } from "assert";
		let caught = false;
		try { strictEqual("x", "y"); } catch (e) { caught = true; }
		strictEqual("x", "x");
		caught ? "ok" : "fail"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("assert.strictEqual mismatch should throw; got %q", val.ToString())
	}
}

func TestAssertNotEqual(t *testing.T) {
	p := newWithAssert([]string{"noderati"})

	val, errs := p.RunCode(`
		import { notEqual } from "assert";
		let caught = false;
		try { notEqual("same", "same"); } catch (e) { caught = true; }
		notEqual("a", "b");
		caught ? "ok" : "fail"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("assert.notEqual equal values should throw; got %q", val.ToString())
	}
}

func TestAssertNotStrictEqual(t *testing.T) {
	p := newWithAssert([]string{"noderati"})

	val, errs := p.RunCode(`
		import { notStrictEqual } from "assert";
		let caught = false;
		try { notStrictEqual("same", "same"); } catch (e) { caught = true; }
		notStrictEqual("a", "b");
		caught ? "ok" : "fail"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("assert.notStrictEqual equal values should throw; got %q", val.ToString())
	}
}

func TestAssertFail(t *testing.T) {
	p := newWithAssert([]string{"noderati"})

	val, errs := p.RunCode(`
		import { fail } from "assert";
		let caught = false;
		try { fail("boom"); } catch (e) { caught = true; }
		caught ? "ok" : "fail"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("assert.fail should throw; got %q", val.ToString())
	}
}

func TestAssertNodeAlias(t *testing.T) {
	p := newWithAssert([]string{"noderati"})

	val, errs := p.RunCode(`
		import { ok } from "node:assert";
		ok(true);
		"ok"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("node:assert alias failed; got %q", val.ToString())
	}
}

func TestAssertNamedImports(t *testing.T) {
	p := newWithAssert([]string{"noderati"})

	val, errs := p.RunCode(`
		import { ok, equal } from "assert";
		let caught = false;
		try { ok(false); } catch (e) { caught = true; }
		ok(true);
		equal("x", "x");
		caught ? "ok" : "fail"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("named imports test failed; got %q", val.ToString())
	}
}

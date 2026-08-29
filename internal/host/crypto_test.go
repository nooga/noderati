package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

func TestCryptoRandomUUID(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { randomUUID } from "node:crypto";
		typeof randomUUID() === "string" && randomUUID().length === 36 ? "ok" : "no"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("randomUUID = %q", val.ToString())
	}
}

func TestCryptoRandomBytes(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { randomBytes } from "node:crypto";
		randomBytes(8).length === 8 ? "ok" : "no"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("randomBytes = %q", val.ToString())
	}
}

func TestCryptoCreateHashSha256(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { createHash } from "node:crypto";
		const hex = createHash("sha256").update("abc").digest("hex");
		const b64 = createHash("sha256").update("abc").digest("base64");
		hex === "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" && typeof b64 === "string" && b64.length > 0 ? "ok" : "no"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("createHash = %q", val.ToString())
	}
}

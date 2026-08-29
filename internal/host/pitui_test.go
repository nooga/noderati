package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

func TestPiTuiShimExports(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { TUI, KeybindingsManager, ProcessTerminal } from "@earendil-works/pi-tui";
		TUI && new KeybindingsManager() && ProcessTerminal ? "ok" : "no"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("pi-tui exports = %q, want ok", val.ToString())
	}
}

func TestPerfHooksShim(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { performance } from "node:perf_hooks";
		typeof performance.now === "function" ? "ok" : "no"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("perf_hooks = %q, want ok", val.ToString())
	}
}

func TestStringDecoderShim(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { StringDecoder } from "node:string_decoder";
		const d = new StringDecoder();
		d.write("hi") + d.end()
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "hi" {
		t.Errorf("StringDecoder = %q, want hi", val.ToString())
	}
}

func TestPiTuiKeybindingsExtends(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { TuiKeybindingsManager } from "@earendil-works/pi-tui";
		class KeybindingsManager extends TuiKeybindingsManager {}
		const m = new KeybindingsManager({}, "");
		m.getResolvedBindings ? "ok" : "no"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("extends KeybindingsManager = %q, want ok", val.ToString())
	}
}

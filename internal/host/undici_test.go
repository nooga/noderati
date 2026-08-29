package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

func TestUndiciShim(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import * as undici from "undici";
		const agent = new undici.EnvHttpProxyAgent({ allowH2: false });
		undici.setGlobalDispatcher(agent);
		undici.install();
		typeof fetch === "function" && agent.opts.allowH2 === false ? "ok" : "no"
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("undici shim = %q, want ok", val.ToString())
	}
}

package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

// TestHTTP2ConstantsMatchRealNode spot-checks a handful of real
// node:http2.constants entries (one from each of the distinct families
// real Node's own constants object mixes together - an nghttp2 error
// code, a pseudo-header name, an HTTP method, and an HTTP status code)
// plus the total real key count (240, as of the Node version this was
// generated against - see http2.go's own doc comment for how). This
// isn't the exhaustive real call site (only HTTP2_HEADER_PATH/
// HTTP2_HEADER_METHOD are actually used by @smithy/node-http-handler,
// per docs/real-node-plan.md's Round 94), but constants is real, static
// data with no reason to only assert the two keys one caller happens
// to use.
func TestHTTP2ConstantsMatchRealNode(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { constants } from "node:http2";
		JSON.stringify({
			count: Object.keys(constants).length,
			HTTP2_HEADER_PATH: constants.HTTP2_HEADER_PATH,
			HTTP2_HEADER_METHOD: constants.HTTP2_HEADER_METHOD,
			HTTP2_METHOD_POST: constants.HTTP2_METHOD_POST,
			HTTP_STATUS_OK: constants.HTTP_STATUS_OK,
			NGHTTP2_ERR_FRAME_SIZE_ERROR: constants.NGHTTP2_ERR_FRAME_SIZE_ERROR,
		})
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"count":240,"HTTP2_HEADER_PATH":":path","HTTP2_HEADER_METHOD":":method","HTTP2_METHOD_POST":"POST","HTTP_STATUS_OK":200,"NGHTTP2_ERR_FRAME_SIZE_ERROR":-522}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestHTTP2RequireWorks guards the exact CJS shape real code uses
// (@smithy/node-http-handler's own node-http2-handler.js does `import {
// constants } from "node:http2"`, but this project's own established
// pattern - see async_hooks/http/https's own nativeRequireNames
// comments - is that a require() route needs checking independently of
// import(), since they go through different code paths here).
func TestHTTP2RequireWorks(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := RunCJS(p, `
		const { constants } = require("node:http2");
		module.exports = constants.HTTP2_HEADER_METHOD;
	`, "/virtual/test.js")
	if len(errs) > 0 {
		t.Fatalf("RunCJS: %v", errs[0])
	}
	if val.ToString() != ":method" {
		t.Errorf("got %q, want %q", val.ToString(), ":method")
	}
}

// TestHTTP2UnimplementedThrowsClearly guards this being an honest gap
// (a clear thrown error naming what's missing) rather than a silent
// no-op that would hide the fact that connect()/createServer()/etc.
// don't actually do anything yet.
func TestHTTP2UnimplementedThrowsClearly(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { connect } from "node:http2";
		let message = "";
		try {
			connect("https://example.com");
		} catch (e) {
			message = e.message;
		}
		message
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() == "" {
		t.Error("expected connect() to throw a clear 'not implemented' error, got no exception at all")
	}
}

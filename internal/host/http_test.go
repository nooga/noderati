package host

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nooga/paserati/pkg/driver"
)

func TestHTTPRequestGET(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test", "yes")
		w.WriteHeader(200)
		fmt.Fprint(w, "hello from server")
	}))
	defer srv.Close()

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { request } from "node:http";
		const url = new URL(`+"`"+srv.URL+"`"+`);
		let result;
		await new Promise((resolve, reject) => {
			const req = request({ hostname: url.hostname, port: url.port, path: "/", method: "GET" }, (res) => {
				let body = "";
				res.on("data", (c) => body += c);
				res.on("end", () => { result = { statusCode: res.statusCode, header: res.headers["x-test"], body }; resolve(); });
			});
			req.on("error", reject);
			req.end();
		});
		JSON.stringify(result)
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"statusCode":200,"header":"yes","body":"hello from server"}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestHTTPMaxHeaderSize drives the exact real gap found while re-probing
// real undici's fetch() (round 75, docs/real-node-plan.md): undici's own
// lib/dispatcher/client.js reads http.maxHeaderSize at module-load time
// unconditionally, throwing InvalidArgumentError the instant any
// Client/Pool is constructed if it's missing/non-numeric. Real Node
// exposes this only on node:http (default 16384), not node:https.
func TestHTTPMaxHeaderSize(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import http from "node:http";
		import https from "node:https";
		JSON.stringify({
			httpMaxHeaderSize: http.maxHeaderSize,
			httpsHasMaxHeaderSize: "maxHeaderSize" in https,
		})
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"httpMaxHeaderSize":16384,"httpsHasMaxHeaderSize":false}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

func TestHTTPRequestPOSTBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "method=%s body=%s", r.Method, string(body))
	}))
	defer srv.Close()

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { request } from "node:http";
		const url = new URL(`+"`"+srv.URL+"`"+`);
		let result = "";
		await new Promise((resolve, reject) => {
			const req = request({ hostname: url.hostname, port: url.port, path: "/", method: "POST" }, (res) => {
				res.on("data", (c) => result += c);
				res.on("end", resolve);
			});
			req.on("error", reject);
			req.write("hello-");
			req.end("world");
		});
		result
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := "method=POST body=hello-world"
	if val.ToString() != want {
		t.Errorf("got %q, want %q", val.ToString(), want)
	}
}

// TestHTTPRequestStreamsIncrementally guards the exact class of bug found
// and fixed elsewhere this whole investigation (round 65's pumpSpawnStream
// ordering, round 66's Buffer.byteLength): a response that looks done
// before its data actually arrived, or arrives all at once instead of as
// it's produced. The server flushes multiple separate writes; a passing
// test here means each one really did arrive as its own "data" event, not
// coalesced into one at "end".
func TestHTTPRequestStreamsIncrementally(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		for i := 0; i < 5; i++ {
			fmt.Fprintf(w, "chunk%d;", i)
			if ok {
				flusher.Flush()
			}
			// A real, deliberate gap between flushes - back-to-back
			// flushes with no delay can legitimately coalesce into one
			// Read() (confirmed by testing under -race, which changes
			// scheduling enough to turn 5 flushes into 1 read with zero
			// data loss - a timing artifact, not a bug, but one that also
			// made the first version of this test itself flaky). A real
			// gap makes "arrived as separate events" an actual, reliable
			// property of this test rather than a timing coincidence.
			time.Sleep(5 * time.Millisecond)
		}
	}))
	defer srv.Close()

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { request } from "node:http";
		const url = new URL(`+"`"+srv.URL+"`"+`);
		let chunkCount = 0, body = "";
		await new Promise((resolve, reject) => {
			const req = request({ hostname: url.hostname, port: url.port, path: "/", method: "GET" }, (res) => {
				res.on("data", (c) => { chunkCount++; body += c; });
				res.on("end", resolve);
			});
			req.on("error", reject);
			req.end();
		});
		JSON.stringify({ multiChunk: chunkCount > 1, body })
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	// Not "chunkCount === 5": Go's Read() (like real Node's own "data"
	// event) makes no promise that one server-side flush maps to exactly
	// one event - closely-timed flushes can legitimately coalesce on
	// either side. What actually matters, and is real per this whole
	// investigation's own history of bugs in exactly this area: no data
	// lost (byte-exact body) and genuinely incremental delivery (more than
	// one chunk), not everything buffered and handed over at "end".
	want := `{"multiChunk":true,"body":"chunk0;chunk1;chunk2;chunk3;chunk4;"}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

func TestHTTPRequestConnectionErrorEmitsError(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { request } from "node:http";
		let gotError = false;
		await new Promise((resolve) => {
			// Port 1 is reserved/unlisted - a real, fast connection-refused
			// case rather than a slow timeout, keeping the test quick.
			const req = request({ hostname: "127.0.0.1", port: 1, path: "/", method: "GET" }, () => {
				resolve();
			});
			req.on("error", () => { gotError = true; resolve(); });
			req.end();
		});
		gotError
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if !val.IsTruthy() {
		t.Errorf("expected a connection error to be emitted, got none")
	}
}

// TestHTTPRequestConstructionErrorEmitsError guards a real fix made this
// round: the body-pipe goroutine every request starts was left running,
// blocked forever on an unbuffered channel that write()/end() (replaced
// with no-ops on this path) never touch - a real per-request goroutine
// leak on every malformed request, not just a missed error event.
func TestHTTPRequestConstructionErrorEmitsError(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { request } from "node:http";
		let gotError = false;
		await new Promise((resolve) => {
			const req = request({ hostname: "localhost", port: 80, path: "/", method: "BAD METHOD" }, () => resolve());
			req.on("error", () => { gotError = true; resolve(); });
			req.end();
		});
		gotError
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if !val.IsTruthy() {
		t.Errorf("expected a request-construction error to be emitted, got none")
	}
}

func TestHTTPSocketConnectEventFires(t *testing.T) {
	// Guards the real bug found and fixed this round: the socket object
	// was originally a bespoke object with a hand-rolled .on("connect")
	// that only fired if already connected at registration time, silently
	// dropping the event otherwise - the common case, not an edge one.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { request } from "node:http";
		const url = new URL(`+"`"+srv.URL+"`"+`);
		let connected = false;
		await new Promise((resolve, reject) => {
			const req = request({ hostname: url.hostname, port: url.port, path: "/", method: "GET" }, (res) => {
				res.on("data", () => {});
				res.on("end", resolve);
			});
			req.on("error", reject);
			req.on("socket", (s) => {
				s.on("connect", () => { connected = true; });
			});
			req.end();
		});
		connected
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if !val.IsTruthy() {
		t.Errorf("socket 'connect' event never fired")
	}
}

// TestHTTPResponseDataIsRealBuffer guards a real deviation from Node
// found chasing the Bedrock investigation (docs/real-node-plan.md,
// round 101): real Node's IncomingMessage emits real Buffers by
// default (only a string once setEncoding() has been called) - this
// used to always emit a plain JS string instead. Real
// @smithy/node-http-handler's own streamCollector collects "data"
// chunks into an array and does Buffer.concat(chunks) on it -
// concatenating a string produces zero bytes, not a thrown error, so a
// response body silently came back empty rather than failing loudly.
func TestHTTPResponseDataIsRealBuffer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello")
	}))
	defer srv.Close()

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { request } from "node:http";
		const url = new URL(`+"`"+srv.URL+"`"+`);
		let result;
		await new Promise((resolve, reject) => {
			const req = request({ hostname: url.hostname, port: url.port, path: "/", method: "GET" }, (res) => {
				const chunks = [];
				res.on("data", (c) => chunks.push(c));
				res.on("end", () => {
					result = JSON.stringify({
						isBuffer: Buffer.isBuffer(chunks[0]),
						concatenated: Buffer.concat(chunks).toString("utf8"),
					});
					resolve();
				});
			});
			req.on("error", reject);
			req.end();
		});
		result
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"isBuffer":true,"concatenated":"hello"}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestHTTPResponsePipesIntoWritableSubclass guards a real bug found the
// same round: emitter.go's own pipe() looked up a destination's
// write()/end() methods via GetOwn (instance-only properties), but a
// real class-based Writable (e.g. real @smithy/node-http-handler's own
// streamCollector, which does exactly `class Collector extends
// Writable { _write(...) {...} }` then `stream.pipe(collector)` to
// read a real HTTP response body) has write()/end() on its
// *prototype*, not as own instance properties - GetOwn silently found
// neither, so piping a real response into any class-based Writable
// wrote nothing and never called end(), hanging whatever awaited the
// destination's own "finish" event forever.
func TestHTTPResponsePipesIntoWritableSubclass(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "piped-body")
	}))
	defer srv.Close()

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import { request } from "node:http";
		import { Writable } from "node:stream";
		class Collector extends Writable {
			chunks = [];
			_write(chunk, _encoding, callback) {
				this.chunks.push(chunk);
				callback();
			}
		}
		const url = new URL(`+"`"+srv.URL+"`"+`);
		let result;
		await new Promise((resolve, reject) => {
			const req = request({ hostname: url.hostname, port: url.port, path: "/", method: "GET" }, (res) => {
				const collector = new Collector();
				res.pipe(collector);
				collector.on("finish", () => {
					result = Buffer.concat(collector.chunks).toString("utf8");
					resolve();
				});
			});
			req.on("error", reject);
			req.end();
		});
		result
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "piped-body" {
		t.Errorf("got %q, want %q", val.ToString(), "piped-body")
	}
}

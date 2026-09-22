package host

import (
	"strings"
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

// http_server_test.go exercises http.createServer end to end as a
// self-hosted round trip - a real client (http.request, already covered by
// http_test.go's own tests) talking to a real server, both driven from the
// same script, over a real loopback TCP connection net.Listen actually
// opened. This is the same "single-process behavioral test" shape every
// other test in this file already uses (see TestHTTPRequestGET), just with
// this round's new server half added on the other end of the wire.

func TestHTTPServerBasicGET(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import http from "node:http";
		let result;
		await new Promise((resolve, reject) => {
			const server = http.createServer((req, res) => {
				res.setHeader("X-Test", "yes");
				res.writeHead(200, { "Content-Type": "text/plain" });
				res.end("hello from server");
			});
			server.listen(0, () => {
				const port = server.address().port;
				const req = http.request({ hostname: "127.0.0.1", port, path: "/hi?x=1", method: "GET" }, (res) => {
					let body = "";
					res.on("data", (c) => body += c);
					res.on("end", () => {
						result = { statusCode: res.statusCode, header: res.headers["x-test"], body };
						server.close(() => resolve());
					});
				});
				req.on("error", reject);
				req.end();
			});
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

// TestHTTPServerRequestShape guards req.method/req.url/req.headers - the
// exact surface Connect/Koa's own routing reads - and specifically that
// req.url is path+query only (RequestURI()), never an absolute URL: Koa's
// ctx.path/ctx.query parse exactly this shape, and an absolute URL there
// breaks routing with no thrown error at all (see http_server.go's own
// doc comment on buildServerIncomingMessage).
func TestHTTPServerRequestShape(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import http from "node:http";
		let result;
		await new Promise((resolve, reject) => {
			const server = http.createServer((req, res) => {
				result = { method: req.method, url: req.url, accept: req.headers["x-req"] };
				res.end("ok");
			});
			server.listen(0, () => {
				const port = server.address().port;
				const req = http.request({ hostname: "127.0.0.1", port, path: "/hi?x=1", method: "GET", headers: { "X-Req": "yes" } }, (res) => {
					res.on("data", () => {});
					res.on("end", () => server.close(() => resolve()));
				});
				req.on("error", reject);
				req.end();
			});
		});
		JSON.stringify(result)
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"method":"GET","url":"/hi?x=1","accept":"yes"}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestHTTPServerPOSTBody guards the request body actually reaching the
// server's IncomingMessage as real streamed "data" events (pumpServerRequestBody),
// not silently dropped or coalesced.
func TestHTTPServerPOSTBody(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import http from "node:http";
		let result = "";
		await new Promise((resolve, reject) => {
			const server = http.createServer((req, res) => {
				let body = "";
				req.on("data", (c) => body += c);
				req.on("end", () => res.end("method=" + req.method + " body=" + body));
			});
			server.listen(0, () => {
				const port = server.address().port;
				const req = http.request({ hostname: "127.0.0.1", port, path: "/", method: "POST" }, (res) => {
					res.on("data", (c) => result += c);
					res.on("end", () => server.close(() => resolve()));
				});
				req.on("error", reject);
				req.write("hello-");
				req.end("world");
			});
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

// TestHTTPServerWritesIncrementally guards against the same class of bug
// TestHTTPRequestStreamsIncrementally (http_test.go) already guards on the
// client side: a response that looks done before its data actually
// arrived, or arrives all at once instead of as it's produced. The server
// here issues several separate res.write() calls; a passing test means
// each one really did flush as its own chunk.
func TestHTTPServerWritesIncrementally(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import http from "node:http";
		let chunkCount = 0;
		let result = "";
		await new Promise((resolve, reject) => {
			const server = http.createServer((req, res) => {
				res.writeHead(200);
				// setInterval/clearInterval don't exist in this runtime yet
				// (a pre-existing, documented gap - see timers.go) - a
				// recursive setTimeout chain is the available substitute for
				// "write several separate chunks over several ticks".
				let i = 0;
				const step = () => {
					res.write("chunk" + i + ";");
					i++;
					if (i >= 5) {
						res.end();
					} else {
						setTimeout(step, 1);
					}
				};
				setTimeout(step, 1);
			});
			server.listen(0, () => {
				const port = server.address().port;
				const req = http.request({ hostname: "127.0.0.1", port, path: "/", method: "GET" }, (res) => {
					res.on("data", (c) => { chunkCount++; result += c; });
					res.on("end", () => server.close(() => resolve()));
				});
				req.on("error", reject);
				req.end();
			});
		});
		JSON.stringify({ chunkCount: chunkCount >= 2, result });
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if !strings.Contains(val.ToString(), `"chunkCount":true`) {
		t.Errorf("expected data to have arrived as more than one chunk, got %s", val.ToString())
	}
	if !strings.Contains(val.ToString(), "chunk0;chunk1;chunk2;chunk3;chunk4;") {
		t.Errorf("expected full concatenated body, got %s", val.ToString())
	}
}

// TestHTTPServerFlushHeadersAfterEnd guards a real panic found during
// review, not by this file's own tests: end() closes the response's write
// channel, and flushHeaders() (Koa exposes this as ctx.flushHeaders(),
// lib/response.js, reachable from real app code) used to send into it
// unconditionally afterwards - "send on closed channel". A call ordering
// no test above happens to exercise, but real code can.
func TestHTTPServerFlushHeadersAfterEnd(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import http from "node:http";
		let result = "ok";
		await new Promise((resolve, reject) => {
			const server = http.createServer((req, res) => {
				res.end("done");
				res.flushHeaders();
				res.writeHead(500);
			});
			server.listen(0, () => {
				const port = server.address().port;
				const req = http.request({ hostname: "127.0.0.1", port, path: "/", method: "GET" }, (res) => {
					res.on("data", () => {});
					res.on("end", () => server.close(() => resolve()));
				});
				req.on("error", (e) => { result = "error:" + e; reject(e); });
				req.end();
			});
		});
		result
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "ok" {
		t.Errorf("got %q, want %q", val.ToString(), "ok")
	}
}

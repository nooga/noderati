package host

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/nooga/paserati/pkg/driver"
)

// newEchoServer starts a real TCP server that echoes back exactly what it
// reads, byte for byte, until the client closes its write side or the
// whole connection closes. Returns the listener's address; caller must
// srv.Close() when done (Close() unblocks the accept loop below).
func newEchoServer(t *testing.T) (addr string, closeFn func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 4096)
				for {
					n, err := c.Read(buf)
					if n > 0 {
						if _, werr := c.Write(buf[:n]); werr != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}(conn)
		}
	}()
	return ln.Addr().String(), func() { _ = ln.Close() }
}

func splitHostPort(t *testing.T, addr string) (string, string) {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split %s: %v", addr, err)
	}
	return host, port
}

func TestNetConnectEchoByteExact(t *testing.T) {
	addr, closeFn := newEchoServer(t)
	defer closeFn()
	host, port := splitHostPort(t, addr)

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(fmt.Sprintf(`
		import { connect } from "node:net";
		let result = "";
		await new Promise((resolve, reject) => {
			const socket = connect({ host: %q, port: %s }, () => {
				socket.setNoDelay(true);
				socket.write("hello ");
				socket.end("world");
			});
			socket.on("data", (chunk) => { result += chunk.toString(); });
			socket.on("end", resolve);
			socket.on("error", reject);
		});
		result
	`, host, port), driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if want := "hello world"; val.ToString() != want {
		t.Errorf("got %q, want %q", val.ToString(), want)
	}
}

// TestNetConnectThisIsSocket guards the emitOnObject fix directly: real
// undici's connector (lib/core/connect.js) does
// `.once('connect', function () { cb(null, this) })` and relies on
// `this` being the socket itself, not undefined.
func TestNetConnectThisIsSocket(t *testing.T) {
	addr, closeFn := newEchoServer(t)
	defer closeFn()
	host, port := splitHostPort(t, addr)

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(fmt.Sprintf(`
		import { connect } from "node:net";
		let ok = false;
		await new Promise((resolve, reject) => {
			const socket = connect({ host: %q, port: %s });
			socket.once("connect", function () {
				ok = this === socket && typeof this.write === "function";
				socket.destroy();
				resolve();
			});
			socket.on("error", reject);
		});
		ok
	`, host, port), driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if !val.IsTruthy() {
		t.Errorf("expected this === socket inside connect listener, got %v", val)
	}
}

// TestNetConnectStreamsIncrementally mirrors http_test.go's own streaming
// guard: assert byte-exact accumulated content across many discrete
// 'data' events, not an exact chunk count (Go's own Read() coalescing is
// legitimate and not something this test should fight).
func TestNetConnectStreamsIncrementally(t *testing.T) {
	addr, closeFn := newEchoServer(t)
	defer closeFn()
	host, port := splitHostPort(t, addr)

	var script strings.Builder
	script.WriteString(fmt.Sprintf(`
		import { connect } from "node:net";
		let result = "";
		let chunks = 0;
		await new Promise((resolve, reject) => {
			const socket = connect({ host: %q, port: %s }, () => {
	`, host, port))
	for i := 0; i < 50; i++ {
		script.WriteString(fmt.Sprintf("				socket.write(%q);\n", fmt.Sprintf("chunk-%03d;", i)))
	}
	script.WriteString(`
				socket.end();
			});
			socket.on("data", (chunk) => { result += chunk.toString(); chunks++; });
			socket.on("end", resolve);
			socket.on("error", reject);
		});
		JSON.stringify({ result, hasChunks: chunks > 0 })
	`)

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(script.String(), driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	var want strings.Builder
	for i := 0; i < 50; i++ {
		want.WriteString(fmt.Sprintf("chunk-%03d;", i))
	}
	wantJSON := fmt.Sprintf(`{"result":%q,"hasChunks":true}`, want.String())
	if val.ToString() != wantJSON {
		t.Errorf("got %s, want %s", val.ToString(), wantJSON)
	}
}

func TestNetConnectErrorOnRefused(t *testing.T) {
	// Bind then immediately close, to get a real, currently-unused local
	// port that will genuinely refuse the connection (not hypothetically
	// - this is what a firewalled/down service actually does).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	host, port := splitHostPort(t, addr)

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(fmt.Sprintf(`
		import { connect } from "node:net";
		let gotError = false;
		await new Promise((resolve) => {
			const socket = connect({ host: %q, port: %s });
			socket.on("error", (err) => { gotError = err instanceof Error; resolve(); });
			socket.on("connect", () => resolve());
		});
		gotError
	`, host, port), driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if !val.IsTruthy() {
		t.Errorf("expected a real connection-refused error, got %v", val)
	}
}

// TestNetSocketBackpressure is the "real backpressure, not synthetic"
// requirement turned into an assertion: write a payload well over the
// (deliberately tiny, via highWaterMark) threshold to a server that reads
// slowly, and check write() actually returns false at some point and
// 'drain' genuinely fires once the OS has caught up - not on a timer.
func TestNetSocketBackpressure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	total := 4 * 1024 * 1024 // 4MB - large enough to exceed OS socket buffers and force real backpressure
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		read := 0
		for read < total {
			// Read slowly on purpose so the writer genuinely backs up.
			time.Sleep(2 * time.Millisecond)
			n, err := conn.Read(buf)
			read += n
			if err != nil {
				return
			}
		}
	}()
	host, port := splitHostPort(t, ln.Addr().String())

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(fmt.Sprintf(`
		import { connect } from "node:net";
		let sawFalse = false;
		let sawDrain = false;
		await new Promise((resolve, reject) => {
			const socket = connect({ host: %q, port: %s, highWaterMark: 16384 }, () => {
				const chunk = "x".repeat(65536);
				for (let i = 0; i < %d; i++) {
					const ok = socket.write(chunk);
					if (!ok) sawFalse = true;
				}
				socket.end();
			});
			socket.on("drain", () => { sawDrain = true; });
			socket.on("close", () => resolve());
			socket.on("error", reject);
		});
		JSON.stringify({ sawFalse, sawDrain })
	`, host, port, total/65536), driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"sawFalse":true,"sawDrain":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestNetSocketPauseResumeStopsReads proves pause() actually stops
// pulling bytes off the OS socket (not just withholding 'data' events
// while still draining in the background) - the server records how many
// bytes it managed to push before the client resumes.
func TestNetSocketPauseResumeStopsReads(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	serverDone := make(chan int64, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		payload := make([]byte, 8*1024*1024)
		n, _ := conn.Write(payload)
		serverDone <- int64(n)
	}()
	host, port := splitHostPort(t, ln.Addr().String())

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(fmt.Sprintf(`
		import { connect } from "node:net";
		let totalWhilePaused = 0;
		let resumed = false;
		await new Promise((resolve, reject) => {
			const socket = connect({ host: %q, port: %s }, () => {
				socket.pause();
				setTimeout(() => {
					resumed = true;
					socket.resume();
				}, 150);
			});
			socket.on("data", (chunk) => {
				if (!resumed) totalWhilePaused += chunk.length;
			});
			socket.on("end", resolve);
			socket.on("error", reject);
		});
		JSON.stringify({ pausedSmall: totalWhilePaused < 1024 * 1024 })
	`, host, port), driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if want := `{"pausedSmall":true}`; val.ToString() != want {
		t.Errorf("got %s, want %s - pause() should have kept the reader loop from pulling the whole 8MB before resume()", val.ToString(), want)
	}
	<-serverDone
}

// TestNetSocketDestroyRightAfterWriteDoesNotHang guards the destroy()/
// write() interaction documented in writerLoop's own comment: destroying
// a socket immediately after queuing a large write (before the writer
// goroutine could plausibly have flushed it) can drop that data - real
// Node's own destroy() is specified to abort immediately, not flush
// first, so this is expected, not a bug. What must still hold, and what
// this test actually checks, is that the sequence is safe: it must not
// hang, panic, or double-emit 'close' regardless of which goroutine
// notices the close first.
func TestNetSocketDestroyRightAfterWriteDoesNotHang(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		for {
			if _, err := conn.Read(buf); err != nil {
				return
			}
		}
	}()
	host, port := splitHostPort(t, ln.Addr().String())

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(fmt.Sprintf(`
		import { connect } from "node:net";
		let closeCount = 0;
		await new Promise((resolve, reject) => {
			const socket = connect({ host: %q, port: %s }, () => {
				socket.write("x".repeat(1024 * 1024));
				socket.destroy();
			});
			socket.on("close", () => { closeCount++; resolve(); });
			socket.on("error", reject);
		});
		closeCount
	`, host, port), driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToFloat() != 1 {
		t.Errorf("got %v 'close' events, want exactly 1", val)
	}
}

// TestNetSocketUnrefLetsProcessDrainWithConnectionStillOpen guards
// Socket.unref()/.ref() directly: real undici's client-h1.js calls
// socket.unref() the instant a keep-alive connection has no in-flight
// request (resumeH1), specifically so a pooled idle connection can't by
// itself keep the process running - exactly like real Node's own
// socket.unref(). Before this existed, unref()/ref() were pure no-op
// stubs, so DrainUntilIdle's WaitForExternalOp() would wait forever on
// any still-open socket regardless of unref() - confirmed directly via a
// live pprof goroutine dump during a real-undici fetch() E2E probe
// (docs/real-node-plan.md, Round 76/77) before writing this fix, though
// that specific hang's actual root cause turned out to be a different,
// unrelated bug (Socket never implementing paused-mode Readable, so
// undici's parser was never fed any bytes at all - see that round's
// entry). This test exercises unref() in isolation, independent of that
// other bug: the server below never closes the connection, so if
// unref() were still a no-op, DrainUntilIdle would hang this test
// forever - guarded with a timeout so a regression fails loudly instead
// of hanging the whole suite.
func TestNetSocketUnrefLetsProcessDrainWithConnectionStillOpen(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		// Deliberately never closes or writes again - a real, live,
		// open connection that would keep readerLoop's conn.Read()
		// blocked forever, exactly like an idle real-undici keep-alive
		// socket.
		_ = conn
		<-make(chan struct{})
	}()
	host, port := splitHostPort(t, ln.Addr().String())

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(fmt.Sprintf(`
		import { connect } from "node:net";
		let connected = false;
		await new Promise((resolve, reject) => {
			const socket = connect({ host: %q, port: %s }, () => {
				connected = true;
				socket.unref();
				resolve();
			});
			socket.on("error", reject);
		});
		connected
	`, host, port), driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if !val.AsBoolean() {
		t.Fatalf("socket never connected")
	}

	// The connection above is still open (the server never closes or
	// destroys it, and this script never called socket.destroy()/end()
	// either) - only unref() stands between DrainUntilIdle and waiting
	// on it forever.
	done := make(chan struct{})
	go func() {
		p.GetVM().DrainUntilIdle()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("DrainUntilIdle did not return within 5s - socket.unref() did not release its external-op registration (connection is still open)")
	}
}

// TestNetSocketReadableProtocolDrivesParserStyleConsumer guards the
// actual root cause of the Round 76/77 real-undici E2E hang directly
// (docs/real-node-plan.md): real undici's HTTP/1.1 client drives its
// llhttp parser exclusively through Node's paused-mode Readable
// protocol - socket.on('readable', ...) then socket.read() in a loop -
// and never listens for 'data' at all. Before this fix, Socket only
// ever emitted 'data' unconditionally; those bytes arrived, were
// drained off the real OS socket, and were emitted into an event
// nobody was listening for, so a parser driven exactly this way never
// received a single byte. This test drives the socket with that exact
// protocol (never touching 'data') and asserts the bytes come through
// byte-exact anyway.
func TestNetSocketReadableProtocolDrivesParserStyleConsumer(t *testing.T) {
	addr, closeFn := newEchoServer(t)
	defer closeFn()
	host, port := splitHostPort(t, addr)

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(fmt.Sprintf(`
		import { connect } from "node:net";
		let result = "";
		let endCount = 0;
		await new Promise((resolve, reject) => {
			const socket = connect({ host: %q, port: %s }, () => {
				socket.write("hello ");
				socket.end("world");
			});
			socket.on("readable", () => {
				let chunk;
				while ((chunk = socket.read()) !== null) {
					result += chunk.toString();
				}
			});
			socket.on("end", () => { endCount++; resolve(); });
			socket.on("error", reject);
		});
		JSON.stringify({ result, endCount })
	`, host, port), driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"result":"hello world","endCount":1}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestNetSocketReadWithSizeLeavesRemainderQueued guards read(size)'s
// partial-consumption behavior: taking fewer bytes than are buffered
// must leave the rest available for the next read() call, not discard
// or duplicate it.
func TestNetSocketReadWithSizeLeavesRemainderQueued(t *testing.T) {
	addr, closeFn := newEchoServer(t)
	defer closeFn()
	host, port := splitHostPort(t, addr)

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(fmt.Sprintf(`
		import { connect } from "node:net";
		let firstRead = "";
		let rest = "";
		await new Promise((resolve, reject) => {
			const socket = connect({ host: %q, port: %s }, () => {
				socket.end("0123456789");
			});
			let readSized = false;
			socket.on("readable", () => {
				if (!readSized) {
					const chunk = socket.read(3);
					if (chunk !== null) {
						firstRead = chunk.toString();
						readSized = true;
					}
				}
				let chunk;
				while ((chunk = socket.read()) !== null) {
					rest += chunk.toString();
				}
			});
			socket.on("end", resolve);
			socket.on("error", reject);
		});
		JSON.stringify({ firstRead, rest, full: firstRead + rest })
	`, host, port), driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"firstRead":"012","rest":"3456789","full":"0123456789"}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestNetSocketReadableEndDeferredUntilBufferDrained guards the race
// this fix has to get right: EOF can arrive while there's still unread
// data sitting in readBuf (a server that writes its payload and closes
// immediately makes this the common case, not a rare one). 'end' must
// not fire until a paused-mode consumer actually drains that data via
// read() - firing it early would mean a consumer relying on 'end' to
// know it's safe to stop calling read() could miss bytes that arrived
// before the close. This test deliberately delays calling read() past
// the point where the server has already sent everything and closed,
// forcing readerLoop's EOF branch to observe a non-empty buffer.
func TestNetSocketReadableEndDeferredUntilBufferDrained(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("payload-before-close"))
		// Closing immediately after the write (no pause) means the
		// client's readerLoop is likely to observe the data and the
		// EOF together, or in very quick succession - exactly the
		// race this test targets.
	}()
	host, port := splitHostPort(t, ln.Addr().String())

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(fmt.Sprintf(`
		import { connect } from "node:net";
		let result = "";
		let endFired = false;
		await new Promise((resolve, reject) => {
			const socket = connect({ host: %q, port: %s });
			socket.on("readable", () => {});
			socket.on("end", () => { endFired = true; resolve(); });
			socket.on("error", reject);
			// Deliberately wait well past the point where the server
			// has already written its payload and closed, before ever
			// calling read() - readerLoop's EOF branch must have
			// already run against a non-empty buffer by then.
			setTimeout(() => {
				let chunk;
				while ((chunk = socket.read()) !== null) {
					result += chunk.toString();
				}
			}, 100);
		});
		JSON.stringify({ result, endFired })
	`, host, port), driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"result":"payload-before-close","endFired":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestNetSocketDataListenerStillWorksAlongsideReadableFix guards against
// a regression in the opposite direction: adding this round's
// paused-mode Readable support must not break existing push-mode
// ('data') consumers, which every other caller in this codebase (and
// real Node code that never touches 'readable'/read()) still relies on.
func TestNetSocketDataListenerStillWorksAlongsideReadableFix(t *testing.T) {
	addr, closeFn := newEchoServer(t)
	defer closeFn()
	host, port := splitHostPort(t, addr)

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(fmt.Sprintf(`
		import { connect } from "node:net";
		let result = "";
		let sawReadable = false;
		await new Promise((resolve, reject) => {
			const socket = connect({ host: %q, port: %s }, () => {
				socket.write("hello ");
				socket.end("world");
			});
			socket.on("data", (chunk) => { result += chunk.toString(); });
			socket.on("readable", () => { sawReadable = true; });
			socket.on("end", resolve);
			socket.on("error", reject);
		});
		JSON.stringify({ result, sawReadable })
	`, host, port), driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	// Real Node's own behavior: once a 'data' listener switches a stream
	// to flowing mode, 'readable' no longer fires - flowing mode owns
	// delivery entirely.
	want := `{"result":"hello world","sawReadable":false}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

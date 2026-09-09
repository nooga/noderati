package host

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

// compressGzip/compressZlib/compressFlate build real, standard-library
// compressed payloads to feed into the decompressor under test - the
// oracle here is Go's own stdlib compressor, matching real Node's own
// zlib bindings' wire format exactly (gzip/zlib/deflate are standard
// formats, not host-specific).
func compressGzip(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func compressZlib(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatalf("zlib write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zlib close: %v", err)
	}
	return buf.Bytes()
}

func compressFlateRaw(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		t.Fatalf("flate.NewWriter: %v", err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatalf("flate write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("flate close: %v", err)
	}
	return buf.Bytes()
}

// bytesToJSStringLiteral encodes arbitrary bytes as a JS string literal
// using \xHH-style escapes for every byte, so the compressed payload
// (which is binary, not valid UTF-8 in general) survives embedding
// directly into a test script unchanged.
func bytesToJSStringLiteral(data []byte) string {
	var sb bytes.Buffer
	sb.WriteByte('"')
	for _, b := range data {
		fmt.Fprintf(&sb, "\\x%02x", b)
	}
	sb.WriteByte('"')
	return sb.String()
}

func runDecompressTest(t *testing.T, ctorName string, compressed []byte, want string) {
	t.Helper()
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	script := fmt.Sprintf(`
		import zlib from "node:zlib";
		let result = "";
		await new Promise((resolve, reject) => {
			const d = zlib.%s();
			d.on("data", (chunk) => { result += chunk.toString(); });
			d.on("end", resolve);
			d.on("error", reject);
			d.write(%s);
			d.end();
		});
		result
	`, ctorName, bytesToJSStringLiteral(compressed))
	val, errs := p.RunCode(script, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != want {
		t.Errorf("got %q, want %q", val.ToString(), want)
	}
}

func TestZlibCreateGunzipDecompressesRealGzip(t *testing.T) {
	payload := []byte("hello gzip world, decompressed byte-exact")
	runDecompressTest(t, "createGunzip", compressGzip(t, payload), string(payload))
}

func TestZlibCreateInflateDecompressesRealZlib(t *testing.T) {
	payload := []byte("hello zlib-format world")
	runDecompressTest(t, "createInflate", compressZlib(t, payload), string(payload))
}

func TestZlibCreateInflateRawDecompressesRealRawDeflate(t *testing.T) {
	payload := []byte("hello raw deflate world")
	runDecompressTest(t, "createInflateRaw", compressFlateRaw(t, payload), string(payload))
}

// TestZlibDecompressStreamsIncrementally mirrors this codebase's own
// standing streaming guard (http_test.go/net_test.go): write the
// compressed payload in many small chunks and assert the accumulated
// decompressed output is byte-exact - not that it arrived in any
// particular number of 'data' events.
func TestZlibDecompressStreamsIncrementally(t *testing.T) {
	var payload bytes.Buffer
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&payload, "line-%03d;", i)
	}
	compressed := compressGzip(t, payload.Bytes())

	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	// Split the compressed bytes into several write() calls to actually
	// exercise incremental feeding, not just a single write()+end().
	half := len(compressed) / 2
	script := fmt.Sprintf(`
		import zlib from "node:zlib";
		let result = "";
		await new Promise((resolve, reject) => {
			const d = zlib.createGunzip();
			d.on("data", (chunk) => { result += chunk.toString(); });
			d.on("end", resolve);
			d.on("error", reject);
			d.write(%s);
			d.write(%s);
			d.end();
		});
		result
	`, bytesToJSStringLiteral(compressed[:half]), bytesToJSStringLiteral(compressed[half:]))
	val, errs := p.RunCode(script, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != payload.String() {
		t.Errorf("got %q, want %q", val.ToString(), payload.String())
	}
}

func TestZlibCreateGunzipEmitsErrorOnCorruptData(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import zlib from "node:zlib";
		let gotError = false;
		await new Promise((resolve) => {
			const d = zlib.createGunzip();
			d.on("error", (err) => { gotError = err instanceof Error; resolve(); });
			d.on("end", () => resolve());
			d.write("this is not gzip data at all");
			d.end();
		});
		gotError
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if !val.IsTruthy() {
		t.Errorf("expected a real decompression error for corrupt input, got %v", val)
	}
}

func TestZlibBrotliAndZstdThrowRealError(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		import zlib from "node:zlib";
		let brotliThrew = false, zstdThrew = false;
		try { zlib.createBrotliDecompress(); } catch (e) { brotliThrew = e instanceof Error; }
		try { zlib.createZstdDecompress(); } catch (e) { zstdThrew = e instanceof Error; }
		JSON.stringify({ brotliThrew, zstdThrew })
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"brotliThrew":true,"zstdThrew":true}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

package host

import (
	"testing"

	"github.com/nooga/paserati/pkg/driver"
)

// TestFileGlobalIsRealBlobSubclass drives the exact real requirement
// found while probing undici: real undici's lib/web/webidl/index.js
// does `webidl.is.File = webidl.util.MakeTypeAssertion(File)` at its own
// module top level, so `File` must exist as a real global constructor,
// not just be reachable through some import. Also checks it's a genuine
// Blob subclass - real File instances must still work everywhere a Blob
// does (`.size`, `.slice()`, `instanceof Blob`), not a parallel, unrelated
// stand-in class.
func TestFileGlobalIsRealBlobSubclass(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		const f = new File(["hello"], "test.txt", { type: "text/plain", lastModified: 12345 });
		JSON.stringify({
			isBlob: f instanceof Blob,
			isFile: f instanceof File,
			name: f.name,
			size: f.size,
			type: f.type,
			lastModified: f.lastModified,
			tag: Object.prototype.toString.call(f),
		})
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	want := `{"isBlob":true,"isFile":true,"name":"test.txt","size":5,"type":"text/plain","lastModified":12345,"tag":"[object File]"}`
	if val.ToString() != want {
		t.Errorf("got %s, want %s", val.ToString(), want)
	}
}

// TestFileGlobalInheritsRealBlobMethods checks a real Blob method
// (.text()) still works on a File instance - since File is meant to be
// a thin subclass, not a reimplementation, this must come from Blob's
// own real implementation, not something File.go re-does.
func TestFileGlobalInheritsRealBlobMethods(t *testing.T) {
	p := New([]string{"noderati"})
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(`
		const f = new File(["hello world"], "test.txt");
		await f.text()
	`, driver.RunOptions{})
	if len(errs) > 0 {
		t.Fatalf("RunCode: %v", errs[0])
	}
	if val.ToString() != "hello world" {
		t.Errorf("got %q, want %q", val.ToString(), "hello world")
	}
}

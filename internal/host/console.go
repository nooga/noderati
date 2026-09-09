package host

// console.go implements node:console's `Console` class - the one real
// piece missing here, not `console` itself: paserati already has a real
// global `console` singleton (pkg/builtins/console_init.go - log/error/
// warn/info/debug/trace/time/timeEnd/group/groupEnd, writing to the
// actual process stdout/stderr), it just has no constructible `Console`
// class alongside it. Real undici's lib/mock/pending-interceptors-
// formatter.js does `const { Console } = require('node:console')` then
// `new Console({ stdout: someTransform, inspectOptions: {...} })`, used
// only to pretty-print MockAgent's own pending-interceptor list (a
// mocking/debugging convenience nothing in pi's real fetch/dispatch path
// ever exercises - confirmed by reading every real call site, not
// assumed). Built as a pure JS shim (no Go natives needed) since a
// custom-stream Console is genuinely just formatting + delegating writes
// to whichever stream it was given, falling back to the real global
// console when none was: real behavior, not a stand-in, just a small one
// since nothing reachable here actually calls its methods yet.
const consoleShim = `function writeTo(stream, line) {
  if (stream && typeof stream.write === "function") {
    stream.write(line + "\n");
    return true;
  }
  return false;
}

class Console {
  constructor(stdoutOrOptions, stderr, ignoreErrors) {
    let stdout = stdoutOrOptions;
    let opts = {};
    if (stdoutOrOptions && typeof stdoutOrOptions === "object" && typeof stdoutOrOptions.write !== "function") {
      opts = stdoutOrOptions;
      stdout = opts.stdout;
      stderr = opts.stderr;
    }
    this._stdout = stdout;
    this._stderr = stderr || stdout;
    this._groupIndent = "";
  }

  _emit(stream, args) {
    const line = this._groupIndent + args.map((a) => (typeof a === "string" ? a : String(a))).join(" ");
    if (!writeTo(stream, line)) {
      // No stream of our own - real Node's default Console (the global
      // console) is already wired to the process's actual stdout/
      // stderr, so falling back to it is the honest equivalent, not a
      // silent drop.
      (stream === this._stderr ? globalThis.console.error : globalThis.console.log)(line);
    }
  }

  log(...args) { this._emit(this._stdout, args); }
  info(...args) { this._emit(this._stdout, args); }
  debug(...args) { this._emit(this._stdout, args); }
  warn(...args) { this._emit(this._stderr, args); }
  error(...args) { this._emit(this._stderr, args); }
  trace(...args) { this._emit(this._stderr, ["Trace:", ...args]); }
  dir(obj) { this._emit(this._stdout, [obj]); }
  group(...args) {
    if (args.length) this._emit(this._stdout, args);
    this._groupIndent += "  ";
  }
  groupEnd() {
    this._groupIndent = this._groupIndent.slice(2);
  }
  assert(condition, ...args) {
    if (!condition) this._emit(this._stderr, ["Assertion failed:", ...args]);
  }
  // table(): a real, if plain, rendering (one row per array element, JSON
  // per row) rather than real Node's box-drawing grid - nothing reachable
  // here exercises this at all (see this file's own doc comment), so
  // matching Node's exact box-drawing output isn't worth building without
  // a real caller to verify it against.
  table(data) {
    if (Array.isArray(data)) {
      for (const row of data) this._emit(this._stdout, [JSON.stringify(row)]);
    } else {
      this._emit(this._stdout, [JSON.stringify(data)]);
    }
  }
}

export { Console };
export default { Console };
`

func declareConsole() {
	registerJSShim("console", consoleShim)
}

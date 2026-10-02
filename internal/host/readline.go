package host

// node:readline and node:readline/promises. Interface follows Node's
// lib/internal/readline/interface.js for non-terminal input: a
// StringDecoder over the input, lines split on \r\n, \n or \r (with
// crlfDelay for a \r\n split across chunks), a final unterminated line
// emitted at end, question()/prompt()/setPrompt(), pause/resume
// forwarded to the input, and `for await (const line of rl)`. Terminal
// line editing (TTY keypress handling) isn't implemented; a terminal
// Interface reads lines the same way. The cursor helpers write Node's CSI
// sequences. Both modules live here so readline.promises needs no import
// cycle.
const readlineShim = `import { EventEmitter } from "node:events";
import { StringDecoder } from "node:string_decoder";

const CSI = "\x1b[";
const lineEnding = /\r?\n|\r(?!\n)/g;

function emitKeypressEvents(_stream, _options) {}

function useAfterClose() {
  const err = new Error("readline was closed");
  err.code = "ERR_USE_AFTER_CLOSE";
  return err;
}

function abortError(signal) {
  const err = new Error("The operation was aborted", { cause: signal && signal.reason });
  err.name = "AbortError";
  err.code = "ABORT_ERR";
  return err;
}

class Interface extends EventEmitter {
  constructor(input, output, completer, terminal) {
    super();
    let options = {};
    if (input && input.input) {
      options = input;
      output = options.output;
      completer = options.completer;
      terminal = options.terminal;
      input = options.input;
    }
    if (terminal === undefined && !(output === null || output === undefined)) terminal = !!output.isTTY;
    this.input = input;
    this.output = output;
    this.completer = completer;
    this.terminal = !!terminal;
    this.crlfDelay = options.crlfDelay ? Math.max(100, options.crlfDelay) : 100;
    this.historySize = options.historySize === undefined ? 30 : options.historySize;
    this.history = options.history ? options.history.slice() : [];
    this.line = "";
    this.cursor = 0;
    this.closed = false;
    this.paused = false;
    this._prompt = options.prompt === undefined ? "> " : options.prompt;
    this._decoder = new StringDecoder("utf8");
    this._lineBuffer = null;
    this._sawReturnAt = 0;
    this._questionCallback = null;
    this._oldPrompt = this._prompt;

    const onData = (data) => this._normalWrite(data);
    const onEnd = () => {
      if (typeof this._lineBuffer === "string" && this._lineBuffer.length > 0) this.emit("line", this._lineBuffer);
      this._lineBuffer = null;
      this.close();
    };
    const onError = (err) => this.emit("error", err);
    if (input && typeof input.on === "function") {
      input.on("data", onData);
      input.on("end", onEnd);
      input.on("error", onError);
      this.once("close", () => {
        if (typeof input.removeListener === "function") {
          input.removeListener("data", onData);
          input.removeListener("end", onEnd);
          input.removeListener("error", onError);
        }
      });
    }
    if (options.signal) {
      const signal = options.signal;
      if (signal.aborted) {
        queueMicrotask(() => this.close());
      } else {
        const onAbort = () => this.close();
        signal.addEventListener("abort", onAbort, { once: true });
        this.once("close", () => signal.removeEventListener("abort", onAbort));
      }
    }
    if (input && typeof input.resume === "function") input.resume();
  }

  get columns() {
    return this.output && this.output.columns ? this.output.columns : Infinity;
  }

  _normalWrite(b) {
    if (b === undefined) return;
    let string = this._decoder.write(b);
    if (this._sawReturnAt && Date.now() - this._sawReturnAt <= this.crlfDelay) {
      if (string.codePointAt(0) === 10) string = string.slice(1);
      this._sawReturnAt = 0;
    }
    lineEnding.lastIndex = 0;
    let newPartContainsEnding = lineEnding.exec(string);
    if (newPartContainsEnding !== null) {
      if (this._lineBuffer) {
        string = this._lineBuffer + string;
        this._lineBuffer = null;
        lineEnding.lastIndex = 0;
        newPartContainsEnding = lineEnding.exec(string);
      }
      this._sawReturnAt = string.endsWith("\r") ? Date.now() : 0;
      const indexes = [0, newPartContainsEnding.index, lineEnding.lastIndex];
      let nextMatch;
      while ((nextMatch = lineEnding.exec(string)) !== null) indexes.push(nextMatch.index, lineEnding.lastIndex);
      const lastIndex = indexes.length - 1;
      this._lineBuffer = string.slice(indexes[lastIndex]);
      for (let i = 1; i < lastIndex; i += 2) this._onLine(string.slice(indexes[i - 1], indexes[i]));
    } else if (string) {
      this._lineBuffer = this._lineBuffer ? this._lineBuffer + string : string;
    }
  }

  _onLine(line) {
    if (this._questionCallback) {
      const cb = this._questionCallback;
      this._questionCallback = null;
      this.setPrompt(this._oldPrompt);
      cb(line);
    } else {
      this.emit("line", line);
    }
  }

  _writeToOutput(s) {
    if (this.output !== null && this.output !== undefined) this.output.write(s);
  }

  setPrompt(prompt) {
    this._prompt = prompt;
  }

  getPrompt() {
    return this._prompt;
  }

  prompt(_preserveCursor) {
    if (this.paused) this.resume();
    this._writeToOutput(this._prompt);
  }

  _question(query, cb) {
    if (this.closed) throw useAfterClose();
    if (this._questionCallback) {
      this.prompt();
    } else {
      this._oldPrompt = this._prompt;
      this.setPrompt(query);
      this._questionCallback = cb;
      this.prompt();
    }
  }

  _questionCancel() {
    if (this._questionCallback) {
      this._questionCallback = null;
      this.setPrompt(this._oldPrompt);
    }
  }

  question(query, options, cb) {
    cb = typeof options === "function" ? options : cb;
    if (options !== null && typeof options === "object" && options.signal) {
      const signal = options.signal;
      if (signal.aborted) return;
      const onAbort = () => this._questionCancel();
      signal.addEventListener("abort", onAbort, { once: true });
      const cleanup = () => signal.removeEventListener("abort", onAbort);
      const originalCb = cb;
      cb = typeof cb === "function" ? (answer) => {
        cleanup();
        return originalCb(answer);
      } : cleanup;
    }
    if (typeof cb === "function") this._question(query, cb);
  }

  write(d) {
    if (this.closed) throw useAfterClose();
    if (this.paused) this.resume();
    this._normalWrite(d);
  }

  pause() {
    if (this.paused) return;
    if (this.input && typeof this.input.pause === "function") this.input.pause();
    this.paused = true;
    this.emit("pause");
    return this;
  }

  resume() {
    if (!this.paused) return;
    if (this.input && typeof this.input.resume === "function") this.input.resume();
    this.paused = false;
    this.emit("resume");
    return this;
  }

  close() {
    if (this.closed) return;
    this.pause();
    this.closed = true;
    this.emit("close");
  }

  getCursorPos() {
    return { rows: 0, cols: this.cursor };
  }

  [Symbol.asyncIterator]() {
    if (this._lineIterator === undefined) {
      const queue = [];
      const waiters = [];
      let done = false;
      let error = null;
      const finish = () => {
        done = true;
        while (waiters.length) waiters.shift().resolve({ value: undefined, done: true });
      };
      this.on("line", (line) => {
        if (waiters.length) waiters.shift().resolve({ value: line, done: false });
        else queue.push(line);
      });
      this.on("close", finish);
      this.on("error", (err) => {
        if (waiters.length) waiters.shift().reject(err);
        else error = err;
        finish();
      });
      const rl = this;
      this._lineIterator = {
        next() {
          if (queue.length) return Promise.resolve({ value: queue.shift(), done: false });
          if (error) {
            const p = Promise.reject(error);
            error = null;
            return p;
          }
          if (done) return Promise.resolve({ value: undefined, done: true });
          return new Promise((resolve, reject) => waiters.push({ resolve, reject }));
        },
        return() {
          rl.close();
          finish();
          return Promise.resolve({ value: undefined, done: true });
        },
        [Symbol.asyncIterator]() {
          return this;
        },
      };
    }
    return this._lineIterator;
  }
}

function createInterface(input, output, completer, terminal) {
  return new Interface(input, output, completer, terminal);
}

function doneNow(callback) {
  if (typeof callback === "function") process.nextTick(callback, null);
  return true;
}

function clearLine(stream, dir, callback) {
  if (stream === null || stream === undefined) return doneNow(callback);
  const type = dir < 0 ? CSI + "1K" : dir > 0 ? CSI + "0K" : CSI + "2K";
  return stream.write(type, callback);
}

function clearScreenDown(stream, callback) {
  if (stream === null || stream === undefined) return doneNow(callback);
  return stream.write(CSI + "0J", callback);
}

function cursorTo(stream, x, y, callback) {
  if (typeof y === "function") {
    callback = y;
    y = undefined;
  }
  for (const [name, v] of [["x", x], ["y", y]]) {
    if (Number.isNaN(v)) {
      const err = new TypeError("The argument '" + name + "' is invalid. Received NaN");
      err.code = "ERR_INVALID_ARG_VALUE";
      throw err;
    }
  }
  if (stream === null || stream === undefined || (typeof x !== "number" && typeof y !== "number")) return doneNow(callback);
  if (typeof x !== "number") {
    const err = new TypeError("Cannot set cursor row without setting its column");
    err.code = "ERR_INVALID_CURSOR_POS";
    throw err;
  }
  const data = typeof y !== "number" ? CSI + (x + 1) + "G" : CSI + (y + 1) + ";" + (x + 1) + "H";
  return stream.write(data, callback);
}

function moveCursor(stream, dx, dy, callback) {
  if (stream === null || stream === undefined || !(dx || dy)) return doneNow(callback);
  let data = "";
  if (dx < 0) data += CSI + -dx + "D";
  else if (dx > 0) data += CSI + dx + "C";
  if (dy < 0) data += CSI + -dy + "A";
  else if (dy > 0) data += CSI + dy + "B";
  return stream.write(data, callback);
}

// readline/promises.
class PromisesInterface extends Interface {
  question(query, options = {}) {
    return new Promise((resolve, reject) => {
      let cb = resolve;
      if (options && options.signal) {
        const signal = options.signal;
        if (signal.aborted) return reject(abortError(signal));
        const onAbort = () => {
          this._questionCancel();
          reject(abortError(signal));
        };
        signal.addEventListener("abort", onAbort, { once: true });
        cb = (answer) => {
          signal.removeEventListener("abort", onAbort);
          resolve(answer);
        };
      }
      this._question(query, cb);
    });
  }
}
Object.defineProperty(PromisesInterface, "name", { value: "Interface" });

function createPromisesInterface(input, output, completer, terminal) {
  return new PromisesInterface(input, output, completer, terminal);
}

// Readline: batched cursor/clear commands for a stream, flushed by commit().
class Readline {
  constructor(stream, options = undefined) {
    this._stream = stream;
    this._autoCommit = !!(options && options.autoCommit);
    this._todo = [];
  }
  _add(data) {
    if (this._autoCommit) process.nextTick(() => this._stream.write(data));
    else this._todo.push(data);
    return this;
  }
  cursorTo(x, y = undefined) {
    return this._add(typeof y !== "number" ? CSI + (x + 1) + "G" : CSI + (y + 1) + ";" + (x + 1) + "H");
  }
  moveCursor(dx, dy) {
    if (!(dx || dy)) return this;
    let data = "";
    if (dx < 0) data += CSI + -dx + "D";
    else if (dx > 0) data += CSI + dx + "C";
    if (dy < 0) data += CSI + -dy + "A";
    else if (dy > 0) data += CSI + dy + "B";
    return this._add(data);
  }
  clearLine(dir) {
    return this._add(dir < 0 ? CSI + "1K" : dir > 0 ? CSI + "0K" : CSI + "2K");
  }
  clearScreenDown() {
    return this._add(CSI + "0J");
  }
  commit() {
    return new Promise((resolve) => {
      this._stream.write(this._todo.join(""), resolve);
      this._todo = [];
    });
  }
  rollback() {
    this._todo = [];
    return this;
  }
}

const promises = { Interface: PromisesInterface, Readline, createInterface: createPromisesInterface };

export { createInterface, emitKeypressEvents, Interface, clearLine, clearScreenDown, cursorTo, moveCursor, promises };
export default { createInterface, emitKeypressEvents, Interface, clearLine, clearScreenDown, cursorTo, moveCursor, promises };
`

const readlinePromisesShim = `import { promises } from "node:readline";
export const Interface = promises.Interface;
export const Readline = promises.Readline;
export const createInterface = promises.createInterface;
export default promises;
`

func declareReadline() {
	registerJSShim("readline", readlineShim)
	registerJSShim("readline/promises", readlinePromisesShim)
}

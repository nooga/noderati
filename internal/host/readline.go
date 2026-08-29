package host

const readlineShim = `import { EventEmitter } from "node:events";

function emitKeypressEvents(_stream, _options) {}

class Interface extends EventEmitter {
  constructor(options = {}) {
    super();
    this.input = options.input;
    this.output = options.output;
    this.terminal = !!options.terminal;
    this._buffer = "";
    if (this.input && typeof this.input.on === "function") {
      this.input.on("data", (chunk) => {
        this._buffer += String(chunk);
        let idx;
        while ((idx = this._buffer.indexOf("\n")) >= 0) {
          let line = this._buffer.slice(0, idx);
          this._buffer = this._buffer.slice(idx + 1);
          if (line.endsWith("\r")) line = line.slice(0, -1);
          this.emit("line", line);
        }
      });
      this.input.on("close", () => this.emit("close"));
      this.input.on("end", () => this.emit("close"));
    }
  }
  question(query, cb) {
    if (this.output && typeof this.output.write === "function") {
      this.output.write(query);
    }
    if (typeof cb === "function") {
      this.once("line", (answer) => {
        cb(answer);
      });
    }
  }
  close() {
    this.emit("close");
  }
  pause() {}
  resume() {}
  setPrompt(_prompt) {}
  prompt() {}
}

function createInterface(options) {
  return new Interface(options);
}

export { createInterface, emitKeypressEvents, Interface };
export default { createInterface, emitKeypressEvents, Interface };
`

func declareReadline() {
	registerJSShim("readline", readlineShim)
}

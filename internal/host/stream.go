package host

const streamShim = `class EventEmitter {
  constructor() {
    this._events = Object.create(null);
  }
  on(event, listener) {
    if (!this._events[event]) this._events[event] = [];
    this._events[event].push(listener);
    return this;
  }
  once(event, listener) {
    const wrapper = (...args) => {
      this.off(event, wrapper);
      listener(...args);
    };
    return this.on(event, wrapper);
  }
  off(event, listener) {
    return this.removeListener(event, listener);
  }
  removeListener(event, listener) {
    const list = this._events[event];
    if (!list) return this;
    const i = list.indexOf(listener);
    if (i >= 0) list.splice(i, 1);
    return this;
  }
  emit(event, ...args) {
    const list = this._events[event];
    if (!list || list.length === 0) return false;
    for (const fn of list.slice()) fn(...args);
    return true;
  }
}

class Readable extends EventEmitter {
  constructor(_opts) {
    super();
    this.readable = true;
  }
  pipe(dest) {
    this.on("data", (chunk) => {
      if (dest && typeof dest.write === "function") dest.write(chunk);
    });
    this.on("end", () => {
      if (dest && typeof dest.end === "function") dest.end();
    });
    return dest;
  }
}

class Writable extends EventEmitter {
  constructor(_opts) {
    super();
    this.writable = true;
  }
  write(chunk) {
    this.emit("data", chunk);
    return true;
  }
  end(chunk) {
    if (chunk !== undefined) this.write(chunk);
    this.emit("end");
    this.emit("finish");
    return this;
  }
}

export { Readable, Writable };
export default { Readable, Writable };
`

const streamPromisesShim = `export async function pipeline(..._streams) {}
export default { pipeline };
`

func declareStream() {
	registerJSShim("stream", streamShim)
	registerJSShim("stream/promises", streamPromisesShim)
}

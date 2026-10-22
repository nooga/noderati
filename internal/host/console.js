// node:console's Console class and the global console's implementation,
// following Node's lib/internal/console/constructor.js and
// lib/internal/cli_table.js (MIT, Node.js contributors).
import { inspect, formatWithOptions, getStringWidth } from "noderati-internal:inspect";
import { previewEntries } from "noderati-internal:inspect-natives";
import { types } from "node:util";

const { isMap, isSet, isMapIterator, isSetIterator, isTypedArray } = types;

const kCounts = Symbol("counts");
const kGroupIndentationWidth = Symbol("kGroupIndentWidth");
const kFormatForStderr = Symbol("kFormatForStderr");
const kFormatForStdout = Symbol("kFormatForStdout");
const kGetInspectOptions = Symbol("kGetInspectOptions");
const kColorMode = Symbol("kColorMode");
const kIsConsole = Symbol("kIsConsole");
const kWriteToConsole = Symbol("kWriteToConsole");
const kBindProperties = Symbol("kBindProperties");
const kBindStreamsEager = Symbol("kBindStreamsEager");
const kBindStreamsLazy = Symbol("kBindStreamsLazy");
const kUseStdout = Symbol("kUseStdout");
const kUseStderr = Symbol("kUseStderr");
const kGroupIndentationString = Symbol("kGroupIndentationString");
const kMaxGroupIndentation = 1000;

// Object.defineProperties one key at a time: the engine's own skips
// symbol-keyed descriptors.
function defineProperties(target, descriptors) {
  for (const key of Reflect.ownKeys(descriptors)) Object.defineProperty(target, key, descriptors[key]);
  return target;
}

function nodeError(Ctor, code, message) {
  const err = new Ctor(message);
  err.code = code;
  return err;
}

function describeReceived(v) {
  if (v == null) return ` Received ${v}`;
  if (typeof v === "function") return ` Received function ${v.name || "<anonymous>"}`;
  if (typeof v === "object") {
    if (v.constructor?.name) return ` Received an instance of ${v.constructor.name}`;
    return ` Received ${inspect(v, { depth: -1 })}`;
  }
  let inspected = inspect(v, { colors: false });
  if (inspected.length > 28) inspected = `${inspected.slice(0, 25)}...`;
  return ` Received type ${typeof v} (${inspected})`;
}

function shouldColorize(stream) {
  if (process.env.FORCE_COLOR !== undefined) {
    const force = process.env.FORCE_COLOR;
    return !(force === "0" || force === "false");
  }
  return !!(stream?.isTTY && (typeof stream.getColorDepth === "function" ? stream.getColorDepth() > 2 : true));
}

function emitWarning(msg) {
  if (typeof process.emitWarning === "function") process.emitWarning(msg);
}

function hrtimeMs() {
  if (typeof process.hrtime === "function") {
    const t = process.hrtime();
    return t[0] * 1000 + t[1] / 1e6;
  }
  return performance.now();
}

const kSecond = 1000;
const kMinute = 60 * kSecond;
const kHour = 60 * kMinute;
const pad = (value) => `${value}`.padStart(2, "0");

function formatTime(ms) {
  let hours = 0;
  let minutes = 0;
  let seconds = 0;
  if (ms >= kSecond) {
    if (ms >= kMinute) {
      if (ms >= kHour) {
        hours = Math.floor(ms / kHour);
        ms = ms % kHour;
      }
      minutes = Math.floor(ms / kMinute);
      ms = ms % kMinute;
    }
    seconds = ms / kSecond;
  }
  if (hours !== 0 || minutes !== 0) {
    const parts = seconds.toFixed(3).split(".", 2);
    seconds = parts[0];
    ms = parts[1];
    const res = hours !== 0 ? `${hours}:${pad(minutes)}` : minutes;
    return `${res}:${pad(seconds)}.${ms} (${hours !== 0 ? "h:m" : ""}m:ss.mmm)`;
  }
  if (seconds !== 0) return `${seconds.toFixed(3)}s`;
  return `${Number(ms.toFixed(3))}ms`;
}

function timeLogImpl(consoleRef, timesStore, implementation, label, args) {
  const start = timesStore.get(label);
  if (start === undefined) {
    emitWarning(`No such label '${label}' for ${implementation}`);
    return;
  }
  const formatted = formatTime(hrtimeMs() - start);
  if (args === undefined) consoleRef.log("%s: %s", label, formatted);
  else consoleRef.log("%s: %s", label, formatted, ...args);
}

const optionsMap = new WeakMap();

export function Console(options /* or: stdout, stderr, ignoreErrors = true */) {
  if (new.target === undefined) return Reflect.construct(Console, arguments);
  if (!options || typeof options.write === "function") {
    options = { stdout: options, stderr: arguments[1], ignoreErrors: arguments[2] };
  }
  const { stdout, stderr = stdout, ignoreErrors = true, colorMode = "auto", inspectOptions, groupIndentation } = options;
  if (!stdout || typeof stdout.write !== "function") {
    throw nodeError(TypeError, "ERR_CONSOLE_WRITABLE_STREAM", "Console expects a writable stream instance for stdout");
  }
  if (!stderr || typeof stderr.write !== "function") {
    throw nodeError(TypeError, "ERR_CONSOLE_WRITABLE_STREAM", "Console expects a writable stream instance for stderr");
  }
  if (colorMode !== "auto" && colorMode !== true && colorMode !== false) {
    throw nodeError(
      TypeError,
      "ERR_INVALID_ARG_VALUE",
      `The argument 'colorMode' must be one of: 'auto', true, false. Received ${inspect(colorMode)}`,
    );
  }
  if (groupIndentation !== undefined) {
    if (typeof groupIndentation !== "number") {
      throw nodeError(TypeError, "ERR_INVALID_ARG_TYPE", 'The "groupIndentation" argument must be of type number.' + describeReceived(groupIndentation));
    }
    if (!Number.isInteger(groupIndentation) || groupIndentation < 0 || groupIndentation > kMaxGroupIndentation) {
      throw nodeError(
        RangeError,
        Number.isInteger(groupIndentation) ? "ERR_OUT_OF_RANGE" : "ERR_OUT_OF_RANGE",
        `The value of "groupIndentation" is out of range. It must be ${Number.isInteger(groupIndentation) ? `>= 0 && <= ${kMaxGroupIndentation}` : "an integer"}. Received ${groupIndentation}`,
      );
    }
  }
  if (inspectOptions !== undefined) {
    if (inspectOptions === null || typeof inspectOptions !== "object") {
      throw nodeError(TypeError, "ERR_INVALID_ARG_TYPE", 'The "options.inspectOptions" property must be of type object.' + describeReceived(inspectOptions));
    }
    const inspectOptionsMap = isMap(inspectOptions)
      ? inspectOptions
      : new Map([
          [stdout, inspectOptions],
          [stderr, inspectOptions],
        ]);
    for (const opts of inspectOptionsMap.values()) {
      if (opts.colors !== undefined && options.colorMode !== undefined) {
        throw nodeError(
          TypeError,
          "ERR_INCOMPATIBLE_OPTION_PAIR",
          'Option "options.inspectOptions.color" cannot be used in combination with option "colorMode"',
        );
      }
    }
    optionsMap.set(this, inspectOptionsMap);
  }
  for (const key of Object.keys(Console.prototype)) {
    this[key] = this[key].bind(this);
    Object.defineProperty(this[key], "name", { value: key });
  }
  this[kBindStreamsEager](stdout, stderr);
  this[kBindProperties](ignoreErrors, colorMode, groupIndentation);
}

const consolePropAttributes = { writable: true, enumerable: false, configurable: true };

Object.defineProperty(Console, Symbol.hasInstance, {
  value(instance) {
    return instance !== null && (typeof instance === "object" || typeof instance === "function") && instance[kIsConsole] === true;
  },
});

const kColorInspectOptions = { colors: true };
const kNoColorInspectOptions = {};

function noop() {}

defineProperties(Console.prototype, {
  [kBindStreamsEager]: {
    ...consolePropAttributes,
    value: function (stdout, stderr) {
      defineProperties(this, {
        _stdout: { ...consolePropAttributes, value: stdout },
        _stderr: { ...consolePropAttributes, value: stderr },
      });
    },
  },
  [kBindStreamsLazy]: {
    ...consolePropAttributes,
    value: function (object) {
      let stdout;
      let stderr;
      defineProperties(this, {
        _stdout: {
          enumerable: false,
          configurable: true,
          get() {
            return (stdout ||= object.stdout);
          },
          set(value) {
            stdout = value;
          },
        },
        _stderr: {
          enumerable: false,
          configurable: true,
          get() {
            return (stderr ||= object.stderr);
          },
          set(value) {
            stderr = value;
          },
        },
      });
    },
  },
  [kBindProperties]: {
    ...consolePropAttributes,
    value: function (ignoreErrors, colorMode, groupIndentation = 2) {
      defineProperties(this, {
        _stdoutErrorHandler: { ...consolePropAttributes, value: createWriteErrorHandler(this, kUseStdout) },
        _stderrErrorHandler: { ...consolePropAttributes, value: createWriteErrorHandler(this, kUseStderr) },
        _ignoreErrors: { ...consolePropAttributes, value: Boolean(ignoreErrors) },
        _times: { ...consolePropAttributes, value: new Map() },
        [kCounts]: { ...consolePropAttributes, value: new Map() },
        [kColorMode]: { ...consolePropAttributes, value: colorMode },
        [kIsConsole]: { ...consolePropAttributes, value: true },
        [kGroupIndentationWidth]: { ...consolePropAttributes, value: groupIndentation },
        [kGroupIndentationString]: { ...consolePropAttributes, value: "" },
        [Symbol.toStringTag]: { writable: false, enumerable: false, configurable: true, value: "console" },
      });
    },
  },
  [kWriteToConsole]: {
    ...consolePropAttributes,
    value: function (streamSymbol, string) {
      const ignoreErrors = this._ignoreErrors;
      const groupIndent = this[kGroupIndentationString];
      const useStdout = streamSymbol === kUseStdout;
      const stream = useStdout ? this._stdout : this._stderr;
      const errorHandler = useStdout ? this._stdoutErrorHandler : this._stderrErrorHandler;
      if (groupIndent.length !== 0) {
        if (string.includes("\n")) string = string.replace(/\n/g, `\n${groupIndent}`);
        string = groupIndent + string;
      }
      string += "\n";
      if (ignoreErrors === false) return stream.write(string);
      const canListen = typeof stream.listenerCount === "function" && typeof stream.once === "function";
      try {
        if (canListen && stream.listenerCount("error") === 0) stream.once("error", noop);
        stream.write(string, errorHandler);
      } catch (e) {
        if (e instanceof RangeError && /call stack/i.test(String(e.message))) throw e;
      } finally {
        if (canListen && typeof stream.removeListener === "function") stream.removeListener("error", noop);
      }
    },
  },
  [kGetInspectOptions]: {
    ...consolePropAttributes,
    value: function (stream) {
      let color = this[kColorMode];
      if (color === "auto") color = shouldColorize(stream);
      const inspectOptionsMap = optionsMap.get(this);
      const options = inspectOptionsMap ? inspectOptionsMap.get(stream) : undefined;
      if (options) {
        if (options.colors === undefined) options.colors = color;
        return options;
      }
      return color ? kColorInspectOptions : kNoColorInspectOptions;
    },
  },
  [kFormatForStdout]: {
    ...consolePropAttributes,
    value: function (args) {
      if (args.length === 1 && typeof args[0] === "string") return args[0];
      const opts = this[kGetInspectOptions](this._stdout);
      return formatWithOptions(opts, ...args);
    },
  },
  [kFormatForStderr]: {
    ...consolePropAttributes,
    value: function (args) {
      if (args.length === 1 && typeof args[0] === "string") return args[0];
      const opts = this[kGetInspectOptions](this._stderr);
      return formatWithOptions(opts, ...args);
    },
  },
});

function createWriteErrorHandler(instance, streamSymbol) {
  return (err) => {
    const stream = streamSymbol === kUseStdout ? instance._stdout : instance._stderr;
    if (err !== null && err !== undefined && !stream?._writableState?.errorEmitted) {
      if (typeof stream.listenerCount === "function" && stream.listenerCount("error") === 0) stream.once("error", noop);
    }
  };
}

let cliTableChars;
function renderRow(row, columnWidths) {
  let out = cliTableChars.left;
  for (let i = 0; i < row.length; i++) {
    const cell = row[i];
    const len = getStringWidth(cell);
    const needed = columnWidths[i] - len;
    out += cell + " ".repeat(Math.ceil(needed));
    if (i !== row.length - 1) out += cliTableChars.middle;
  }
  out += cliTableChars.right;
  return out;
}

function cliTable(head, columns) {
  cliTableChars ??= {
    middleMiddle: "─",
    rowMiddle: "┼",
    topRight: "┐",
    topLeft: "┌",
    leftMiddle: "├",
    topMiddle: "┬",
    bottomRight: "┘",
    bottomLeft: "└",
    bottomMiddle: "┴",
    rightMiddle: "┤",
    left: "│ ",
    right: " │",
    middle: " │ ",
  };
  const rows = [];
  const columnWidths = head.map((h) => getStringWidth(h));
  const longestColumn = Math.max(...columns.map((a) => a.length));
  for (let i = 0; i < head.length; i++) {
    const column = columns[i];
    for (let j = 0; j < longestColumn; j++) {
      if (rows[j] === undefined) rows[j] = [];
      const value = (rows[j][i] = Object.prototype.hasOwnProperty.call(column, j) ? column[j] : "");
      const width = columnWidths[i] || 0;
      const counted = getStringWidth(value);
      columnWidths[i] = Math.max(width, counted);
    }
  }
  const divider = columnWidths.map((i) => cliTableChars.middleMiddle.repeat(i + 2));
  let result =
    cliTableChars.topLeft +
    divider.join(cliTableChars.topMiddle) +
    cliTableChars.topRight +
    "\n" +
    renderRow(head, columnWidths) +
    "\n" +
    cliTableChars.leftMiddle +
    divider.join(cliTableChars.rowMiddle) +
    cliTableChars.rightMiddle +
    "\n";
  for (const row of rows) result += `${renderRow(row, columnWidths)}\n`;
  result += cliTableChars.bottomLeft + divider.join(cliTableChars.bottomMiddle) + cliTableChars.bottomRight;
  return result;
}

const keyKey = "Key";
const valuesKey = "Values";
const indexKey = "(index)";
const iterKey = "(iteration index)";

const isArrayLike = (v) => Array.isArray(v) || isTypedArray(v);

function traceHeader(err) {
  let stack = typeof err.stack === "string" ? err.stack : "";
  const header = Error.prototype.toString.call(err);
  if (!stack.startsWith(header)) stack = stack === "" ? header : `${header}\n${stack}`;
  return stack;
}

const consoleMethods = {
  log(...args) {
    this[kWriteToConsole](kUseStdout, this[kFormatForStdout](args));
  },
  info(...args) {
    this[kWriteToConsole](kUseStdout, this[kFormatForStdout](args));
  },
  debug(...args) {
    this[kWriteToConsole](kUseStdout, this[kFormatForStdout](args));
  },
  warn(...args) {
    this[kWriteToConsole](kUseStderr, this[kFormatForStderr](args));
  },
  error(...args) {
    this[kWriteToConsole](kUseStderr, this[kFormatForStderr](args));
  },
  dir(object, options) {
    this[kWriteToConsole](
      kUseStdout,
      inspect(object, { customInspect: false, ...this[kGetInspectOptions](this._stdout), ...options }),
    );
  },
  time(label = "default") {
    label = `${label}`;
    if (this._times.has(label)) {
      emitWarning(`Label '${label}' already exists for console.time()`);
      return;
    }
    this._times.set(label, hrtimeMs());
  },
  timeEnd(label = "default") {
    label = `${label}`;
    timeLogImpl(this, this._times, "console.timeEnd()", label);
    this._times.delete(label);
  },
  timeLog(label = "default", ...data) {
    label = `${label}`;
    timeLogImpl(this, this._times, "console.timeLog()", label, data);
  },
  trace: function trace(...args) {
    const err = { name: "Trace", message: this[kFormatForStderr](args) };
    Error.captureStackTrace(err, trace);
    this.error(traceHeader(err));
  },
  assert(expression, ...args) {
    if (!expression) {
      if (args.length && typeof args[0] === "string") args[0] = `Assertion failed: ${args[0]}`;
      else args.unshift("Assertion failed");
      Reflect.apply(this.warn, this, args);
    }
  },
  clear() {
    if (this._stdout.isTTY && process.env.TERM !== "dumb") {
      this._stdout.write("\x1b[1;1H");
      this._stdout.write("\x1b[0J");
    }
  },
  count(label = "default") {
    label = `${label}`;
    const counts = this[kCounts];
    let count = counts.get(label);
    if (count === undefined) count = 1;
    else count++;
    counts.set(label, count);
    this.log(`${label}: ${count}`);
  },
  countReset(label = "default") {
    const counts = this[kCounts];
    if (!counts.has(label)) {
      emitWarning(`Count for '${label}' does not exist`);
      return;
    }
    counts.delete(`${label}`);
  },
  group(...data) {
    if (data.length > 0) Reflect.apply(this.log, this, data);
    this[kGroupIndentationString] += " ".repeat(this[kGroupIndentationWidth]);
  },
  groupEnd() {
    const currentIndentation = this[kGroupIndentationString];
    this[kGroupIndentationString] = currentIndentation.slice(
      0,
      currentIndentation.length - this[kGroupIndentationWidth],
    );
  },
  table(tabularData, properties) {
    if (properties !== undefined && !Array.isArray(properties)) {
      throw nodeError(TypeError, "ERR_INVALID_ARG_TYPE", 'The "properties" argument must be an instance of Array.' + describeReceived(properties));
    }
    if (tabularData === null || typeof tabularData !== "object") return this.log(tabularData);
    const final = (k, v) => this.log(cliTable(k, v));
    const _inspect = (v) => {
      const depth = v !== null && typeof v === "object" && !isArrayLike(v) && Object.keys(v).length > 2 ? -1 : 0;
      const opt = { depth, maxArrayLength: 3, breakLength: Infinity, ...this[kGetInspectOptions](this._stdout) };
      return inspect(v, opt);
    };
    const getIndexArray = (length) => Array.from({ length }, (_, i) => _inspect(i));

    const mapIter = isMapIterator(tabularData);
    let isKeyValue = false;
    let i = 0;
    if (mapIter) {
      const res = previewEntries(tabularData, true);
      tabularData = res[0];
      isKeyValue = res[1];
    }
    if (isKeyValue || isMap(tabularData)) {
      const keys = [];
      const values = [];
      let length = 0;
      if (mapIter) {
        for (; i < tabularData.length / 2; ++i) {
          keys.push(_inspect(tabularData[i * 2]));
          values.push(_inspect(tabularData[i * 2 + 1]));
          length++;
        }
      } else {
        for (const entry of tabularData) {
          keys.push(_inspect(entry[0]));
          values.push(_inspect(entry[1]));
          length++;
        }
      }
      return final([iterKey, keyKey, valuesKey], [getIndexArray(length), keys, values]);
    }

    const setIter = isSetIterator(tabularData);
    if (setIter) tabularData = previewEntries(tabularData, false);
    const setlike = setIter || mapIter || isSet(tabularData);
    if (setlike) {
      const values = [];
      let length = 0;
      for (const v of tabularData) {
        values.push(_inspect(v));
        length++;
      }
      return final([iterKey, valuesKey], [getIndexArray(length), values]);
    }

    const map = { __proto__: null };
    let hasPrimitives = false;
    const valuesKeyArray = [];
    const indexKeyArray = Object.keys(tabularData);
    for (; i < indexKeyArray.length; i++) {
      const item = tabularData[indexKeyArray[i]];
      const primitive = item === null || (typeof item !== "function" && typeof item !== "object");
      if (properties === undefined && primitive) {
        hasPrimitives = true;
        valuesKeyArray[i] = _inspect(item);
      } else {
        const keys = properties || Object.keys(item);
        for (const key of keys) {
          map[key] ??= [];
          if ((primitive && properties) || !Object.prototype.hasOwnProperty.call(item, key)) map[key][i] = "";
          else map[key][i] = _inspect(item[key]);
        }
      }
    }
    const keys = Object.keys(map);
    const values = Object.values(map);
    if (hasPrimitives) {
      keys.push(valuesKey);
      values.push(valuesKeyArray);
    }
    keys.unshift(indexKey);
    values.unshift(indexKeyArray);
    return final(keys, values);
  },
};

for (const method of Reflect.ownKeys(consoleMethods)) Console.prototype[method] = consoleMethods[method];
Console.prototype.dirxml = Console.prototype.log;
Console.prototype.groupCollapsed = Console.prototype.group;

// The V8 inspector's own console methods, present on Node's global console.
// Without an inspector attached they do nothing observable.
const inspectorOnly = {
  profile(label) {},
  profileEnd(label) {},
  timeStamp(label) {},
  context(name) {
    const ctx = {};
    for (const key of [
      "dir", "dirXml", "table", "groupEnd", "clear", "count", "countReset", "profile", "profileEnd", "debug", "error",
      "info", "log", "warn", "trace", "group", "groupCollapsed", "assert", "time", "timeLog", "timeEnd", "timeStamp",
    ]) {
      ctx[key] = Object.defineProperty(function () {}, "name", { value: key });
    }
    return ctx;
  },
  createTask(name) {
    if (arguments.length === 0) throw new TypeError("First argument must be a string.");
    return { run: (fn) => fn() };
  },
};

// installGlobalConsole turns target (the engine's global console object)
// into Node's global console: Console.prototype's methods bound to it,
// writing lazily to process.stdout / process.stderr.
export function installGlobalConsole(target) {
  const order = [
    "log", "info", "debug", "warn", "error", "dir", "time", "timeEnd", "timeLog", "trace", "assert", "clear", "count",
    "countReset", "group", "groupEnd", "table", "dirxml", "groupCollapsed",
  ];
  if (target[kIsConsole] === true) return target;
  for (const key of [...order, "Console", ...Object.keys(inspectorOnly)]) delete target[key];
  for (const key of order) {
    const fn = Console.prototype[key].bind(target);
    Object.defineProperty(fn, "name", { value: key });
    Object.defineProperty(target, key, { value: fn, writable: true, enumerable: true, configurable: true });
  }
  Object.defineProperty(target, "Console", { value: Console, writable: true, enumerable: true, configurable: true });
  for (const key of Object.keys(inspectorOnly)) {
    Object.defineProperty(target, key, { value: inspectorOnly[key], writable: true, enumerable: true, configurable: true });
  }
  for (const sym of [kBindStreamsEager, kBindStreamsLazy, kBindProperties, kWriteToConsole, kGetInspectOptions, kFormatForStdout, kFormatForStderr]) {
    Object.defineProperty(target, sym, { ...consolePropAttributes, value: Console.prototype[sym] });
  }
  target[kBindStreamsLazy](process);
  target[kBindProperties](true, "auto");
  // Node answers `console instanceof console.Console` through
  // Symbol.hasInstance, which the engine's instanceof doesn't consult;
  // an empty Console.prototype-derived layer gives the same answer.
  if (Object.getPrototypeOf(target) === Object.prototype) Object.setPrototypeOf(target, Object.create(Console.prototype));
  return target;
}

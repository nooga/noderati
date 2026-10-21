// util.inspect / util.format for noderati. A port of the formatting logic in
// Node's lib/internal/util/inspect.js (MIT, Node.js contributors), rewritten
// against plain JS built-ins instead of primordials. Engine-internal lookups
// V8 provides through internalBinding('util') (promise state, proxy target,
// iterator previews, non-index keys) come from noderati-internal:inspect-natives.
import { types } from "node:util";
import {
  getPromiseDetails,
  getProxyDetails,
  previewEntries,
  getOwnNonIndexKeys,
} from "noderati-internal:inspect-natives";
import bufferModule from "node:buffer";

const {
  isAsyncFunction,
  isGeneratorFunction,
  isAnyArrayBuffer,
  isArrayBuffer,
  isArgumentsObject,
  isBoxedPrimitive,
  isDataView,
  isMap,
  isMapIterator,
  isModuleNamespaceObject,
  isNativeError,
  isPromise,
  isSet,
  isSetIterator,
  isWeakMap,
  isWeakSet,
  isRegExp,
  isDate,
  isTypedArray,
  isStringObject,
  isNumberObject,
  isBooleanObject,
  isBigIntObject,
} = types;

const hasOwn = (o, k) => Object.prototype.hasOwnProperty.call(o, k);
const propertyIsEnumerable = (o, k) => Object.prototype.propertyIsEnumerable.call(o, k);
const TypedArray = Object.getPrototypeOf(Uint8Array);
const typedArrayTagGetter = Object.getOwnPropertyDescriptor(TypedArray.prototype, Symbol.toStringTag).get;
const typedArrayLengthGetter = Object.getOwnPropertyDescriptor(TypedArray.prototype, "length").get;

export const customInspectSymbol = Symbol.for("nodejs.util.inspect.custom");

function isError(e) {
  if (isNativeError(e)) return true;
  try {
    return e instanceof Error;
  } catch {
    return false;
  }
}

const builtInObjects = new Set(
  Object.getOwnPropertyNames(globalThis).filter((e) => /^[A-Z][a-zA-Z0-9]+$/.test(e)),
);

export const inspectDefaultOptions = Object.seal({
  showHidden: false,
  depth: 2,
  colors: false,
  customInspect: true,
  showProxy: false,
  maxArrayLength: 100,
  maxStringLength: 10000,
  breakLength: 80,
  compact: 3,
  sorted: false,
  getters: false,
  numericSeparator: false,
});

const kObjectType = 0;
const kArrayType = 1;
const kArrayExtrasType = 2;

const keyStrRegExp = /^[a-zA-Z_][a-zA-Z_0-9]*$/;
const numberRegExp = /^(0|[1-9][0-9]*)$/;
const coreModuleRegExp = /^ {4}at (?:[^/\\(]+ \(|)node:(.+):\d+:\d+\)?$/;
const classRegExp = /^(\s+[^(]*?)\s*{/;
const stripCommentsRegExp = /(\/\/.*?\n)|(\/\*(.|\n)*?\*\/)/g;

const kMinLineLength = 16;

const kWeak = 0;
const kIterator = 1;
const kMapEntries = 2;

const kPending = 0;
const kRejected = 2;

// Escapes for control characters, the single quote and the backslash.
const meta = [];
for (let i = 0; i < 0xa0; i++) {
  if (i < 0x20 || (i >= 0x7f && i < 0xa0)) meta[i] = "\\x" + i.toString(16).toUpperCase().padStart(2, "0");
  else meta[i] = "";
}
meta[8] = "\\b";
meta[9] = "\\t";
meta[10] = "\\n";
meta[12] = "\\f";
meta[13] = "\\r";
meta[39] = "\\'";
meta[92] = "\\\\";

const ansi = new RegExp(
  "[\\u001B\\u009B][[\\]()#;?]*" +
    "(?:(?:(?:(?:;[-a-zA-Z\\d\\/\\#&.:=?%@~_]+)*" +
    "|[a-zA-Z\\d]+(?:;[-a-zA-Z\\d\\/\\#&.:=?%@~_]*)*)?" +
    "(?:\\u0007|\\u001B\\u005C|\\u009C))" +
    "|(?:(?:\\d{1,4}(?:;\\d{0,4})*)?" +
    "[\\dA-PR-TZcf-nq-uy=><~]))",
  "g",
);

function getUserOptions(ctx, isCrossContext) {
  const ret = {
    stylize: ctx.stylize,
    showHidden: ctx.showHidden,
    depth: ctx.depth,
    colors: ctx.colors,
    customInspect: ctx.customInspect,
    showProxy: ctx.showProxy,
    maxArrayLength: ctx.maxArrayLength,
    maxStringLength: ctx.maxStringLength,
    breakLength: ctx.breakLength,
    compact: ctx.compact,
    sorted: ctx.sorted,
    getters: ctx.getters,
    numericSeparator: ctx.numericSeparator,
    ...ctx.userOptions,
  };
  if (isCrossContext) {
    Object.setPrototypeOf(ret, null);
    for (const key of Object.keys(ret)) {
      if ((typeof ret[key] === "object" || typeof ret[key] === "function") && ret[key] !== null) {
        delete ret[key];
      }
    }
    ret.stylize = Object.setPrototypeOf((value, flavour) => {
      let stylized;
      try {
        stylized = `${ctx.stylize(value, flavour)}`;
      } catch {}
      if (typeof stylized !== "string") return value;
      return stylized;
    }, null);
  }
  return ret;
}

export function inspect(value, opts) {
  const ctx = {
    budget: {},
    indentationLvl: 0,
    seen: [],
    currentDepth: 0,
    stylize: stylizeNoColor,
    showHidden: inspectDefaultOptions.showHidden,
    depth: inspectDefaultOptions.depth,
    colors: inspectDefaultOptions.colors,
    customInspect: inspectDefaultOptions.customInspect,
    showProxy: inspectDefaultOptions.showProxy,
    maxArrayLength: inspectDefaultOptions.maxArrayLength,
    maxStringLength: inspectDefaultOptions.maxStringLength,
    breakLength: inspectDefaultOptions.breakLength,
    compact: inspectDefaultOptions.compact,
    sorted: inspectDefaultOptions.sorted,
    getters: inspectDefaultOptions.getters,
    numericSeparator: inspectDefaultOptions.numericSeparator,
  };
  if (arguments.length > 1) {
    // Legacy signature: inspect(value, showHidden, depth, colors).
    if (arguments.length > 2) {
      if (arguments[2] !== undefined) ctx.depth = arguments[2];
      if (arguments.length > 3 && arguments[3] !== undefined) ctx.colors = arguments[3];
    }
    if (typeof opts === "boolean") {
      ctx.showHidden = opts;
    } else if (opts) {
      const optKeys = Object.keys(opts);
      for (let i = 0; i < optKeys.length; ++i) {
        const key = optKeys[i];
        if (hasOwn(inspectDefaultOptions, key) || key === "stylize") {
          ctx[key] = opts[key];
        } else if (ctx.userOptions === undefined) {
          ctx.userOptions = opts;
        }
      }
    }
  }
  if (ctx.colors) ctx.stylize = stylizeWithColor;
  if (ctx.maxArrayLength === null) ctx.maxArrayLength = Infinity;
  if (ctx.maxStringLength === null) ctx.maxStringLength = Infinity;
  return formatValue(ctx, value, 0);
}
inspect.custom = customInspectSymbol;

Object.defineProperty(inspect, "defaultOptions", {
  get() {
    return inspectDefaultOptions;
  },
  set(options) {
    if (options === null || typeof options !== "object") {
      const err = new TypeError('The "options" argument must be of type object.' + receivedSuffix(options));
      err.code = "ERR_INVALID_ARG_TYPE";
      throw err;
    }
    return Object.assign(inspectDefaultOptions, options);
  },
});

const defaultFG = 39;
const defaultBG = 49;
inspect.colors = {
  __proto__: null,
  reset: [0, 0],
  bold: [1, 22],
  dim: [2, 22],
  italic: [3, 23],
  underline: [4, 24],
  blink: [5, 25],
  inverse: [7, 27],
  hidden: [8, 28],
  strikethrough: [9, 29],
  doubleunderline: [21, 24],
  black: [30, defaultFG],
  red: [31, defaultFG],
  green: [32, defaultFG],
  yellow: [33, defaultFG],
  blue: [34, defaultFG],
  magenta: [35, defaultFG],
  cyan: [36, defaultFG],
  white: [37, defaultFG],
  bgBlack: [40, defaultBG],
  bgRed: [41, defaultBG],
  bgGreen: [42, defaultBG],
  bgYellow: [43, defaultBG],
  bgBlue: [44, defaultBG],
  bgMagenta: [45, defaultBG],
  bgCyan: [46, defaultBG],
  bgWhite: [47, defaultBG],
  framed: [51, 54],
  overlined: [53, 55],
  gray: [90, defaultFG],
  redBright: [91, defaultFG],
  greenBright: [92, defaultFG],
  yellowBright: [93, defaultFG],
  blueBright: [94, defaultFG],
  magentaBright: [95, defaultFG],
  cyanBright: [96, defaultFG],
  whiteBright: [97, defaultFG],
  bgGray: [100, defaultBG],
  bgRedBright: [101, defaultBG],
  bgGreenBright: [102, defaultBG],
  bgYellowBright: [103, defaultBG],
  bgBlueBright: [104, defaultBG],
  bgMagentaBright: [105, defaultBG],
  bgCyanBright: [106, defaultBG],
  bgWhiteBright: [107, defaultBG],
};

function defineColorAlias(target, alias) {
  Object.defineProperty(inspect.colors, alias, {
    get() {
      return this[target];
    },
    set(value) {
      this[target] = value;
    },
    configurable: true,
    enumerable: false,
  });
}
defineColorAlias("gray", "grey");
defineColorAlias("gray", "blackBright");
defineColorAlias("bgGray", "bgGrey");
defineColorAlias("bgGray", "bgBlackBright");
defineColorAlias("dim", "faint");
defineColorAlias("strikethrough", "crossedout");
defineColorAlias("strikethrough", "strikeThrough");
defineColorAlias("strikethrough", "crossedOut");
defineColorAlias("hidden", "conceal");
defineColorAlias("inverse", "swapColors");
defineColorAlias("inverse", "swapcolors");
defineColorAlias("doubleunderline", "doubleUnderline");

inspect.styles = Object.assign({ __proto__: null }, {
  special: "cyan",
  number: "yellow",
  bigint: "yellow",
  boolean: "yellow",
  undefined: "grey",
  null: "bold",
  string: "green",
  symbol: "green",
  date: "magenta",
  regexp: highlightRegExp,
  module: "underline",
});
highlightRegExp.colors = ["green", "red", "yellow", "cyan", "magenta"];
const highlightRegExpColors = highlightRegExp.colors.slice();

// Colorizes a RegExp source by group depth (Node's highlightRegExp).
function highlightRegExp(regexpString) {
  let out = "";
  let i = 0;
  let depth = 0;
  let inClass = false;
  const paletteNames = highlightRegExp.colors?.length > 0 ? highlightRegExp.colors : highlightRegExpColors;
  const palette = paletteNames.reduce((acc, name) => {
    const color = inspect.colors[name];
    if (color) acc.push([`\u001b[${color[0]}m`, `\u001b[${color[1]}m`]);
    return acc;
  }, []);

  function writeGroup(start, end, decreaseDepth = 1) {
    let seq = "";
    i++;
    while (i < regexpString.length && regexpString[i] !== end) seq += regexpString[i++];
    if (i < regexpString.length) {
      depth -= decreaseDepth;
      write(start);
      writeDepth(seq, 1, 1);
      write(end);
      depth += decreaseDepth;
    } else {
      writeDepth(start, 1, -seq.length);
    }
  }
  const write = (str) => {
    const idx = depth % palette.length;
    const color = palette[idx] ?? palette[0];
    out += color[0] + str + color[1];
    return idx;
  };
  function writeDepth(str, incDepth, incI) {
    depth += incDepth;
    write(str);
    depth -= incDepth;
    i += incI;
  }

  write("/");
  depth++;
  i = 1;
  while (i < regexpString.length) {
    const ch = regexpString[i];
    if (inClass) {
      if (ch === "\\") {
        let seq = "\\";
        i++;
        if (i < regexpString.length) {
          seq += regexpString[i++];
          const next = seq[1];
          if (next === "u" && regexpString[i] === "{") {
            writeGroup(`${seq}{`, "}", 0);
            continue;
          } else if ((next === "p" || next === "P") && regexpString[i] === "{") {
            writeGroup(`${seq}{`, "}", 0);
            continue;
          } else if (seq[1] === "x") {
            seq += regexpString.slice(i, i + 2);
            i += 2;
          }
        }
        write(seq);
      } else if (ch === "]") {
        depth--;
        write("]");
        i++;
        inClass = false;
      } else if (ch === "-" && regexpString[i - 1] !== "[" && i + 1 < regexpString.length && regexpString[i + 1] !== "]") {
        writeDepth("-", 1, 1);
      } else {
        write(ch);
        i++;
      }
    } else if (ch === "[") {
      write("[");
      depth++;
      i++;
      inClass = true;
    } else if (ch === "(") {
      write("(");
      depth++;
      i++;
      if (i < regexpString.length && regexpString[i] === "?") {
        i++;
        const a = i < regexpString.length ? regexpString[i] : "";
        if (a === ":" || a === "=" || a === "!") {
          writeDepth(`?${a}`, -1, 1);
        } else {
          const b = i + 1 < regexpString.length ? regexpString[i + 1] : "";
          if (a === "<" && (b === "=" || b === "!")) {
            writeDepth(`?<${b}`, -1, 2);
          } else if (a === "<") {
            i++;
            const start = i;
            while (i < regexpString.length && regexpString[i] !== ">") i++;
            const name = regexpString.slice(start, i);
            if (i < regexpString.length && regexpString[i] === ">") {
              depth--;
              write("?<");
              writeDepth(name, 1, 0);
              write(">");
              depth++;
              i++;
            } else {
              writeDepth("?<", -1, 0);
              write(name);
            }
          } else {
            write("?");
          }
        }
      }
    } else if (ch === ")") {
      depth--;
      write(")");
      i++;
    } else if (ch === "\\") {
      let seq = "\\";
      i++;
      if (i < regexpString.length) {
        seq += regexpString[i++];
        const next = seq[1];
        if (i < regexpString.length) {
          if (next === "u" && regexpString[i] === "{") {
            writeGroup(`${seq}{`, "}", 0);
            continue;
          } else if (next === "x") {
            seq += regexpString.slice(i, i + 2);
            i += 2;
          } else if (next >= "0" && next <= "9") {
            while (i < regexpString.length && regexpString[i] >= "0" && regexpString[i] <= "9") seq += regexpString[i++];
          } else if (next === "k" && regexpString[i] === "<") {
            writeGroup(`${seq}<`, ">");
            continue;
          } else if ((next === "p" || next === "P") && regexpString[i] === "{") {
            writeGroup(`${seq}{`, "}", 0);
            continue;
          }
        }
      }
      writeDepth(seq, 1, 0);
    } else if (ch === "|" || ch === "+" || ch === "*" || ch === "?" || ch === "," || ch === "^" || ch === "$") {
      writeDepth(ch, 3, 1);
    } else if (ch === "{") {
      i++;
      let digits = "";
      while (i < regexpString.length && regexpString[i] >= "0" && regexpString[i] <= "9") digits += regexpString[i++];
      if (digits) {
        write("{");
        depth++;
        writeDepth(digits, 1, 0);
      }
      if (i < regexpString.length) {
        if (regexpString[i] === ",") {
          if (!digits) {
            write("{");
            depth++;
          }
          write(",");
          i++;
        } else if (!digits) {
          depth += 1;
          write("{");
          depth -= 1;
          continue;
        }
      }
      let digits2 = "";
      while (i < regexpString.length && regexpString[i] >= "0" && regexpString[i] <= "9") digits2 += regexpString[i++];
      if (digits2) writeDepth(digits2, 1, 0);
      if (i < regexpString.length && regexpString[i] === "}") {
        depth--;
        write("}");
        i++;
      }
      if (i < regexpString.length && regexpString[i] === "?") writeDepth("?", 3, 1);
    } else if (ch === ".") {
      writeDepth(ch, 2, 1);
    } else if (ch === "/") {
      break;
    } else {
      writeDepth(ch, 1, 1);
    }
  }
  writeDepth("/", -1, 1);
  if (i < regexpString.length) write(regexpString.slice(i));
  return out;
}

function addQuotes(str, quotes) {
  if (quotes === -1) return `"${str}"`;
  if (quotes === -2) return `\`${str}\``;
  return `'${str}'`;
}

// Escapes control characters, the chosen quote, the backslash and lone
// surrogates. Written as a plain scan (no lookbehind regexps).
export function strEscape(str) {
  let singleQuote = 39;
  if (str.includes("'")) {
    if (!str.includes('"')) singleQuote = -1;
    else if (!str.includes("`") && !str.includes("${")) singleQuote = -2;
  }
  let result = "";
  let last = 0;
  for (let i = 0; i < str.length; i++) {
    const point = str.charCodeAt(i);
    if (point === singleQuote || point === 92 || point < 32 || (point > 126 && point < 160)) {
      result += str.slice(last, i) + meta[point];
      last = i + 1;
    } else if (point >= 0xd800 && point <= 0xdfff) {
      if (point <= 0xdbff && i + 1 < str.length) {
        const next = str.charCodeAt(i + 1);
        if (next >= 0xdc00 && next <= 0xdfff) {
          i++;
          continue;
        }
      }
      result += `${str.slice(last, i)}\\u${point.toString(16)}`;
      last = i + 1;
    }
  }
  if (last !== str.length) result += str.slice(last);
  return addQuotes(result, singleQuote);
}

function escapeSymbolDescription(str) {
  let result = "";
  for (let i = 0; i < str.length; i++) {
    const point = str.charCodeAt(i);
    if (point === 39 || point === 92 || point < 32 || (point > 126 && point < 160)) result += meta[point];
    else result += str[i];
  }
  return result;
}

function stylizeWithColor(str, styleType) {
  const style = inspect.styles[styleType];
  if (style !== undefined) {
    const color = inspect.colors[style];
    if (color !== undefined) return `\u001b[${color[0]}m${str}\u001b[${color[1]}m`;
    if (typeof style === "function") return style(str);
  }
  return str;
}

function stylizeNoColor(str) {
  return str;
}

function isInstanceof(object, proto) {
  try {
    return object instanceof proto;
  } catch {
    return false;
  }
}

const wellKnownPrototypes = new Map();
for (const ctor of [
  Array, ArrayBuffer, Function, Map, Set, Object, TypedArray, RegExp, Date, DataView,
  Error, globalThis.AggregateError, RangeError, TypeError, Boolean, Number, String, Promise, WeakMap, WeakSet,
]) {
  if (typeof ctor === "function") {
    wellKnownPrototypes.set(ctor.prototype, { name: ctor === TypedArray ? "TypedArray" : ctor.name, constructor: ctor });
  }
}

// V8's internal class name for an object, used for null-prototype prefixes.
function internalGetConstructorName(v) {
  if (typeof v === "function") return "Function";
  if (Array.isArray(v)) return "Array";
  if (isMap(v)) return "Map";
  if (isSet(v)) return "Set";
  if (isTypedArray(v)) return typedArrayTagGetter.call(v);
  if (isArrayBuffer(v)) return "ArrayBuffer";
  if (isAnyArrayBuffer(v)) return "SharedArrayBuffer";
  if (isDataView(v)) return "DataView";
  if (isPromise(v)) return "Promise";
  if (isRegExp(v)) return "RegExp";
  if (isDate(v)) return "Date";
  if (isNativeError(v)) return "Error";
  if (isWeakMap(v)) return "WeakMap";
  if (isWeakSet(v)) return "WeakSet";
  if (isNumberObject(v)) return "Number";
  if (isStringObject(v)) return "String";
  if (isBooleanObject(v)) return "Boolean";
  if (isBigIntObject(v)) return "BigInt";
  if (isBoxedPrimitive(v)) return "Symbol";
  return "Object";
}

function getConstructorName(obj, ctx, recurseTimes, protoProps) {
  let firstProto;
  const tmp = obj;
  while (obj) {
    const known = wellKnownPrototypes.get(obj);
    if (known !== undefined) {
      if (isInstanceof(tmp, known.constructor)) {
        if (protoProps !== undefined && firstProto !== obj) {
          addPrototypeProperties(ctx, tmp, firstProto || tmp, recurseTimes, protoProps);
        }
        return known.name;
      }
    }
    const descriptor = Object.getOwnPropertyDescriptor(obj, "constructor");
    if (
      descriptor !== undefined &&
      typeof descriptor.value === "function" &&
      descriptor.value.name !== "" &&
      isInstanceof(tmp, descriptor.value)
    ) {
      if (protoProps !== undefined && (firstProto !== obj || !builtInObjects.has(descriptor.value.name))) {
        addPrototypeProperties(ctx, tmp, firstProto || tmp, recurseTimes, protoProps);
      }
      return String(descriptor.value.name);
    }
    obj = Object.getPrototypeOf(obj);
    if (firstProto === undefined) firstProto = obj;
  }
  if (firstProto === null) return null;
  const res = internalGetConstructorName(tmp);
  if (recurseTimes > ctx.depth && ctx.depth !== null) return `${res} <Complex prototype>`;
  const protoConstr = getConstructorName(firstProto, ctx, recurseTimes + 1, protoProps);
  if (protoConstr === null) {
    return `${res} <${inspect(firstProto, { ...ctx, customInspect: false, depth: -1 })}>`;
  }
  return `${res} <${protoConstr}>`;
}

function addPrototypeProperties(ctx, main, obj, recurseTimes, output) {
  let depth = 0;
  let keys;
  let keySet;
  do {
    if (depth !== 0 || main === obj) {
      obj = Object.getPrototypeOf(obj);
      if (obj === null) return;
      const descriptor = Object.getOwnPropertyDescriptor(obj, "constructor");
      if (descriptor !== undefined && typeof descriptor.value === "function" && builtInObjects.has(descriptor.value.name)) {
        return;
      }
    }
    if (depth === 0) keySet = new Set();
    else keys.forEach((key) => keySet.add(key));
    keys = Reflect.ownKeys(obj);
    ctx.seen.push(main);
    for (const key of keys) {
      if (key === "constructor" || hasOwn(main, key) || (depth !== 0 && keySet.has(key))) continue;
      const desc = Object.getOwnPropertyDescriptor(obj, key);
      if (typeof desc.value === "function") continue;
      const value = formatProperty(ctx, obj, recurseTimes, key, kObjectType, desc, main);
      if (ctx.colors) output.push(`\u001b[2m${value}\u001b[22m`);
      else output.push(value);
    }
    ctx.seen.pop();
  } while (++depth !== 3);
}

function getPrefix(constructor, tag, fallback, size = "") {
  if (constructor === null) {
    if (tag !== "" && fallback !== tag) return `[${fallback}${size}: null prototype] [${tag}] `;
    return `[${fallback}${size}: null prototype] `;
  }
  let result = `${constructor}${size} `;
  if (tag !== "") {
    const position = constructor.indexOf(tag);
    if (position === -1) {
      result += `[${tag}] `;
    } else {
      const endPos = position + tag.length;
      if (endPos !== constructor.length && constructor[endPos] === constructor[endPos].toLowerCase()) {
        result += `[${tag}] `;
      }
    }
  }
  return result;
}

function getKeys(value, showHidden) {
  let keys;
  const symbols = Object.getOwnPropertySymbols(value);
  if (showHidden) {
    keys = Object.getOwnPropertyNames(value);
    if (symbols.length !== 0) keys.push(...symbols);
  } else {
    try {
      keys = Object.keys(value);
    } catch {
      keys = Object.getOwnPropertyNames(value);
    }
    if (symbols.length !== 0) keys.push(...symbols.filter((key) => propertyIsEnumerable(value, key)));
  }
  return keys;
}

// Own non-index keys (V8's getOwnNonIndexProperties). Arrays and typed
// arrays ask the engine directly so huge arrays aren't enumerated.
function getOwnNonIndexProperties(value, onlyEnumerable) {
  let keys = getOwnNonIndexKeys(value);
  if (keys === undefined) {
    keys = Reflect.ownKeys(value).filter((k) => typeof k !== "string" || !numberRegExp.test(k) || +k > 2 ** 32 - 2);
  }
  if (Array.isArray(value)) {
    keys = keys.filter((k) => k !== "length");
    if (!onlyEnumerable) keys.unshift("length");
  }
  if (onlyEnumerable) keys = keys.filter((k) => propertyIsEnumerable(value, k));
  return keys;
}

function getCtxStyle(value, constructor, tag) {
  let fallback = "";
  if (constructor === null) {
    fallback = internalGetConstructorName(value);
    if (fallback === tag) fallback = "Object";
  }
  return getPrefix(constructor, tag, fallback);
}

function formatProxy(ctx, proxy, recurseTimes) {
  if (recurseTimes > ctx.depth && ctx.depth !== null) return ctx.stylize("Proxy [Array]", "special");
  recurseTimes += 1;
  ctx.indentationLvl += 2;
  const res = [formatValue(ctx, proxy[0], recurseTimes), formatValue(ctx, proxy[1], recurseTimes)];
  ctx.indentationLvl -= 2;
  return reduceToSingleString(ctx, res, "", ["Proxy [", "]"], kArrayExtrasType, recurseTimes);
}

function formatValue(ctx, value, recurseTimes, typedArray) {
  if (typeof value !== "object" && typeof value !== "function") {
    return formatPrimitive(ctx.stylize, value, ctx);
  }
  if (value === null) return ctx.stylize("null", "null");

  const context = value;
  let proxies = 0;
  let proxy = getProxyDetails(value, !!ctx.showProxy);
  if (proxy !== undefined) {
    if (ctx.showProxy) {
      if (proxy[0] === null) return ctx.stylize("<Revoked Proxy>", "special");
      return formatProxy(ctx, proxy, recurseTimes);
    }
    do {
      if (proxy === null) {
        let formatted = ctx.stylize("<Revoked Proxy>", "special");
        for (let i = 0; i < proxies; i++) {
          formatted = `${ctx.stylize("Proxy(", "special")}${formatted}${ctx.stylize(")", "special")}`;
        }
        return formatted;
      }
      value = proxy;
      proxy = getProxyDetails(value, false);
      proxies += 1;
    } while (proxy !== undefined);
  }

  if (ctx.customInspect) {
    let maybeCustom;
    try {
      maybeCustom = value[customInspectSymbol];
    } catch {}
    if (
      typeof maybeCustom === "function" &&
      maybeCustom !== inspect &&
      Object.getOwnPropertyDescriptor(value, "constructor")?.value?.prototype !== value
    ) {
      const depth = ctx.depth === null ? null : ctx.depth - recurseTimes;
      const isCrossContext = proxies !== 0 || !isInstanceof(context, Object);
      const ret = maybeCustom.call(context, depth, getUserOptions(ctx, isCrossContext), inspect);
      if (ret !== context) {
        if (typeof ret !== "string") return formatValue(ctx, ret, recurseTimes);
        return ret.replaceAll("\n", `\n${" ".repeat(ctx.indentationLvl)}`);
      }
    }
  }

  if (ctx.seen.includes(value)) {
    let index = 1;
    if (ctx.circular === undefined) {
      ctx.circular = new Map();
      ctx.circular.set(value, index);
    } else {
      index = ctx.circular.get(value);
      if (index === undefined) {
        index = ctx.circular.size + 1;
        ctx.circular.set(value, index);
      }
    }
    return ctx.stylize(`[Circular *${index}]`, "special");
  }

  let formatted = formatRaw(ctx, value, recurseTimes, typedArray);
  for (let i = 0; i < proxies; i++) {
    formatted = `${ctx.stylize("Proxy(", "special")}${formatted}${ctx.stylize(")", "special")}`;
  }
  return formatted;
}

function formatRaw(ctx, value, recurseTimes, typedArray) {
  let keys;
  let protoProps;
  if (ctx.showHidden && (recurseTimes <= ctx.depth || ctx.depth === null)) protoProps = [];

  const constructor = getConstructorName(value, ctx, recurseTimes, protoProps);
  if (protoProps !== undefined && protoProps.length === 0) protoProps = undefined;

  let tag = "";
  try {
    tag = value[Symbol.toStringTag];
  } catch {}
  if (
    typeof tag !== "string" ||
    (tag !== "" && (ctx.showHidden ? hasOwn : propertyIsEnumerable)(value, Symbol.toStringTag))
  ) {
    tag = "";
  }
  let base = "";
  let formatter = () => [];
  let braces;
  let noIterator = true;
  let i = 0;
  const onlyEnumerable = !ctx.showHidden;
  let extrasType = kObjectType;
  let extraKeys;

  if (Symbol.iterator in value || constructor === null) {
    noIterator = false;
    if (Array.isArray(value)) {
      const prefix =
        constructor !== "Array" || tag !== "" ? getPrefix(constructor, tag, "Array", `(${value.length})`) : "";
      keys = getOwnNonIndexProperties(value, onlyEnumerable);
      braces = [`${prefix}[`, "]"];
      if (value.length === 0 && keys.length === 0 && protoProps === undefined) return `${braces[0]}]`;
      extrasType = kArrayExtrasType;
      formatter = formatArray;
    } else if (isSet(value)) {
      const realSize = Object.getOwnPropertyDescriptor(Set.prototype, "size").get.call(value);
      const prefix = getPrefix(constructor, tag, "Set", `(${realSize})`);
      keys = getKeys(value, ctx.showHidden);
      const target = constructor !== null ? value : Set.prototype.values.call(value);
      formatter = (c, v, r) => formatSet(target, c, v, r);
      if (realSize === 0 && keys.length === 0 && protoProps === undefined) return `${prefix}{}`;
      braces = [`${prefix}{`, "}"];
    } else if (isMap(value)) {
      const size = Object.getOwnPropertyDescriptor(Map.prototype, "size").get.call(value);
      const prefix = getPrefix(constructor, tag, "Map", `(${size})`);
      keys = getKeys(value, ctx.showHidden);
      const target = constructor !== null ? value : Map.prototype.entries.call(value);
      formatter = (c, v, r) => formatMap(target, c, v, r);
      if (size === 0 && keys.length === 0 && protoProps === undefined) return `${prefix}{}`;
      braces = [`${prefix}{`, "}"];
    } else if (isTypedArray(value)) {
      keys = getOwnNonIndexProperties(value, onlyEnumerable);
      let bound = value;
      let fallback = "";
      if (constructor === null) {
        fallback = typedArrayTagGetter.call(value);
        bound = new globalThis[fallback](value);
      }
      const size = typedArrayLengthGetter.call(value);
      const prefix = getPrefix(constructor, tag, fallback, `(${size})`);
      braces = [`${prefix}[`, "]"];
      if (value.length === 0 && keys.length === 0 && !ctx.showHidden) return `${braces[0]}]`;
      formatter = (c) => formatTypedArray(bound, size, c);
      extrasType = kArrayExtrasType;
      if (ctx.showHidden) {
        extraKeys = ["BYTES_PER_ELEMENT", "length", "byteLength", "byteOffset", "buffer"];
        typedArray = true;
      }
    } else if (isMapIterator(value)) {
      keys = getKeys(value, ctx.showHidden);
      braces = getIteratorBraces("Map", tag);
      formatter = (c, v, r) => formatIterator(braces, c, v, r);
    } else if (isSetIterator(value)) {
      keys = getKeys(value, ctx.showHidden);
      braces = getIteratorBraces("Set", tag);
      formatter = (c, v, r) => formatIterator(braces, c, v, r);
    } else {
      noIterator = true;
    }
  }
  if (noIterator) {
    keys = getKeys(value, ctx.showHidden);
    braces = ["{", "}"];
    if (typeof value === "function") {
      base = getFunctionBase(ctx, value, constructor, tag);
      if (keys.length === 0 && protoProps === undefined) return ctx.stylize(base, "special");
    } else if (constructor === "Object") {
      if (isArgumentsObject(value)) {
        braces[0] = "[Arguments] {";
      } else if (tag !== "") {
        braces[0] = `${getPrefix(constructor, tag, "Object")}{`;
      }
      if (keys.length === 0 && protoProps === undefined) return `${braces[0]}}`;
    } else if (isRegExp(value)) {
      base = RegExp.prototype.toString.call(constructor !== null ? value : new RegExp(value));
      const prefix = getPrefix(constructor, tag, "RegExp");
      if (prefix !== "RegExp ") base = `${prefix}${base}`;
      base = ctx.stylize(base, "regexp");
      if ((keys.length === 0 && protoProps === undefined) || (recurseTimes > ctx.depth && ctx.depth !== null)) {
        return base;
      }
    } else if (isDate(value)) {
      const t = Date.prototype.getTime.call(value);
      base = Number.isNaN(t) ? Date.prototype.toString.call(value) : Date.prototype.toISOString.call(value);
      const prefix = getPrefix(constructor, tag, "Date");
      if (prefix !== "Date ") base = `${prefix}${base}`;
      if (keys.length === 0 && protoProps === undefined) return ctx.stylize(base, "date");
    } else if (isError(value)) {
      base = formatError(value, constructor, tag, ctx, keys);
      if (keys.length === 0 && protoProps === undefined) return base;
    } else if (isAnyArrayBuffer(value)) {
      const arrayType = isArrayBuffer(value) ? "ArrayBuffer" : "SharedArrayBuffer";
      const prefix = getPrefix(constructor, tag, arrayType);
      if (typedArray === undefined) {
        formatter = formatArrayBuffer;
      } else if (keys.length === 0 && protoProps === undefined) {
        return prefix + `{ byteLength: ${formatNumber(ctx.stylize, value.byteLength, false)} }`;
      }
      braces[0] = `${prefix}{`;
      extraKeys = ["byteLength"];
    } else if (isDataView(value)) {
      braces[0] = `${getPrefix(constructor, tag, "DataView")}{`;
      extraKeys = ["byteLength", "byteOffset", "buffer"];
    } else if (isPromise(value)) {
      braces[0] = `${getPrefix(constructor, tag, "Promise")}{`;
      formatter = formatPromise;
    } else if (isWeakSet(value)) {
      braces[0] = `${getPrefix(constructor, tag, "WeakSet")}{`;
      formatter = ctx.showHidden ? formatWeakSet : formatWeakCollection;
    } else if (isWeakMap(value)) {
      braces[0] = `${getPrefix(constructor, tag, "WeakMap")}{`;
      formatter = ctx.showHidden ? formatWeakMap : formatWeakCollection;
    } else if (isModuleNamespaceObject(value)) {
      braces[0] = `${getPrefix(constructor, tag, "Module")}{`;
      formatter = (c, v, r) => formatNamespaceObject(keys, c, v, r);
    } else if (isBoxedPrimitive(value)) {
      base = getBoxedBase(value, ctx, keys, constructor, tag);
      if (keys.length === 0 && protoProps === undefined) return base;
    } else if (isURL(value) && !(recurseTimes > ctx.depth && ctx.depth !== null)) {
      base = value.href;
      if (keys.length === 0 && protoProps === undefined) return base;
    } else {
      if (keys.length === 0 && protoProps === undefined) return `${getCtxStyle(value, constructor, tag)}{}`;
      braces[0] = `${getCtxStyle(value, constructor, tag)}{`;
    }
  }

  if (recurseTimes > ctx.depth && ctx.depth !== null) {
    let constructorName = getCtxStyle(value, constructor, tag).slice(0, -1);
    if (constructor !== null) constructorName = `[${constructorName}]`;
    return ctx.stylize(constructorName, "special");
  }
  recurseTimes += 1;

  ctx.seen.push(value);
  ctx.currentDepth = recurseTimes;
  let output;
  const indentationLvl = ctx.indentationLvl;
  try {
    output = formatter(ctx, value, recurseTimes);
    if (extraKeys !== undefined) {
      for (i = 0; i < extraKeys.length; i++) {
        let formatted;
        try {
          formatted = formatExtraProperties(ctx, value, recurseTimes, extraKeys[i], typedArray);
        } catch {
          const tempValue = { [extraKeys[i]]: value.buffer[extraKeys[i]] };
          formatted = formatExtraProperties(ctx, tempValue, recurseTimes, extraKeys[i], typedArray);
        }
        output.push(formatted);
      }
    }
    for (i = 0; i < keys.length; i++) {
      output.push(formatProperty(ctx, value, recurseTimes, keys[i], extrasType));
    }
    if (protoProps !== undefined) output.push(...protoProps);
  } catch (err) {
    if (!(err instanceof RangeError) || !/call stack/i.test(String(err.message))) throw err;
    const constructorName = getCtxStyle(value, constructor, tag).slice(0, -1);
    ctx.seen.pop();
    ctx.indentationLvl = indentationLvl;
    return ctx.stylize(
      `[${constructorName}: Inspection interrupted prematurely. Maximum call stack size exceeded.]`,
      "special",
    );
  }
  if (ctx.circular !== undefined) {
    const index = ctx.circular.get(value);
    if (index !== undefined) {
      const reference = ctx.stylize(`<ref *${index}>`, "special");
      if (ctx.compact !== true) base = base === "" ? reference : `${reference} ${base}`;
      else braces[0] = `${reference} ${braces[0]}`;
    }
  }
  ctx.seen.pop();

  if (ctx.sorted) {
    const comparator = ctx.sorted === true ? undefined : ctx.sorted;
    if (extrasType === kObjectType) {
      output.sort(comparator);
    } else if (keys.length > 1) {
      const sorted = output.slice(output.length - keys.length).sort(comparator);
      output.splice(output.length - keys.length, keys.length, ...sorted);
    }
  }

  const res = reduceToSingleString(ctx, output, base, braces, extrasType, recurseTimes, value);
  const budget = ctx.budget[ctx.indentationLvl] || 0;
  const newLength = budget + res.length;
  ctx.budget[ctx.indentationLvl] = newLength;
  if (newLength > 2 ** 27) ctx.depth = -1;
  return res;
}

function isURL(value) {
  const URLCtor = globalThis.URL;
  return typeof URLCtor === "function" && typeof value.href === "string" && isInstanceof(value, URLCtor);
}

function getIteratorBraces(type, tag) {
  if (tag !== `${type} Iterator`) {
    if (tag !== "") tag += "] [";
    tag += `${type} Iterator`;
  }
  return [`[${tag}] {`, "}"];
}

function getBoxedBase(value, ctx, keys, constructor, tag) {
  let fn;
  let type;
  if (isNumberObject(value)) {
    fn = Number.prototype.valueOf;
    type = "Number";
  } else if (isStringObject(value)) {
    fn = String.prototype.valueOf;
    type = "String";
    keys.splice(0, value.length);
  } else if (isBooleanObject(value)) {
    fn = Boolean.prototype.valueOf;
    type = "Boolean";
  } else if (isBigIntObject(value)) {
    fn = BigInt.prototype.valueOf;
    type = "BigInt";
  } else {
    fn = Symbol.prototype.valueOf;
    type = "Symbol";
  }
  let base = `[${type}`;
  if (type !== constructor) {
    if (constructor === null) base += " (null prototype)";
    else base += ` (${constructor})`;
  }
  base += `: ${formatPrimitive(stylizeNoColor, fn.call(value), ctx)}]`;
  if (tag !== "" && tag !== constructor) base += ` [${tag}]`;
  if (keys.length !== 0 || ctx.stylize === stylizeNoColor) return base;
  return ctx.stylize(base, type.toLowerCase());
}

function getClassBase(value, constructor, tag) {
  const hasName = hasOwn(value, "name");
  const name = (hasName && value.name) || "(anonymous)";
  let base = `class ${name}`;
  if (constructor !== "Function" && constructor !== null) base += ` [${constructor}]`;
  if (tag !== "" && constructor !== tag) base += ` [${tag}]`;
  if (constructor !== null) {
    const superName = Object.getPrototypeOf(value).name;
    if (superName) base += ` extends ${superName}`;
  } else {
    base += " extends [null prototype]";
  }
  return `[${base}]`;
}

function getFunctionBase(ctx, value, constructor, tag) {
  let stringified = "";
  try {
    stringified = Function.prototype.toString.call(value);
  } catch {}
  if (stringified.startsWith("class") && stringified[stringified.length - 1] === "}") {
    const slice = stringified.slice(5, -1);
    const bracketIndex = slice.indexOf("{");
    if (
      bracketIndex !== -1 &&
      (!slice.slice(0, bracketIndex).includes("(") ||
        classRegExp.exec(slice.replace(stripCommentsRegExp, "")) !== null)
    ) {
      return getClassBase(value, constructor, tag);
    }
  }
  let type = "Function";
  if (isGeneratorFunction(value)) type = `Generator${type}`;
  if (isAsyncFunction(value)) type = `Async${type}`;
  let base = `[${type}`;
  if (constructor === null) base += " (null prototype)";
  if (value.name === "") base += " (anonymous)";
  else base += `: ${typeof value.name === "string" ? value.name : formatValue(ctx, value.name)}`;
  base += "]";
  if (constructor !== type && constructor !== null) base += ` ${constructor}`;
  if (tag !== "" && constructor !== tag) base += ` [${tag}]`;
  return base;
}

export function identicalSequenceRange(a, b) {
  for (let i = 0; i < a.length - 3; i++) {
    const pos = b.indexOf(a[i]);
    if (pos !== -1) {
      const rest = b.length - pos;
      if (rest > 3) {
        let len = 1;
        const maxLen = Math.min(a.length - i, rest);
        while (maxLen > len && a[i + len] === b[pos + len]) len++;
        if (len > 3) return [len, i];
      }
    }
  }
  return [0, 0];
}

function getDuplicateErrorFrameRanges(frames) {
  const result = [];
  const lineToPositions = new Map();
  for (let i = 0; i < frames.length; i++) {
    const positions = lineToPositions.get(frames[i]);
    if (positions === undefined) lineToPositions.set(frames[i], [i]);
    else positions[positions.length] = i;
  }
  const minimumDuplicateRange = 3;
  if (frames.length - lineToPositions.size <= minimumDuplicateRange) return result;
  for (let i = 0; i < frames.length - minimumDuplicateRange; i++) {
    const positions = lineToPositions.get(frames[i]);
    if (positions.length === 1 || positions[positions.length - 1] === i) continue;
    const current = positions.indexOf(i) + 1;
    if (current === positions.length) continue;
    let range = positions[positions.length - 1] - i;
    if (range < minimumDuplicateRange) continue;
    let extraSteps;
    if (current + 1 < positions.length) {
      let gcdRange = 0;
      for (let j = current; j < positions.length; j++) {
        let distance = positions[j] - i;
        while (distance !== 0) {
          const remainder = gcdRange % distance;
          if (gcdRange !== 0) {
            extraSteps ??= new Set();
            extraSteps.add(gcdRange);
          }
          gcdRange = distance;
          distance = remainder;
        }
        if (gcdRange === 1) break;
      }
      range = gcdRange;
      if (extraSteps) {
        extraSteps.delete(range);
        extraSteps = [...extraSteps];
      }
    }
    let maxRange = range;
    let maxDuplicates = 0;
    let duplicateRanges = 0;
    for (let nextStart = i + range; ; nextStart += range) {
      let equalFrames = 0;
      for (let j = 0; j < range; j++) {
        if (frames[i + j] !== frames[nextStart + j]) break;
        equalFrames++;
      }
      if (equalFrames !== range) {
        if (!extraSteps?.length) break;
        if (duplicateRanges !== 0 && maxRange * maxDuplicates < range * duplicateRanges) {
          maxRange = range;
          maxDuplicates = duplicateRanges;
        }
        range = extraSteps.pop();
        nextStart = i;
        duplicateRanges = 0;
        continue;
      }
      duplicateRanges++;
    }
    if (maxDuplicates !== 0 && maxRange * maxDuplicates >= range * duplicateRanges) {
      range = maxRange;
      duplicateRanges = maxDuplicates;
    }
    if (duplicateRanges * range >= 3) {
      result.push(i + range, range, duplicateRanges);
      i += range * (duplicateRanges + 1) - 1;
    }
  }
  return result;
}

// The engine's error.stack holds only the frames; V8's starts with
// "Name: message". Re-add that header so stacks print as in Node.
function withStackHeader(error, stack) {
  if (stack.startsWith("    at ") || stack === "") {
    let header;
    try {
      header = Error.prototype.toString.call(error);
    } catch {
      header = "Error";
    }
    return stack === "" ? header : `${header}\n${stack}`;
  }
  return stack;
}

function getStackString(ctx, error) {
  let stack;
  try {
    stack = error.stack;
  } catch {}
  if (stack) {
    if (typeof stack === "string") return withStackHeader(error, stack);
    ctx.seen.push(error);
    ctx.indentationLvl += 4;
    const result = formatValue(ctx, stack);
    ctx.indentationLvl -= 4;
    ctx.seen.pop();
    return `${Error.prototype.toString.call(error)}\n    ${result}`;
  }
  return Error.prototype.toString.call(error);
}

function getStackFrames(ctx, err, stack) {
  const frames = stack.split("\n");
  let cause;
  try {
    ({ cause } = err);
  } catch {}
  if (cause != null && isError(cause)) {
    const causeStack = getStackString(ctx, cause);
    const causeStackStart = causeStack.indexOf("\n    at");
    if (causeStackStart !== -1) {
      const causeFrames = causeStack.slice(causeStackStart + 1).split("\n");
      const [len, offset] = identicalSequenceRange(frames, causeFrames);
      if (len > 0) {
        const skipped = len - 2;
        const msg = `    ... ${skipped} lines matching cause stack trace ...`;
        frames.splice(offset + 1, skipped, ctx.stylize(msg, "undefined"));
      }
    }
  }
  if (frames.length > 10) {
    const ranges = getDuplicateErrorFrameRanges(frames);
    for (let i = ranges.length - 3; i >= 0; i -= 3) {
      const offset = ranges[i];
      const length = ranges[i + 1];
      const duplicateRanges = ranges[i + 2];
      const msg =
        `    ... collapsed ${length * duplicateRanges} duplicate lines matching above ` +
        (duplicateRanges > 1 ? `${length} lines ${duplicateRanges} times...` : "lines ...");
      frames.splice(offset, length * duplicateRanges, ctx.stylize(msg, "undefined"));
    }
  }
  return frames;
}

function improveStack(stack, constructor, name, tag) {
  let len = name.length;
  if (typeof name !== "string") {
    stack = stack.replace(`${name}`, `${name} [${getPrefix(constructor, tag, "Error").slice(0, -1)}]`);
  }
  if (
    constructor === null ||
    (name.endsWith("Error") &&
      stack.startsWith(name) &&
      (stack.length === len || stack[len] === ":" || stack[len] === "\n"))
  ) {
    let fallback = "Error";
    if (constructor === null) {
      const start =
        /^([A-Z][a-z_ A-Z0-9[\]()-]+)(?::|\n {4}at)/.exec(stack) || /^([a-z_A-Z0-9-]*Error)$/.exec(stack);
      fallback = start?.[1] || "";
      len = fallback.length;
      fallback ||= "Error";
    }
    const prefix = getPrefix(constructor, tag, fallback).slice(0, -1);
    if (name !== prefix) {
      if (prefix.includes(name)) {
        if (len === 0) stack = `${prefix}: ${stack}`;
        else stack = `${prefix}${stack.slice(len)}`;
      } else {
        stack = `${prefix} [${name}]${stack.slice(len)}`;
      }
    }
  }
  return stack;
}

function markNodeModules(ctx, line) {
  let tempLine = "";
  let lastPos = 0;
  let searchFrom = 0;
  while (true) {
    const nodeModulePosition = line.indexOf("node_modules", searchFrom);
    if (nodeModulePosition === -1) break;
    const separator = line[nodeModulePosition - 1];
    const after = line[nodeModulePosition + 12];
    if ((after !== "/" && after !== "\\") || (separator !== "/" && separator !== "\\")) {
      searchFrom = nodeModulePosition + 1;
      continue;
    }
    const moduleStart = nodeModulePosition + 13;
    tempLine += line.slice(lastPos, moduleStart);
    let moduleEnd = line.indexOf(separator, moduleStart);
    if (line[moduleStart] === "@") moduleEnd = line.indexOf(separator, moduleEnd + 1);
    const nodeModule = line.slice(moduleStart, moduleEnd);
    tempLine += ctx.stylize(nodeModule, "module");
    lastPos = moduleEnd;
    searchFrom = moduleEnd;
  }
  if (lastPos !== 0) line = tempLine + line.slice(lastPos);
  return line;
}

function markCwd(ctx, line, workingDirectory) {
  let cwdStartPos = line.indexOf(workingDirectory);
  let tempLine = "";
  let cwdLength = workingDirectory.length;
  if (cwdStartPos !== -1) {
    if (line.slice(cwdStartPos - 7, cwdStartPos) === "file://") {
      cwdLength += 7;
      cwdStartPos -= 7;
    }
    const start = line[cwdStartPos - 1] === "(" ? cwdStartPos - 1 : cwdStartPos;
    const end = start !== cwdStartPos && line.endsWith(")") ? -1 : line.length;
    const workingDirectoryEndPos = cwdStartPos + cwdLength + 1;
    const cwdSlice = line.slice(start, workingDirectoryEndPos);
    tempLine += line.slice(0, start);
    tempLine += ctx.stylize(cwdSlice, "undefined");
    tempLine += line.slice(workingDirectoryEndPos, end);
    if (end === -1) tempLine += ctx.stylize(")", "undefined");
  } else {
    tempLine += line;
  }
  return tempLine;
}

function safeGetCWD() {
  try {
    return process.cwd();
  } catch {
    return undefined;
  }
}

function formatError(err, constructor, tag, ctx, keys) {
  let message, name, stack;
  try {
    stack = getStackString(ctx, err);
  } catch {
    return Object.prototype.toString.call(err);
  }
  let messageIsGetterThatThrows = false;
  try {
    message = err.message;
  } catch {
    messageIsGetterThatThrows = true;
  }
  let nameIsGetterThatThrows = false;
  try {
    name = err.name;
  } catch {
    nameIsGetterThatThrows = true;
  }
  if (!ctx.showHidden && keys.length !== 0) {
    const index = keys.indexOf("stack");
    if (index !== -1) keys.splice(index, 1);
    if (!messageIsGetterThatThrows) {
      const index = keys.indexOf("message");
      if (index !== -1 && (typeof message !== "string" || stack.includes(message))) keys.splice(index, 1);
    }
    if (!nameIsGetterThatThrows) {
      const index = keys.indexOf("name");
      if (index !== -1 && (typeof name !== "string" || stack.includes(name))) keys.splice(index, 1);
    }
  }
  name ??= "Error";
  if (hasOwn(err, "cause") && (keys.length === 0 || !keys.includes("cause"))) keys.push("cause");
  try {
    const errors = err.errors;
    if (Array.isArray(errors) && hasOwn(err, "errors") && (keys.length === 0 || !keys.includes("errors"))) {
      keys.push("errors");
    }
  } catch {}

  stack = improveStack(stack, constructor, name, tag);

  let pos = (message && stack.indexOf(message)) || -1;
  if (pos !== -1) pos += message.length;
  const stackStart = stack.indexOf("\n    at", pos);
  if (stackStart === -1) {
    stack = `[${stack}]`;
  } else {
    let newStack = stack.slice(0, stackStart);
    const stackFramePart = stack.slice(stackStart + 1);
    const lines = getStackFrames(ctx, err, stackFramePart);
    if (ctx.colors) {
      const workingDirectory = safeGetCWD();
      let esmWorkingDirectory;
      for (let line of lines) {
        const core = coreModuleRegExp.exec(line);
        if (core !== null) {
          newStack += `\n${ctx.stylize(line, "undefined")}`;
        } else {
          newStack += "\n";
          line = markNodeModules(ctx, line);
          if (workingDirectory !== undefined) {
            let newLine = markCwd(ctx, line, workingDirectory);
            if (newLine === line) {
              esmWorkingDirectory ??= "file://" + encodeURI(workingDirectory);
              newLine = markCwd(ctx, line, esmWorkingDirectory);
            }
            line = newLine;
          }
          newStack += line;
        }
      }
    } else {
      newStack += `\n${lines.join("\n")}`;
    }
    stack = newStack;
  }
  if (ctx.indentationLvl !== 0) {
    const indentation = " ".repeat(ctx.indentationLvl);
    stack = stack.replaceAll("\n", `\n${indentation}`);
  }
  return stack;
}

function groupArrayElements(ctx, output, value) {
  let totalLength = 0;
  let maxLength = 0;
  let i = 0;
  let outputLength = output.length;
  if (ctx.maxArrayLength < output.length) outputLength--;
  const separatorSpace = 2;
  const dataLen = new Array(outputLength);
  for (; i < outputLength; i++) {
    const len = getStringWidth(output[i], ctx.colors);
    dataLen[i] = len;
    totalLength += len + separatorSpace;
    if (maxLength < len) maxLength = len;
  }
  const actualMax = maxLength + separatorSpace;
  if (actualMax * 3 + ctx.indentationLvl < ctx.breakLength && (totalLength / actualMax > 5 || maxLength <= 6)) {
    const approxCharHeights = 2.5;
    const averageBias = Math.sqrt(actualMax - totalLength / output.length);
    const biasedMax = Math.max(actualMax - 3 - averageBias, 1);
    const columns = Math.min(
      Math.round(Math.sqrt(approxCharHeights * biasedMax * outputLength) / biasedMax),
      Math.floor((ctx.breakLength - ctx.indentationLvl) / actualMax),
      ctx.compact * 4,
      15,
    );
    if (columns <= 1) return output;
    const tmp = [];
    const maxLineLength = [];
    for (let i = 0; i < columns; i++) {
      let lineMaxLength = 0;
      for (let j = i; j < output.length; j += columns) {
        if (dataLen[j] > lineMaxLength) lineMaxLength = dataLen[j];
      }
      maxLineLength[i] = lineMaxLength + separatorSpace;
    }
    let padStart = true;
    if (value !== undefined) {
      for (let i = 0; i < output.length; i++) {
        if (typeof value[i] !== "number" && typeof value[i] !== "bigint") {
          padStart = false;
          break;
        }
      }
    }
    for (let i = 0; i < outputLength; i += columns) {
      const max = Math.min(i + columns, outputLength);
      let str = "";
      let j = i;
      for (; j < max - 1; j++) {
        const padding = maxLineLength[j - i] + output[j].length - dataLen[j];
        str += padStart ? `${output[j]}, `.padStart(padding, " ") : `${output[j]}, `.padEnd(padding, " ");
      }
      if (padStart) {
        const padding = maxLineLength[j - i] + output[j].length - dataLen[j] - separatorSpace;
        str += output[j].padStart(padding, " ");
      } else {
        str += output[j];
      }
      tmp.push(str);
    }
    if (ctx.maxArrayLength < output.length) tmp.push(output[outputLength]);
    output = tmp;
  }
  return output;
}

function addNumericSeparator(integerString) {
  let result = "";
  let i = integerString.length;
  const start = integerString[0] === "-" ? 1 : 0;
  for (; i >= start + 4; i -= 3) result = `_${integerString.slice(i - 3, i)}${result}`;
  return i === integerString.length ? integerString : `${integerString.slice(0, i)}${result}`;
}

function addNumericSeparatorEnd(integerString) {
  let result = "";
  let i = 0;
  for (; i < integerString.length - 3; i += 3) result += `${integerString.slice(i, i + 3)}_`;
  return i === 0 ? integerString : `${result}${integerString.slice(i)}`;
}

const remainingText = (remaining) => `... ${remaining} more item${remaining > 1 ? "s" : ""}`;

function formatNumber(fn, number, numericSeparator) {
  if (!numericSeparator) {
    if (Object.is(number, -0)) return fn("-0", "number");
    return fn(`${number}`, "number");
  }
  const numberString = String(number);
  const integer = Math.trunc(number);
  if (integer === number) {
    if (!Number.isFinite(number) || numberString.includes("e")) return fn(numberString, "number");
    return fn(addNumericSeparator(numberString), "number");
  }
  if (Number.isNaN(number)) return fn(numberString, "number");
  const decimalIndex = numberString.indexOf(".");
  const integerPart = numberString.slice(0, decimalIndex);
  const fractionalPart = numberString.slice(decimalIndex + 1);
  return fn(`${addNumericSeparator(integerPart)}.${addNumericSeparatorEnd(fractionalPart)}`, "number");
}

function formatBigInt(fn, bigint, numericSeparator) {
  const string = String(bigint);
  if (!numericSeparator) return fn(`${string}n`, "bigint");
  return fn(`${addNumericSeparator(string)}n`, "bigint");
}

// Splits after each newline, keeping it (String.split(/(?<=\n)/)).
function splitLinesKeep(value) {
  const out = [];
  let start = 0;
  for (;;) {
    const nl = value.indexOf("\n", start);
    if (nl === -1 || nl === value.length - 1) {
      out.push(value.slice(start));
      return out;
    }
    out.push(value.slice(start, nl + 1));
    start = nl + 1;
  }
}

function formatPrimitive(fn, value, ctx) {
  if (typeof value === "string") {
    let trailer = "";
    if (value.length > ctx.maxStringLength) {
      const remaining = value.length - ctx.maxStringLength;
      value = value.slice(0, ctx.maxStringLength);
      trailer = `... ${remaining} more character${remaining > 1 ? "s" : ""}`;
    }
    if (ctx.compact !== true && value.length > kMinLineLength && value.length > ctx.breakLength - ctx.indentationLvl - 4) {
      return (
        splitLinesKeep(value)
          .map((line) => fn(strEscape(line), "string"))
          .join(` +\n${" ".repeat(ctx.indentationLvl + 2)}`) + trailer
      );
    }
    return fn(strEscape(value), "string") + trailer;
  }
  if (typeof value === "number") return formatNumber(fn, value, ctx.numericSeparator);
  if (typeof value === "bigint") return formatBigInt(fn, value, ctx.numericSeparator);
  if (typeof value === "boolean") return fn(`${value}`, "boolean");
  if (typeof value === "undefined") return fn("undefined", "undefined");
  return fn(Symbol.prototype.toString.call(value), "symbol");
}

function formatNamespaceObject(keys, ctx, value, recurseTimes) {
  const output = new Array(keys.length);
  for (let i = 0; i < keys.length; i++) {
    try {
      output[i] = formatProperty(ctx, value, recurseTimes, keys[i], kObjectType);
    } catch {
      const tmp = { [keys[i]]: "" };
      output[i] = formatProperty(ctx, tmp, recurseTimes, keys[i], kObjectType);
      const pos = output[i].lastIndexOf(" ");
      output[i] = output[i].slice(0, pos + 1) + ctx.stylize("<uninitialized>", "special");
    }
  }
  keys.length = 0;
  return output;
}

function formatSpecialArray(ctx, value, recurseTimes, maxLength, output, i) {
  const keys = Object.keys(value);
  let index = i;
  for (; i < keys.length && output.length < maxLength; i++) {
    const key = keys[i];
    const tmp = +key;
    if (tmp > 2 ** 32 - 2) break;
    if (`${index}` !== key) {
      if (numberRegExp.exec(key) === null) break;
      const emptyItems = tmp - index;
      const ending = emptyItems > 1 ? "s" : "";
      output.push(ctx.stylize(`<${emptyItems} empty item${ending}>`, "undefined"));
      index = tmp;
      if (output.length === maxLength) break;
    }
    output.push(formatProperty(ctx, value, recurseTimes, key, kArrayType));
    index++;
  }
  const remaining = value.length - index;
  if (output.length !== maxLength) {
    if (remaining > 0) {
      const ending = remaining > 1 ? "s" : "";
      output.push(ctx.stylize(`<${remaining} empty item${ending}>`, "undefined"));
    }
  } else if (remaining > 0) {
    output.push(remainingText(remaining));
  }
  return output;
}

function hexSlice(bytes, end) {
  let s = "";
  for (let i = 0; i < end; i++) s += bytes[i].toString(16).padStart(2, "0");
  return s;
}

function formatArrayBuffer(ctx, value) {
  let buffer;
  try {
    buffer = new Uint8Array(value);
  } catch {
    return [ctx.stylize("(detached)", "special")];
  }
  const rawString = hexSlice(buffer, Math.min(ctx.maxArrayLength, buffer.length));
  let str = "";
  let i = 0;
  for (; i < rawString.length - 2; i += 2) str += `${rawString[i]}${rawString[i + 1]} `;
  if (rawString.length > 0) str += `${rawString[i]}${rawString[i + 1]}`;
  const remaining = buffer.length - ctx.maxArrayLength;
  if (remaining > 0) str += ` ... ${remaining} more byte${remaining > 1 ? "s" : ""}`;
  return [`${ctx.stylize("[Uint8Contents]", "special")}: <${str}>`];
}

function formatArray(ctx, value, recurseTimes) {
  const valLen = value.length;
  const len = Math.min(Math.max(0, ctx.maxArrayLength), valLen);
  const remaining = valLen - len;
  const output = [];
  for (let i = 0; i < len; i++) {
    const desc = Object.getOwnPropertyDescriptor(value, i);
    if (desc === undefined) return formatSpecialArray(ctx, value, recurseTimes, len, output, i);
    output.push(formatProperty(ctx, value, recurseTimes, i, kArrayType, desc));
  }
  if (remaining > 0) output.push(remainingText(remaining));
  return output;
}

function formatTypedArray(value, length, ctx) {
  const maxLength = Math.min(Math.max(0, ctx.maxArrayLength), length);
  const remaining = value.length - maxLength;
  const output = new Array(maxLength);
  const elementFormatter = value.length > 0 && typeof value[0] === "number" ? formatNumber : formatBigInt;
  for (let i = 0; i < maxLength; ++i) output[i] = elementFormatter(ctx.stylize, value[i], ctx.numericSeparator);
  if (remaining > 0) output[maxLength] = remainingText(remaining);
  return output;
}

function formatSet(value, ctx, ignored, recurseTimes) {
  const length = isSet(value) ? value.size : Infinity;
  const output = [];
  ctx.indentationLvl += 2;
  let i = 0;
  let total = 0;
  const maxLength = Math.max(0, ctx.maxArrayLength);
  for (const v of value) {
    total++;
    if (i >= maxLength) continue;
    output.push(formatValue(ctx, v, recurseTimes));
    i++;
  }
  const remaining = (length === Infinity ? total : length) - i;
  if (remaining > 0) output.push(remainingText(remaining));
  ctx.indentationLvl -= 2;
  return output;
}

function formatMap(value, ctx, ignored, recurseTimes) {
  const length = isMap(value) ? value.size : Infinity;
  const output = [];
  ctx.indentationLvl += 2;
  let i = 0;
  let total = 0;
  const maxLength = Math.max(0, ctx.maxArrayLength);
  // (Indexed rather than destructured: the engine mis-binds
  // `for (const { 0: k, 1: v } of map)`.)
  for (const entry of value) {
    const k = entry[0];
    const v = entry[1];
    total++;
    if (i >= maxLength) continue;
    output.push(`${formatValue(ctx, k, recurseTimes)} => ${formatValue(ctx, v, recurseTimes)}`);
    i++;
  }
  const remaining = (length === Infinity ? total : length) - i;
  if (remaining > 0) output.push(remainingText(remaining));
  ctx.indentationLvl -= 2;
  return output;
}

function formatSetIterInner(ctx, recurseTimes, entries, state) {
  const maxArrayLength = Math.max(ctx.maxArrayLength, 0);
  const maxLength = Math.min(maxArrayLength, entries.length);
  const output = new Array(maxLength);
  ctx.indentationLvl += 2;
  for (let i = 0; i < maxLength; i++) output[i] = formatValue(ctx, entries[i], recurseTimes);
  ctx.indentationLvl -= 2;
  if (state === kWeak && !ctx.sorted) output.sort();
  const remaining = entries.length - maxLength;
  if (remaining > 0) output.push(remainingText(remaining));
  return output;
}

function formatMapIterInner(ctx, recurseTimes, entries, state) {
  const maxArrayLength = Math.max(ctx.maxArrayLength, 0);
  const len = entries.length / 2;
  const remaining = len - maxArrayLength;
  const maxLength = Math.min(maxArrayLength, len);
  const output = new Array(maxLength);
  let i = 0;
  ctx.indentationLvl += 2;
  if (state === kWeak) {
    for (; i < maxLength; i++) {
      const pos = i * 2;
      output[i] = `${formatValue(ctx, entries[pos], recurseTimes)} => ${formatValue(ctx, entries[pos + 1], recurseTimes)}`;
    }
    if (!ctx.sorted) output.sort();
  } else {
    for (; i < maxLength; i++) {
      const pos = i * 2;
      const res = [formatValue(ctx, entries[pos], recurseTimes), formatValue(ctx, entries[pos + 1], recurseTimes)];
      output[i] = reduceToSingleString(ctx, res, "", ["[", "]"], kArrayExtrasType, recurseTimes);
    }
  }
  ctx.indentationLvl -= 2;
  if (remaining > 0) output.push(remainingText(remaining));
  return output;
}

function formatWeakCollection(ctx) {
  return [ctx.stylize("<items unknown>", "special")];
}

function formatWeakSet(ctx, value, recurseTimes) {
  const entries = previewEntries(value, false);
  if (entries === undefined) return formatWeakCollection(ctx);
  return formatSetIterInner(ctx, recurseTimes, entries, kWeak);
}

function formatWeakMap(ctx, value, recurseTimes) {
  const entries = previewEntries(value, false);
  if (entries === undefined) return formatWeakCollection(ctx);
  return formatMapIterInner(ctx, recurseTimes, entries, kWeak);
}

function formatIterator(braces, ctx, value, recurseTimes) {
  const { 0: entries, 1: isKeyValue } = previewEntries(value, true);
  if (isKeyValue) {
    braces[0] = braces[0].replace(/ Iterator] {$/, " Entries] {");
    return formatMapIterInner(ctx, recurseTimes, entries, kMapEntries);
  }
  return formatSetIterInner(ctx, recurseTimes, entries, kIterator);
}

function formatPromise(ctx, value, recurseTimes) {
  let output;
  const { 0: state, 1: result } = getPromiseDetails(value);
  if (state === kPending) {
    output = [ctx.stylize("<pending>", "special")];
  } else {
    ctx.indentationLvl += 2;
    const str = formatValue(ctx, result, recurseTimes);
    ctx.indentationLvl -= 2;
    output = [state === kRejected ? `${ctx.stylize("<rejected>", "special")} ${str}` : str];
  }
  return output;
}

function formatExtraProperties(ctx, value, recurseTimes, key, typedArray) {
  ctx.indentationLvl += 2;
  const str = formatValue(ctx, value[key], recurseTimes, typedArray);
  ctx.indentationLvl -= 2;
  const name = ctx.stylize(`[${key}]`, "string");
  return `${name}: ${str}`;
}

function formatProperty(ctx, value, recurseTimes, key, type, desc, original = value) {
  let name, str;
  let extra = " ";
  desc ??= Object.getOwnPropertyDescriptor(value, key) || { value: value[key], enumerable: true };
  if (desc.value !== undefined) {
    const diff = ctx.compact !== true || type !== kObjectType ? 2 : 3;
    ctx.indentationLvl += diff;
    str = formatValue(ctx, desc.value, recurseTimes);
    if (diff === 3 && ctx.breakLength < getStringWidth(str, ctx.colors)) {
      extra = `\n${" ".repeat(ctx.indentationLvl)}`;
    }
    ctx.indentationLvl -= diff;
  } else if (desc.get !== undefined) {
    const label = desc.set !== undefined ? "Getter/Setter" : "Getter";
    const s = ctx.stylize;
    const sp = "special";
    if (
      ctx.getters &&
      (ctx.getters === true ||
        (ctx.getters === "get" && desc.set === undefined) ||
        (ctx.getters === "set" && desc.set !== undefined))
    ) {
      ctx.indentationLvl += 2;
      try {
        const tmp = desc.get.call(original);
        if (tmp === null) {
          str = `${s(`[${label}:`, sp)} ${s("null", "null")}${s("]", sp)}`;
        } else if (typeof tmp === "object") {
          str = `${s(`[${label}]`, sp)} ${formatValue(ctx, tmp, recurseTimes)}`;
        } else {
          const primitive = formatPrimitive(s, tmp, ctx);
          str = `${s(`[${label}:`, sp)} ${primitive}${s("]", sp)}`;
        }
      } catch (err) {
        const message = `<Inspection threw (${formatValue(ctx, err, recurseTimes)})>`;
        str = `${s(`[${label}:`, sp)} ${message}${s("]", sp)}`;
      }
      ctx.indentationLvl -= 2;
    } else {
      str = ctx.stylize(`[${label}]`, sp);
    }
  } else if (desc.set !== undefined) {
    str = ctx.stylize("[Setter]", "special");
  } else {
    str = ctx.stylize("undefined", "undefined");
  }
  if (type === kArrayType) return str;
  if (typeof key === "symbol") {
    name = ctx.stylize(escapeSymbolDescription(Symbol.prototype.toString.call(key)), "symbol");
  } else if (keyStrRegExp.exec(key) !== null) {
    name = key === "__proto__" ? "['__proto__']" : ctx.stylize(key, "name");
  } else {
    name = ctx.stylize(strEscape(key), "string");
  }
  if (desc.enumerable === false) name = `[${name}]`;
  return `${name}:${extra}${str}`;
}

function isBelowBreakLength(ctx, output, start, base) {
  let totalLength = output.length + start;
  if (totalLength + output.length > ctx.breakLength) return false;
  for (let i = 0; i < output.length; i++) {
    if (ctx.colors) totalLength += stripVTControlCharacters(output[i]).length;
    else totalLength += output[i].length;
    if (totalLength > ctx.breakLength) return false;
  }
  return base === "" || !base.includes("\n");
}

function reduceToSingleString(ctx, output, base, braces, extrasType, recurseTimes, value) {
  if (ctx.compact !== true) {
    if (typeof ctx.compact === "number" && ctx.compact >= 1) {
      const entries = output.length;
      if (extrasType === kArrayExtrasType && entries > 6) output = groupArrayElements(ctx, output, value);
      if (ctx.currentDepth - recurseTimes < ctx.compact && entries === output.length) {
        const start = output.length + ctx.indentationLvl + braces[0].length + base.length + 10;
        if (isBelowBreakLength(ctx, output, start, base)) {
          const joinedOutput = output.join(", ");
          if (!joinedOutput.includes("\n")) {
            return `${base ? `${base} ` : ""}${braces[0]} ${joinedOutput}` + ` ${braces[1]}`;
          }
        }
      }
    }
    const indentation = `\n${" ".repeat(ctx.indentationLvl)}`;
    return `${base ? `${base} ` : ""}${braces[0]}${indentation}  ${output.join(`,${indentation}  `)}${indentation}${braces[1]}`;
  }
  if (isBelowBreakLength(ctx, output, 0, base)) {
    return `${braces[0]}${base ? ` ${base}` : ""} ${output.join(", ")} ` + braces[1];
  }
  const indentation = " ".repeat(ctx.indentationLvl);
  const ln = base === "" && braces[0].length === 1 ? " " : `${base ? ` ${base}` : ""}\n${indentation}  `;
  return `${braces[0]}${ln}${output.join(`,\n${indentation}  `)} ${braces[1]}`;
}

function hasBuiltInToString(value) {
  const proxyTarget = getProxyDetails(value, false);
  if (proxyTarget !== undefined) {
    if (proxyTarget === null) return true;
    return hasBuiltInToString(proxyTarget);
  }
  let hasOwnToString = hasOwn;
  let hasOwnToPrimitive = hasOwn;
  if (typeof value.toString !== "function") {
    if (typeof value[Symbol.toPrimitive] !== "function") return true;
    else if (hasOwn(value, Symbol.toPrimitive)) return false;
    hasOwnToString = () => false;
  } else if (hasOwn(value, "toString")) {
    return false;
  } else if (typeof value[Symbol.toPrimitive] !== "function") {
    hasOwnToPrimitive = () => false;
  } else if (hasOwn(value, Symbol.toPrimitive)) {
    return false;
  }
  let pointer = value;
  do {
    pointer = Object.getPrototypeOf(pointer);
  } while (pointer !== null && !hasOwnToString(pointer, "toString") && !hasOwnToPrimitive(pointer, Symbol.toPrimitive));
  if (pointer === null) return true;
  const descriptor = Object.getOwnPropertyDescriptor(pointer, "constructor");
  return descriptor !== undefined && typeof descriptor.value === "function" && builtInObjects.has(descriptor.value.name);
}

const firstErrorLine = (error) => String(error.message).split("\n", 1)[0];
let CIRCULAR_ERROR_MESSAGE;
function tryStringify(arg) {
  try {
    return JSON.stringify(arg);
  } catch (err) {
    if (!CIRCULAR_ERROR_MESSAGE) {
      try {
        const a = {};
        a.a = a;
        JSON.stringify(a);
      } catch (circularError) {
        CIRCULAR_ERROR_MESSAGE = firstErrorLine(circularError);
      }
    }
    if (err.name === "TypeError" && firstErrorLine(err) === CIRCULAR_ERROR_MESSAGE) return "[Circular]";
    throw err;
  }
}

function receivedSuffix(v) {
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

export function format(...args) {
  return formatWithOptionsInternal(undefined, args);
}

export function formatWithOptions(inspectOptions, ...args) {
  if (inspectOptions === null || typeof inspectOptions !== "object") {
    const err = new TypeError('The "inspectOptions" argument must be of type object.' + receivedSuffix(inspectOptions));
    err.code = "ERR_INVALID_ARG_TYPE";
    throw err;
  }
  return formatWithOptionsInternal(inspectOptions, args);
}

function formatNumberNoColor(number, options) {
  return formatNumber(stylizeNoColor, number, options?.numericSeparator ?? inspectDefaultOptions.numericSeparator);
}

function formatBigIntNoColor(bigint, options) {
  return formatBigInt(stylizeNoColor, bigint, options?.numericSeparator ?? inspectDefaultOptions.numericSeparator);
}

// The global parseInt/parseFloat (the engine's Number.parseInt returns NaN
// for "12.5" instead of 12).
const specParseInt = (v) => parseInt(v);
const specParseFloat = (v) => parseFloat(v);

export function formatWithOptionsInternal(inspectOptions, args) {
  const first = args[0];
  let a = 0;
  let str = "";
  let join = "";
  if (typeof first === "string") {
    if (args.length === 1) return first;
    let tempStr;
    let lastPos = 0;
    for (let i = 0; i < first.length - 1; i++) {
      if (first.charCodeAt(i) === 37) {
        const nextChar = first.charCodeAt(++i);
        if (a + 1 !== args.length) {
          switch (nextChar) {
            case 115: {
              const tempArg = args[++a];
              if (typeof tempArg === "number") tempStr = formatNumberNoColor(tempArg, inspectOptions);
              else if (typeof tempArg === "bigint") tempStr = formatBigIntNoColor(tempArg, inspectOptions);
              else if (typeof tempArg !== "object" || tempArg === null || !hasBuiltInToString(tempArg)) tempStr = String(tempArg);
              else tempStr = inspect(tempArg, { ...inspectOptions, compact: 3, colors: false, depth: 0 });
              break;
            }
            case 106:
              tempStr = tryStringify(args[++a]);
              break;
            case 100: {
              const tempNum = args[++a];
              if (typeof tempNum === "bigint") tempStr = formatBigIntNoColor(tempNum, inspectOptions);
              else if (typeof tempNum === "symbol") tempStr = "NaN";
              else tempStr = formatNumberNoColor(Number(tempNum), inspectOptions);
              break;
            }
            case 79:
              tempStr = inspect(args[++a], inspectOptions);
              break;
            case 111:
              tempStr = inspect(args[++a], { ...inspectOptions, showHidden: true, showProxy: true, depth: 4 });
              break;
            case 105: {
              const tempInteger = args[++a];
              if (typeof tempInteger === "bigint") tempStr = formatBigIntNoColor(tempInteger, inspectOptions);
              else if (typeof tempInteger === "symbol") tempStr = "NaN";
              else tempStr = formatNumberNoColor(specParseInt(tempInteger), inspectOptions);
              break;
            }
            case 102: {
              const tempFloat = args[++a];
              if (typeof tempFloat === "symbol") tempStr = "NaN";
              else tempStr = formatNumberNoColor(specParseFloat(tempFloat), inspectOptions);
              break;
            }
            case 99:
              a += 1;
              tempStr = "";
              break;
            case 37:
              str += first.slice(lastPos, i);
              lastPos = i + 1;
              continue;
            default:
              continue;
          }
          if (lastPos !== i - 1) str += first.slice(lastPos, i - 1);
          str += tempStr;
          lastPos = i + 1;
        } else if (nextChar === 37) {
          str += first.slice(lastPos, i);
          lastPos = i + 1;
        }
      }
    }
    if (lastPos !== 0) {
      a++;
      join = " ";
      if (lastPos < first.length) str += first.slice(lastPos);
    }
  }
  while (a < args.length) {
    const value = args[a];
    str += join;
    str += typeof value !== "string" ? inspect(value, inspectOptions) : value;
    join = " ";
    a++;
  }
  return str;
}

export function isZeroWidthCodePoint(code) {
  return (
    code <= 0x1f ||
    (code >= 0x7f && code <= 0x9f) ||
    (code >= 0x300 && code <= 0x36f) ||
    (code >= 0x200b && code <= 0x200f) ||
    (code >= 0x20d0 && code <= 0x20ff) ||
    (code >= 0xfe00 && code <= 0xfe0f) ||
    (code >= 0xfe20 && code <= 0xfe2f) ||
    (code >= 0xe0100 && code <= 0xe01ef)
  );
}

function isFullWidthCodePoint(code) {
  return (
    code >= 0x1100 &&
    (code <= 0x115f ||
      code === 0x2329 ||
      code === 0x232a ||
      (code >= 0x2e80 && code <= 0x3247 && code !== 0x303f) ||
      (code >= 0x3250 && code <= 0x4dbf) ||
      (code >= 0x4e00 && code <= 0xa4c6) ||
      (code >= 0xa960 && code <= 0xa97c) ||
      (code >= 0xac00 && code <= 0xd7a3) ||
      (code >= 0xf900 && code <= 0xfaff) ||
      (code >= 0xfe10 && code <= 0xfe19) ||
      (code >= 0xfe30 && code <= 0xfe6b) ||
      (code >= 0xff01 && code <= 0xff60) ||
      (code >= 0xffe0 && code <= 0xffe6) ||
      (code >= 0x1b000 && code <= 0x1b001) ||
      (code >= 0x1f200 && code <= 0x1f251) ||
      (code >= 0x1f300 && code <= 0x1f64f) ||
      (code >= 0x20000 && code <= 0x3fffd))
  );
}

export function getStringWidth(str, removeControlChars = true) {
  let width = 0;
  if (removeControlChars) str = stripVTControlCharacters(str);
  let ascii = true;
  for (let i = 0; i < str.length; i++) {
    const code = str.charCodeAt(i);
    if (code >= 127) {
      ascii = false;
      break;
    }
    width += code >= 32 ? 1 : 0;
  }
  if (ascii) return width;
  width = 0;
  str = str.normalize("NFC");
  for (const char of str) {
    const code = char.codePointAt(0);
    if (isFullWidthCodePoint(code)) width += 2;
    else if (!isZeroWidthCodePoint(code)) width++;
  }
  return width;
}

export function stripVTControlCharacters(str) {
  if (typeof str !== "string") {
    const err = new TypeError('The "str" argument must be of type string.' + receivedSuffix(str));
    err.code = "ERR_INVALID_ARG_TYPE";
    throw err;
  }
  if (str.indexOf("\u001B") === -1 && str.indexOf("\u009B") === -1) return str;
  return str.replace(ansi, "");
}

// Buffer's own inspect hook, as lib/buffer.js defines it.
const BufferCtor = globalThis.Buffer;
if (typeof BufferCtor === "function" && !Object.prototype.hasOwnProperty.call(BufferCtor.prototype, customInspectSymbol)) {
  const bufferInspect = function inspectBuffer(recurseTimes, ctx) {
    const configured = bufferModule?.INSPECT_MAX_BYTES;
    const max = typeof configured === "number" ? configured : 50;
    const actualMax = Math.min(max, this.length);
    const remaining = this.length - max;
    let str = "";
    for (let i = 0; i < actualMax; i++) str += (i ? " " : "") + this[i].toString(16).padStart(2, "0");
    if (remaining > 0) str += ` ... ${remaining} more byte${remaining > 1 ? "s" : ""}`;
    if (ctx) {
      let extras = false;
      const obj = { __proto__: null };
      for (const key of getOwnNonIndexProperties(this, !ctx.showHidden)) {
        extras = true;
        obj[key] = this[key];
      }
      if (extras) {
        if (this.length !== 0) str += ", ";
        str += inspect(obj, { ...ctx, breakLength: Infinity, compact: true }).slice(27, -2);
      }
    }
    return `<${this.constructor.name} ${str}>`;
  };
  Object.defineProperty(BufferCtor.prototype, customInspectSymbol, {
    value: bufferInspect,
    writable: true,
    configurable: true,
    enumerable: false,
  });
}

package host

// utilExtraShim holds the node:util functions written in JS, loaded on
// first use (util_extra.go's installUtilLazyExtras). Each follows Node's
// own source: lib/util.js (callbackify, styleText, aborted, _extend,
// toUSVString), lib/internal/util/parse_args/parse_args.js (parseArgs).
const utilExtraShim = `import { isDeepStrictEqual as deepStrict } from "noderati-internal:comparisons";

function received(v) {
  if (v == null) return " Received " + v;
  if (typeof v === "function") return " Received function " + (v.name || "<anonymous>");
  if (typeof v === "object") return " Received an instance of " + ((v.constructor && v.constructor.name) || "Object");
  let s = typeof v === "string" ? "'" + v + "'" : String(v);
  if (typeof v === "string" && v.length > 28) s = "'" + v.slice(0, 25) + "'...";
  return " Received type " + typeof v + " (" + s + ")";
}
function invalidArgType(what, expected, v) {
  const err = new TypeError("The " + what + " must be " + expected + "." + received(v));
  err.code = "ERR_INVALID_ARG_TYPE";
  return err;
}
function argError(name, expected, v) {
  return invalidArgType('"' + name + '" ' + (name.includes(".") ? "property" : "argument"), expected, v);
}
function validateFunction(v, name) {
  if (typeof v !== "function") throw argError(name, "of type function", v);
}
function validateBoolean(v, name) {
  if (typeof v !== "boolean") throw argError(name, "of type boolean", v);
}
function validateString(v, name) {
  if (typeof v !== "string") throw argError(name, "of type string", v);
}
function validateObject(v, name) {
  if (v === null || typeof v !== "object" || Array.isArray(v)) throw argError(name, "of type object", v);
}
function validateArray(v, name) {
  if (!Array.isArray(v)) throw argError(name, "an instance of Array", v);
}

export function isDeepStrictEqual(a, b) {
  return deepStrict(a, b);
}

export function callbackify(original) {
  validateFunction(original, "original");
  function callbackified(...args) {
    const maybeCb = args.pop();
    validateFunction(maybeCb, "last argument");
    const cb = maybeCb.bind(this);
    Reflect.apply(original, this, args).then(
      (ret) => process.nextTick(cb, null, ret),
      (rej) => process.nextTick(callbackifyOnRejected, rej, cb),
    );
  }
  const descriptors = Object.getOwnPropertyDescriptors(original);
  if (descriptors.length && typeof descriptors.length.value === "number") descriptors.length.value++;
  if (descriptors.name && typeof descriptors.name.value === "string") descriptors.name.value += "Callbackified";
  Object.defineProperties(callbackified, descriptors);
  return callbackified;
}
function callbackifyOnRejected(reason, cb) {
  if (!reason) {
    const err = new Error("Promise was rejected with falsy value");
    err.code = "ERR_FALSY_VALUE_REJECTION";
    err.reason = reason;
    reason = err;
  }
  return cb(reason);
}

export function _extend(target, source) {
  if (source === null || typeof source !== "object") return target;
  const keys = Object.keys(source);
  let i = keys.length;
  while (i--) target[keys[i]] = source[keys[i]];
  return target;
}

export function toUSVString(input) {
  return String(input).toWellFormed();
}

export async function aborted(signal, resource) {
  if (signal === null || typeof signal !== "object" || !("aborted" in signal)) throw argError("signal", "an instance of AbortSignal", signal);
  if (resource === null || (typeof resource !== "object" && typeof resource !== "function")) throw argError("resource", "of type object", resource);
  if (signal.aborted) return;
  return new Promise((resolve) => signal.addEventListener("abort", resolve, { once: true }));
}

// styleText: util.inspect.colors' SGR pairs.
const colors = {
  reset: [0, 0], bold: [1, 22], dim: [2, 22], italic: [3, 23], underline: [4, 24], blink: [5, 25], inverse: [7, 27],
  hidden: [8, 28], strikethrough: [9, 29], doubleunderline: [21, 24], black: [30, 39], red: [31, 39], green: [32, 39],
  yellow: [33, 39], blue: [34, 39], magenta: [35, 39], cyan: [36, 39], white: [37, 39], bgBlack: [40, 49],
  bgRed: [41, 49], bgGreen: [42, 49], bgYellow: [43, 49], bgBlue: [44, 49], bgMagenta: [45, 49], bgCyan: [46, 49],
  bgWhite: [47, 49], framed: [51, 54], overlined: [53, 55], gray: [90, 39], redBright: [91, 39],
  greenBright: [92, 39], yellowBright: [93, 39], blueBright: [94, 39], magentaBright: [95, 39], cyanBright: [96, 39],
  whiteBright: [97, 39], bgGray: [100, 49], bgRedBright: [101, 49], bgGreenBright: [102, 49],
  bgYellowBright: [103, 49], bgBlueBright: [104, 49], bgMagentaBright: [105, 49], bgCyanBright: [106, 49],
  bgWhiteBright: [107, 49],
};
const colorAliases = {
  grey: "gray", blackBright: "gray", bgGrey: "bgGray", bgBlackBright: "bgGray", faint: "dim", crossedout: "strikethrough",
  strikeThrough: "strikethrough", crossedOut: "strikethrough", conceal: "hidden", swapColors: "inverse",
  swapcolors: "inverse", doubleUnderline: "doubleunderline",
};
for (const [alias, target] of Object.entries(colorAliases)) {
  Object.defineProperty(colors, alias, { get() { return this[target]; }, set(v) { this[target] = v; }, enumerable: false, configurable: true });
}

// Node's internal/tty getColorDepth: FORCE_COLOR first, then the terminal.
function colorDepthFromEnv(env) {
  const force = env.FORCE_COLOR;
  if (force !== undefined) {
    if (force === "" || force === "1" || force === "true") return 4;
    if (force === "2") return 8;
    if (force === "3") return 24;
    return 1;
  }
  return undefined;
}
function shouldColorize(stream) {
  const forced = colorDepthFromEnv(process.env);
  if (forced !== undefined) return forced > 2;
  if (!stream || !stream.isTTY) return false;
  return typeof stream.getColorDepth === "function" ? stream.getColorDepth() > 2 : true;
}
function isStreamLike(s) {
  return s !== null && typeof s === "object" && (typeof s.write === "function" || typeof s.pipe === "function" || typeof s.getReader === "function" || typeof s.getWriter === "function");
}

export function styleText(format, text, { validateStream = true, stream = process.stdout } = {}) {
  validateString(text, "text");
  validateBoolean(validateStream, "options.validateStream");
  let skipColorize;
  if (validateStream) {
    if (!isStreamLike(stream)) throw argError("stream", "an instance of ReadableStream, WritableStream, or Stream", stream);
    skipColorize = !shouldColorize(stream);
  }
  const formatArray = Array.isArray(format) ? format : [format];
  let left = "";
  let right = "";
  for (const key of formatArray) {
    if (key === "none") continue;
    const codes = colors[key];
    if (codes == null) {
      const allowed = Object.keys(colors).map((k) => "'" + k + "'").join(", ");
      const err = new TypeError("The argument 'format' must be one of: " + allowed + ". Received " + (typeof key === "string" ? "'" + key + "'" : String(key)));
      err.code = "ERR_INVALID_ARG_VALUE";
      throw err;
    }
    if (skipColorize) continue;
    left += "\x1b[" + codes[0] + "m";
    right = "\x1b[" + codes[1] + "m" + right;
  }
  return skipColorize ? text : left + text + right;
}

// parseArgs.
const objectGetOwn = (obj, prop) => (Object.hasOwn(obj, prop) ? obj[prop] : undefined);
const optionsGetOwn = (options, longOption, prop) =>
  Object.hasOwn(options, longOption) ? objectGetOwn(options[longOption], prop) : undefined;
const isOptionValue = (value) => value != null;
const isOptionLikeValue = (value) => value != null && value.length > 1 && value[0] === "-";
const isLoneShortOption = (arg) => arg.length === 2 && arg[0] === "-" && arg[1] !== "-";
const isLoneLongOption = (arg) => arg.length > 2 && arg.startsWith("--") && !arg.includes("=", 3);
const isLongOptionAndValue = (arg) => arg.length > 2 && arg.startsWith("--") && arg.includes("=", 3);
function findLongOptionForShort(shortOption, options) {
  const entry = Object.entries(options).find(([, config]) => objectGetOwn(config, "short") === shortOption);
  return entry ? entry[0] : shortOption;
}
function isShortOptionGroup(arg, options) {
  if (arg.length <= 2 || arg[0] !== "-" || arg[1] === "-") return false;
  return optionsGetOwn(options, findLongOptionForShort(arg[1], options), "type") !== "string";
}
function isShortOptionAndValue(arg, options) {
  if (arg.length <= 2 || arg[0] !== "-" || arg[1] === "-") return false;
  return optionsGetOwn(options, findLongOptionForShort(arg[1], options), "type") === "string";
}

function parseArgsError(code, message) {
  const err = new TypeError(message);
  err.code = code;
  return err;
}

function checkOptionLikeValue(token) {
  if (!token.inlineValue && isOptionLikeValue(token.value)) {
    const example = token.rawName.startsWith("--")
      ? "'" + token.rawName + "=-XYZ'"
      : "'--" + token.name + "=-XYZ' or '" + token.rawName + "-XYZ'";
    throw parseArgsError("ERR_PARSE_ARGS_INVALID_OPTION_VALUE", "Option '" + token.rawName + "' argument is ambiguous.\n" +
      "Did you forget to specify the option argument for '" + token.rawName + "'?\n" +
      "To specify an option argument starting with a dash use " + example + ".");
  }
}

function unknownOption(option, allowPositionals) {
  let suggest = "";
  if (allowPositionals) {
    suggest = ". To specify a positional argument starting with a '-', place it at the end of the command after '--', as in '-- " + JSON.stringify(option);
  }
  return parseArgsError("ERR_PARSE_ARGS_UNKNOWN_OPTION", "Unknown option '" + option + "'" + suggest);
}

function checkOptionUsage(config, token) {
  let tokenName = token.name;
  if (!Object.hasOwn(config.options, tokenName)) {
    if (config.allowNegative && tokenName.startsWith("no-")) {
      tokenName = tokenName.slice(3);
      if (!Object.hasOwn(config.options, tokenName) || optionsGetOwn(config.options, tokenName, "type") !== "boolean") {
        throw unknownOption(token.rawName, config.allowPositionals);
      }
    } else {
      throw unknownOption(token.rawName, config.allowPositionals);
    }
  }
  const short = optionsGetOwn(config.options, tokenName, "short");
  const shortAndLong = (short ? "-" + short + ", " : "") + "--" + tokenName;
  const type = optionsGetOwn(config.options, tokenName, "type");
  if (type === "string" && typeof token.value !== "string") {
    throw parseArgsError("ERR_PARSE_ARGS_INVALID_OPTION_VALUE", "Option '" + shortAndLong + " <value>' argument missing");
  }
  if (type === "boolean" && token.value != null) {
    throw parseArgsError("ERR_PARSE_ARGS_INVALID_OPTION_VALUE", "Option '" + shortAndLong + "' does not take an argument");
  }
}

function storeOption(longOption, optionValue, options, values, allowNegative) {
  if (longOption === "__proto__") return;
  let newValue = optionValue ?? true;
  if (allowNegative && longOption.startsWith("no-") && !Object.hasOwn(options, longOption)) {
    longOption = longOption.slice(3);
    newValue = false;
  }
  if (optionsGetOwn(options, longOption, "multiple")) {
    if (values[longOption]) values[longOption].push(newValue);
    else values[longOption] = [newValue];
  } else {
    values[longOption] = newValue;
  }
}

function argsToTokens(args, options) {
  const tokens = [];
  let index = -1;
  let groupCount = 0;
  const remainingArgs = args.slice();
  while (remainingArgs.length > 0) {
    const arg = remainingArgs.shift();
    const nextArg = remainingArgs[0];
    if (groupCount > 0) groupCount--;
    else index++;
    if (arg === "--") {
      tokens.push({ kind: "option-terminator", index });
      for (const rest of remainingArgs) tokens.push({ kind: "positional", index: ++index, value: rest });
      break;
    }
    if (isLoneShortOption(arg)) {
      const shortOption = arg[1];
      const longOption = findLongOptionForShort(shortOption, options);
      let value;
      let inlineValue;
      if (optionsGetOwn(options, longOption, "type") === "string" && isOptionValue(nextArg)) {
        value = remainingArgs.shift();
        inlineValue = false;
      }
      tokens.push({ kind: "option", name: longOption, rawName: arg, index, value, inlineValue });
      if (value != null) ++index;
      continue;
    }
    if (isShortOptionGroup(arg, options)) {
      const expanded = [];
      for (let i = 1; i < arg.length; i++) {
        const shortOption = arg[i];
        const longOption = findLongOptionForShort(shortOption, options);
        if (optionsGetOwn(options, longOption, "type") !== "string" || i === arg.length - 1) {
          expanded.push("-" + shortOption);
        } else {
          expanded.push("-" + arg.slice(i));
          break;
        }
      }
      remainingArgs.unshift(...expanded);
      groupCount = expanded.length;
      continue;
    }
    if (isShortOptionAndValue(arg, options)) {
      const shortOption = arg[1];
      const longOption = findLongOptionForShort(shortOption, options);
      tokens.push({ kind: "option", name: longOption, rawName: "-" + shortOption, index, value: arg.slice(2), inlineValue: true });
      continue;
    }
    if (isLoneLongOption(arg)) {
      const longOption = arg.slice(2);
      let value;
      let inlineValue;
      if (optionsGetOwn(options, longOption, "type") === "string" && isOptionValue(nextArg)) {
        value = remainingArgs.shift();
        inlineValue = false;
      }
      tokens.push({ kind: "option", name: longOption, rawName: arg, index, value, inlineValue });
      if (value != null) ++index;
      continue;
    }
    if (isLongOptionAndValue(arg)) {
      const equalIndex = arg.indexOf("=");
      const longOption = arg.slice(2, equalIndex);
      tokens.push({ kind: "option", name: longOption, rawName: "--" + longOption, index, value: arg.slice(equalIndex + 1), inlineValue: true });
      continue;
    }
    tokens.push({ kind: "positional", index, value: arg });
  }
  return tokens;
}

function getMainArgs() {
  const evalFlags = ["-e", "--eval", "-p", "--print"];
  if (process.execArgv.some((a) => evalFlags.includes(a) || a.startsWith("--eval=") || a.startsWith("--print="))) {
    return process.argv.slice(1);
  }
  return process.argv.slice(2);
}

export function parseArgs(config = {}) {
  const args = objectGetOwn(config, "args") ?? getMainArgs();
  const strict = objectGetOwn(config, "strict") ?? true;
  const allowPositionals = objectGetOwn(config, "allowPositionals") ?? !strict;
  const returnTokens = objectGetOwn(config, "tokens") ?? false;
  const allowNegative = objectGetOwn(config, "allowNegative") ?? false;
  const options = objectGetOwn(config, "options") ?? { __proto__: null };
  const parseConfig = { args, strict, options, allowPositionals, allowNegative };

  validateArray(args, "args");
  validateBoolean(strict, "strict");
  validateBoolean(allowPositionals, "allowPositionals");
  validateBoolean(returnTokens, "tokens");
  validateBoolean(allowNegative, "allowNegative");
  validateObject(options, "options");
  for (const [longOption, optionConfig] of Object.entries(options)) {
    validateObject(optionConfig, "options." + longOption);
    const optionType = objectGetOwn(optionConfig, "type");
    if (optionType !== "string" && optionType !== "boolean") {
      const err = new TypeError('The "options.' + longOption + '.type" property must be (\'string|boolean\').' + received(optionType));
      err.code = "ERR_INVALID_ARG_TYPE";
      throw err;
    }
    if (Object.hasOwn(optionConfig, "short")) {
      const shortOption = optionConfig.short;
      validateString(shortOption, "options." + longOption + ".short");
      if (shortOption.length !== 1) {
        const err = new TypeError("The property 'options." + longOption + ".short' must be a single character. Received '" + shortOption + "'");
        err.code = "ERR_INVALID_ARG_VALUE";
        throw err;
      }
    }
    const multipleOption = objectGetOwn(optionConfig, "multiple");
    if (Object.hasOwn(optionConfig, "multiple")) validateBoolean(multipleOption, "options." + longOption + ".multiple");
    const defaultValue = objectGetOwn(optionConfig, "default");
    if (defaultValue !== undefined) {
      const name = "options." + longOption + ".default";
      if (optionType === "string") {
        if (multipleOption) {
          validateArray(defaultValue, name);
          defaultValue.forEach((v, i) => validateString(v, name + "[" + i + "]"));
        } else {
          validateString(defaultValue, name);
        }
      } else if (multipleOption) {
        validateArray(defaultValue, name);
        defaultValue.forEach((v, i) => validateBoolean(v, name + "[" + i + "]"));
      } else {
        validateBoolean(defaultValue, name);
      }
    }
  }

  const tokens = argsToTokens(args, options);
  const result = { values: { __proto__: null }, positionals: [] };
  if (returnTokens) result.tokens = tokens;
  for (const token of tokens) {
    if (token.kind === "option") {
      if (strict) {
        checkOptionUsage(parseConfig, token);
        checkOptionLikeValue(token);
      }
      storeOption(token.name, token.value, options, result.values, allowNegative);
    } else if (token.kind === "positional") {
      if (!allowPositionals) {
        throw parseArgsError("ERR_PARSE_ARGS_UNEXPECTED_POSITIONAL",
          "Unexpected argument '" + token.value + "'. This command does not take positional arguments");
      }
      result.positionals.push(token.value);
    }
  }
  for (const [longOption, optionConfig] of Object.entries(options)) {
    if (objectGetOwn(optionConfig, "default") !== undefined && result.values[longOption] === undefined && longOption !== "__proto__") {
      result.values[longOption] = objectGetOwn(optionConfig, "default");
    }
  }
  return result;
}
`

func declareUtilExtra() {
	registerJSShim("noderati-internal:util-extra", utilExtraShim)
}

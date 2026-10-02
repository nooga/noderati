package host

const childProcessShim = `import { Buffer } from "node:buffer";
import { promisify } from "node:util";

// Argument handling, shell expansion, and the exec/execFile family follow
// Node's own lib/child_process.js; the process work is Go
// (__noderatiSpawn / __noderatiSpawnSync).
const MAX_BUFFER = 1024 * 1024;

function invalidArgType(name, expected, actual) {
  const err = new TypeError('The "' + name + '" argument must be ' + expected + "." + receivedOf(actual));
  err.code = "ERR_INVALID_ARG_TYPE";
  return err;
}
function receivedOf(v) {
  if (v == null) return " Received " + v;
  if (typeof v === "function") return " Received function " + (v.name || "<anonymous>");
  if (typeof v === "object") return " Received an instance of " + ((v.constructor && v.constructor.name) || "Object");
  return " Received type " + typeof v + " (" + (typeof v === "string" ? "'" + v + "'" : String(v)) + ")";
}

// normalizeSpawnArguments: (file, args?, options?) with options.shell
// turning the command line into [shell, "-c", line].
function normalizeSpawnArguments(file, args, options) {
  if (typeof file !== "string") throw invalidArgType("file", "of type string", file);
  if (file.length === 0) {
    const err = new TypeError("The argument 'file' cannot be empty. Received ''");
    err.code = "ERR_INVALID_ARG_VALUE";
    throw err;
  }
  if (Array.isArray(args)) {
    args = args.slice();
  } else if (args == null) {
    args = [];
  } else if (typeof args !== "object") {
    throw invalidArgType("args", "of type object", args);
  } else {
    options = args;
    args = [];
  }
  if (options === undefined) options = {};
  else if (options === null || typeof options !== "object") throw invalidArgType("options", "of type object", options);
  options = { ...options };
  if (options.shell) {
    const command = args.length ? [file, ...args].join(" ") : file;
    file = typeof options.shell === "string" ? options.shell : "/bin/sh";
    args = ["-c", command];
  }
  return { file, args: args.map(String), options };
}

export function spawn(file, args, options) {
  const n = normalizeSpawnArguments(file, args, options);
  return globalThis.__noderatiSpawn(n.file, n.args, n.options);
}

function errnoException(code, errno, syscall) {
  const err = new Error(syscall + " " + code);
  err.errno = errno;
  err.code = code;
  err.syscall = syscall;
  return err;
}

export function spawnSync(file, args, options) {
  const n = normalizeSpawnArguments(file, args, options);
  const opts = { maxBuffer: MAX_BUFFER, ...n.options };
  if (opts.input !== undefined && opts.input !== null && typeof opts.input !== "string" && !ArrayBuffer.isView(opts.input)) {
    throw invalidArgType("options.stdio[0]", "of type string or an instance of Buffer, TypedArray, or DataView", opts.input);
  }
  const raw = globalThis.__noderatiSpawnSync(n.file, n.args, opts);
  const result = {};
  if (raw.errorCode) {
    const err = errnoException(raw.errorCode, raw.errno, "spawnSync " + n.file);
    err.path = n.file;
    err.spawnargs = n.args;
    result.error = err;
  }
  let output = raw.output;
  if (output && opts.encoding && opts.encoding !== "buffer") {
    output = output.map((b) => (b ? b.toString(opts.encoding) : b));
  }
  result.status = raw.status;
  result.signal = raw.signal;
  result.output = output;
  result.pid = raw.pid;
  result.stdout = output ? output[1] : undefined;
  result.stderr = output ? output[2] : undefined;
  return result;
}

function checkExecSyncError(ret, args, cmd) {
  let err;
  if (ret.error) {
    err = ret.error;
    Object.assign(err, ret);
  } else if (ret.status !== 0) {
    let msg = "Command failed: " + (cmd || args.join(" "));
    if (ret.stderr && ret.stderr.length > 0) msg += "\n" + ret.stderr.toString();
    err = Object.assign(new Error(msg), ret);
  }
  return err;
}

export function execFileSync(file, args, options) {
  const n = normalizeSpawnArguments(file, args, options);
  const inheritStderr = !n.options.stdio;
  const ret = spawnSync(n.file, n.args, n.options);
  if (inheritStderr && ret.stderr) process.stderr.write(ret.stderr);
  const err = checkExecSyncError(ret, [n.options.argv0 || file, ...(Array.isArray(args) ? args : [])]);
  if (err) throw err;
  return ret.stdout;
}

function normalizeExecArgs(command, options, callback) {
  if (typeof command !== "string") throw invalidArgType("command", "of type string", command);
  if (typeof options === "function") {
    callback = options;
    options = undefined;
  }
  options = { ...options };
  options.shell = typeof options.shell === "string" ? options.shell : true;
  return { file: command, options, callback };
}

export function execSync(command, options) {
  const opts = normalizeExecArgs(command, options, null);
  const inheritStderr = !opts.options.stdio;
  const ret = spawnSync(opts.file, opts.options);
  if (inheritStderr && ret.stderr) process.stderr.write(ret.stderr);
  const err = checkExecSyncError(ret, undefined, command);
  if (err) throw err;
  return ret.stdout;
}

export function execFile(file, args, options, callback) {
  if (typeof args === "function") {
    callback = args;
    args = [];
    options = undefined;
  } else if (args != null && !Array.isArray(args) && typeof args === "object") {
    callback = options;
    options = args;
    args = [];
  } else if (typeof options === "function") {
    callback = options;
    options = undefined;
  }
  if (args == null) args = [];
  if (callback !== undefined && callback !== null && typeof callback !== "function") {
    throw invalidArgType("callback", "of type function", callback);
  }
  options = { encoding: "utf8", timeout: 0, maxBuffer: MAX_BUFFER, killSignal: "SIGTERM", cwd: null, env: null, shell: false, ...options };

  const child = spawn(file, args, {
    cwd: options.cwd, env: options.env, shell: options.shell, detached: options.detached, stdio: options.stdio,
  });

  const encoding = options.encoding !== "buffer" && Buffer.isEncoding(options.encoding) ? options.encoding : null;
  const _stdout = [];
  const _stderr = [];
  let stdoutLen = 0;
  let stderrLen = 0;
  let killed = false;
  let exited = false;
  let timeoutId;
  let ex = null;
  let cmd = file;

  function exithandler(code, signal) {
    if (exited) return;
    exited = true;
    if (timeoutId) {
      clearTimeout(timeoutId);
      timeoutId = null;
    }
    if (!callback) return;
    const stdout = encoding ? _stdout.join("") : Buffer.concat(_stdout);
    const stderr = encoding ? _stderr.join("") : Buffer.concat(_stderr);
    if (!ex && code === 0 && signal === null) {
      callback(null, stdout, stderr);
      return;
    }
    if (args.length) cmd += " " + args.join(" ");
    if (!ex) {
      ex = new Error("Command failed: " + cmd + "\n" + stderr);
      ex.code = code;
      ex.killed = child.killed || killed;
      ex.signal = signal;
    }
    ex.cmd = cmd;
    callback(ex, stdout, stderr);
  }
  function errorhandler(e) {
    ex = e;
    exithandler();
  }
  function kill() {
    killed = true;
    try {
      child.kill(options.killSignal);
    } catch (e) {
      ex = e;
      exithandler();
    }
  }
  if (options.timeout > 0) {
    timeoutId = setTimeout(() => {
      kill();
      timeoutId = null;
    }, options.timeout);
  }
  function collect(stream, chunks, which) {
    if (!stream) return;
    if (encoding) stream.setEncoding(encoding);
    stream.on("data", (chunk) => {
      const length = typeof chunk === "string" ? Buffer.byteLength(chunk, encoding) : chunk.length;
      const total = (which === "stdout" ? (stdoutLen += length) : (stderrLen += length));
      if (total > options.maxBuffer) {
        const truncatedLen = options.maxBuffer - (total - length);
        chunks.push(chunk.slice(0, truncatedLen));
        ex = new RangeError(which + " maxBuffer length exceeded");
        ex.code = "ERR_CHILD_PROCESS_STDIO_MAXBUFFER";
        kill();
      } else {
        chunks.push(chunk);
      }
    });
  }
  collect(child.stdout, _stdout, "stdout");
  collect(child.stderr, _stderr, "stderr");
  child.on("close", exithandler);
  child.on("error", errorhandler);
  return child;
}

export function exec(command, options, callback) {
  const opts = normalizeExecArgs(command, options, callback);
  return execFile(opts.file, opts.options, opts.callback);
}

// util.promisify(exec/execFile) resolves { stdout, stderr } and attaches
// both to the rejection error, as Node's customPromiseExecFunction does.
function customPromiseExecFunction(orig) {
  return (...args) => {
    let resolve, reject;
    const promise = new Promise((res, rej) => {
      resolve = res;
      reject = rej;
    });
    promise.child = orig(...args, (err, stdout, stderr) => {
      if (err !== null) {
        err.stdout = stdout;
        err.stderr = stderr;
        reject(err);
      } else {
        resolve({ stdout, stderr });
      }
    });
    return promise;
  };
}
Object.defineProperty(exec, promisify.custom, { enumerable: false, value: customPromiseExecFunction(exec) });
Object.defineProperty(execFile, promisify.custom, { enumerable: false, value: customPromiseExecFunction(execFile) });

// fork(modulePath, [args], [options]) - real Node's own argument
// handling (lib/child_process.js): spawn process.execPath with
// [...execArgv, modulePath, ...args] and an IPC channel; stdio defaults
// to "inherit" ("pipe" with silent: true).
export function fork(modulePath, args, options) {
  if (modulePath && typeof modulePath === "object" && typeof modulePath.href === "string") {
    modulePath = decodeURIComponent(new URL(modulePath.href).pathname);
  }
  if (typeof modulePath !== "string") {
    const err = new TypeError('The "modulePath" argument must be of type string');
    err.code = "ERR_INVALID_ARG_TYPE";
    throw err;
  }
  if (args == null) {
    args = [];
  } else if (typeof args === "object" && !Array.isArray(args)) {
    options = args;
    args = [];
  }
  options = { ...options };
  const execPath = options.execPath || process.execPath;
  const execArgv = options.execArgv || process.execArgv;
  if (options.stdio === undefined) options.stdio = options.silent ? "pipe" : "inherit";
  if (Array.isArray(options.stdio) && !options.stdio.includes("ipc")) {
    const err = new Error("Forked processes must have an IPC channel, missing value 'ipc' in options.stdio");
    err.code = "ERR_CHILD_PROCESS_IPC_REQUIRED";
    throw err;
  }
  const argv = [...execArgv, modulePath, ...args.map(String)];
  return globalThis.__noderatiFork(execPath, argv, options);
}

export default { spawn, spawnSync, fork, exec, execSync, execFile, execFileSync };
`

func declareChildProcess() {
	registerJSShim("child_process", childProcessShim)
}

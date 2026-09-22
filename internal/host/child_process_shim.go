package host

const childProcessShim = `function collectCommandArgs(rest) {
  if (rest.length === 0) return [];
  if (Array.isArray(rest[0])) return rest[0].map(String);
  if (typeof rest[0] === "object" && rest[0] !== null) return [];
  return rest.map(String);
}

export function spawnSync(command, ...rest) {
  const args = collectCommandArgs(rest);
  return globalThis.__noderatiSpawnSync(command, args);
}

export function spawn(command, args, options) {
  if (args === undefined) {
    return globalThis.__noderatiSpawn(command, [], options ?? {});
  }
  if (Array.isArray(args)) {
    return globalThis.__noderatiSpawn(command, args.map(String), options ?? {});
  }
  if (typeof args === "object" && args !== null) {
    return globalThis.__noderatiSpawn(command, [], args);
  }
  return globalThis.__noderatiSpawn(command, [String(args)], options ?? {});
}

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

export default { spawn, spawnSync, fork };
`

func declareChildProcess() {
	registerJSShim("child_process", childProcessShim)
}

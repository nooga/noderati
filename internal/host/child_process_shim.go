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

export default { spawn, spawnSync };
`

func declareChildProcess() {
	registerJSShim("child_process", childProcessShim)
}

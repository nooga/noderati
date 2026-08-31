# noderati

Node-shaped host for [Paserati](https://github.com/nooga/paserati). Paserati stays the ES/TS language runtime; this repo supplies Node-like globals and builtins.

## Local development

This tree expects a sibling Paserati checkout that includes the host embed API. `go.mod` and `go.work` currently point at `../paserati`. Change that path if your checkout lives elsewhere.

```bash
go build -o noderati ./cmd/noderati
./noderati -e 'console.log("ok")'
./noderati -e 'import { join } from "path"; console.log(join("a", "b"))'
./noderati script.ts
./noderati script.cjs   # CommonJS require()
```

`process.argv` is `[execPath, script, …]` like Node. Process exit status is 0 or 1 (not sysexits 70).

## Status

- CLI: file, `-e`, `-p`, REPL; shebang stripped
- `process` (owned here): argv, env, cwd, exit, nextTick, stdout/stderr TTY, `global`
- `setTimeout` / `nextTick` via Paserati’s opt-in host timers
- Real builtins: `path`, `os`, `util`, `fs` (+ `fs/promises`), `url`, `querystring`,
  `assert`, `buffer`, `events`, `crypto`, `child_process.spawnSync`, `readline`,
  `tty`, `worker_threads`, `perf_hooks`, `module` (`createRequire`), `constants`,
  plus `node:` aliases
- `require()` for CommonJS; ESM `import` of CJS packages (default export)
- `node_modules` resolution (incl. package.json `"imports"` `#specifiers`);
  relative imports from the real OS path of the entry file
- Not yet: `net`/`tls`, N-API, a real `stream` beyond a basic EventEmitter base,
  a real `string_decoder`

This list is the shipped Node surface only. For the actual state of the effort
— what's a real implementation vs. a package-specific fake still waiting to be
deleted, and what's blocking real npm packages from running unmodified — see
[`docs/real-node-plan.md`](docs/real-node-plan.md), the single status source
of truth for that work.

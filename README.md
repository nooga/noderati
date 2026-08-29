# noderati

Node-shaped host for [Paserati](https://github.com/nooga/paserati). Paserati stays the ES/TS language runtime; this repo supplies Node-like globals and builtins.

## Local development

This tree expects a sibling Paserati checkout that includes the host embed API. `go.mod` and `go.work` currently point at `../paserati-noderati-host` (the `noderati-host-api` worktree). Change that path if your checkout lives elsewhere.

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
- `fs` (sync subset), `path`, `os`, `util`, `url`, `querystring`, `assert`, `child_process.spawnSync` plus `node:` aliases
- `require()` for CommonJS; ESM `import` of CJS packages (default export)
- `node_modules` resolution; relative imports from the real OS path of the entry file
- Not yet: `Buffer`, `events`, `stream`, `fs/promises`, `crypto`, `readline`, `net`/`tls`, N-API

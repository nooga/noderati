# noderati

Node-shaped host for [Paserati](https://github.com/nooga/paserati). Paserati stays the ES/TS language runtime; this repo supplies Node-like globals and builtins.

## Local development

This tree expects a sibling Paserati checkout that includes the host embed API. `go.mod` and `go.work` currently point at `../paserati-noderati-host` (the `noderati-host-api` worktree). Change that path if your checkout lives elsewhere.

```bash
go build -o noderati ./cmd/noderati
./noderati -e 'console.log("ok")'
./noderati -p 'require("path").join("a", "b")'  # not yet — use ESM:
./noderati -e 'import { join } from "path"; console.log(join("a", "b"))'
./noderati script.ts
```

`process.argv` is `[execPath, script, …]` like Node. Process exit status is 0 or 1 (not sysexits 70).

## Status

- CLI: file, `-e`, `-p`, REPL
- `process` (owned here, not grown in Paserati)
- `setTimeout` / `nextTick` via Paserati’s opt-in host timers
- `path`, `os`, `util` plus `node:` aliases
- Not yet: `fs`, `Buffer`, `require()`, `node_modules`, N-API

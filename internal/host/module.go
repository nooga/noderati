package host

// This shim imports another builtin ("path") at its own top level - the
// first cross-builtin dependency among noderati's own shims. Safe today
// because "path" has no dependency back on "module"; if that ever changes,
// this becomes a require cycle.
const moduleShim = `import { dirname } from "path";

export function createRequire(filenameOrURL) {
  return process.__noderatiCreateRequire(filenameOrURL);
}
export function _nodeModulePaths(from) {
  return process.__noderatiNodeModulePaths(from);
}
export const builtinModules = process.__noderatiBuiltinModules;

// Real Node's require("module") returns a callable constructor (Module
// itself), with Module.Module === Module (self-reference, for ESM
// interop), and Module.builtinModules/._nodeModulePaths/.createRequire
// attached as static properties directly on it - not a plain namespace
// object holding them. Matched here because real code depends on both
// shapes: jiti's own module-loading core does 'new Module(id)' to build a
// bare module record for a freshly transformed file (see docs/
// real-node-plan.md's round 49 entry - this is exactly what was missing),
// and 'Module._nodeModulePaths(...)'/'Module.builtinModules.includes(...)'
// as static lookups on that same constructor, not a separate export.
export class Module {
  constructor(id = "", parent) {
    this.id = id;
    this.path = id ? dirname(id) : "";
    this.exports = {};
    this.filename = null;
    this.loaded = false;
    this.children = [];
    this.parent = parent;
  }
}
Module.Module = Module;
Module.builtinModules = builtinModules;
Module._nodeModulePaths = _nodeModulePaths;
Module.createRequire = createRequire;

export default Module;
`

func declareModule() {
	registerJSShim("module", moduleShim)
}

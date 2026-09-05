package host

const moduleShim = `export function createRequire(filenameOrURL) {
  return process.__noderatiCreateRequire(filenameOrURL);
}
export function _nodeModulePaths(from) {
  return process.__noderatiNodeModulePaths(from);
}
export const builtinModules = process.__noderatiBuiltinModules;
export default { createRequire, _nodeModulePaths, builtinModules };
`

func declareModule() {
	registerJSShim("module", moduleShim)
}

package host

const moduleShim = `export function createRequire(filenameOrURL) {
  return process.__noderatiCreateRequire(filenameOrURL);
}
export default { createRequire };
`

func declareModule() {
	registerJSShim("module", moduleShim)
}

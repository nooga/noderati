package host

const globShim = `export function globSync(_pattern, _options) { return []; }
export default { globSync };
`

const minimatchShim = `export function minimatch(_str, _pattern, _options) { return false; }
export default minimatch;
`

func declareGlob() {
	registerJSShim("glob", globShim)
}

func declareMinimatch() {
	registerJSShim("minimatch", minimatchShim)
}

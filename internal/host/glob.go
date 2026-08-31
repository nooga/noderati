package host

const globShim = `export function globSync(_pattern, _options) { return []; }
export default { globSync };
`

func declareGlob() {
	registerJSShim("glob", globShim)
}

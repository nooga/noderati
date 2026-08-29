package host

const diffShim = `export function diffLines(_oldStr, _newStr, _options) { return []; }
export function diffChars(_oldStr, _newStr) { return []; }
export default { diffLines, diffChars };
`

func declareDiff() {
	registerJSShim("diff", diffShim)
}

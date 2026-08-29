package host

const jitiShim = `export function createJiti(_root) {
  return function (_id) { return {}; };
}
export default { createJiti };
`

func declareJiti() {
	registerJSShim("jiti/static", jitiShim)
}

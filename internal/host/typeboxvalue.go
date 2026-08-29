package host

const typeboxValueShim = `export function Check(_schema, _value) { return true; }
export function Errors(_schema, _value) { return []; }
export default { Check, Errors };
`

func declareTypeboxValue() {
	registerJSShim("typebox/value", typeboxValueShim)
}

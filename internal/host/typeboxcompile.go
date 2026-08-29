package host

const typeboxCompileShim = `export function Compile(_schema) {
  const validator = {
    Check(value) { return value != null && typeof value === "object"; },
    Errors(_value) { return []; },
  };
  return validator;
}
export default { Compile };
`

func declareTypeboxCompile() {
	registerJSShim("typebox/compile", typeboxCompileShim)
}

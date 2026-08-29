package host

const typeboxShim = `function makeSchema(type) { return { type }; }

export const Type = {
  String: () => makeSchema("string"),
  Number: (_opts) => makeSchema("number"),
  Integer: (_opts) => makeSchema("integer"),
  Boolean: () => makeSchema("boolean"),
  Object: (_props, _opts) => makeSchema("object"),
  Union: (types) => ({ anyOf: types }),
  Optional: (t) => t,
  Record: (_k, _v) => makeSchema("object"),
  Array: (t) => ({ type: "array", items: t }),
  Literal: (v) => ({ const: v }),
  Any: () => ({}),
  Intersect: (types) => ({ allOf: types }),
  Enum: (values) => ({ enum: values }),
  Null: () => makeSchema("null"),
  Never: () => makeSchema("null"),
};

export default Type;
`

func declareTypebox() {
	registerJSShim("typebox", typeboxShim)
}
